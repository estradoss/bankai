// Package cron is the Go port of vibelearn's ScheduleCronTool backend
// (utils/cron.ts + utils/cronTasks.ts). It parses standard 5-field cron
// expressions, computes next fire times, persists durable tasks to
// .claude/scheduled_tasks.json, and runs a scheduler that enqueues each task's
// prompt via an injected Runner. One-shot (recurring=false) tasks auto-delete
// after firing; recurring tasks auto-expire after MaxAgeDays.
package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxJobs           = 50
	DefaultMaxAgeDays = 30
)

// Expr is a parsed 5-field cron expression: minute hour day-of-month month
// day-of-week. Each field is the set of matching values.
type Expr struct {
	Min, Hour, Dom, Mon, Dow map[int]bool
	raw                      string
}

var fieldRanges = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

// Parse parses "M H DoM Mon DoW". Supports *, */n, a-b, a,b, and single values.
// Returns nil on malformed input.
func Parse(s string) *Expr {
	parts := strings.Fields(strings.TrimSpace(s))
	if len(parts) != 5 {
		return nil
	}
	sets := make([]map[int]bool, 5)
	for i, p := range parts {
		set, ok := parseField(p, fieldRanges[i][0], fieldRanges[i][1])
		if !ok {
			return nil
		}
		sets[i] = set
	}
	return &Expr{Min: sets[0], Hour: sets[1], Dom: sets[2], Mon: sets[3], Dow: sets[4], raw: strings.TrimSpace(s)}
}

func parseField(f string, lo, hi int) (map[int]bool, bool) {
	out := map[int]bool{}
	for _, chunk := range strings.Split(f, ",") {
		step := 1
		body := chunk
		if idx := strings.Index(chunk, "/"); idx >= 0 {
			body = chunk[:idx]
			n, err := strconv.Atoi(chunk[idx+1:])
			if err != nil || n <= 0 {
				return nil, false
			}
			step = n
		}
		start, end := lo, hi
		switch {
		case body == "*":
			// full range
		case strings.Contains(body, "-"):
			ab := strings.SplitN(body, "-", 2)
			a, e1 := strconv.Atoi(ab[0])
			b, e2 := strconv.Atoi(ab[1])
			if e1 != nil || e2 != nil {
				return nil, false
			}
			start, end = a, b
		default:
			v, err := strconv.Atoi(body)
			if err != nil {
				return nil, false
			}
			start, end = v, v
		}
		if start < lo || end > hi || start > end {
			return nil, false
		}
		for v := start; v <= end; v += step {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// Next returns the first fire time strictly after `from`, searching up to one
// year. Returns zero time if the expression matches no date in that window.
func (e *Expr) Next(from time.Time) time.Time {
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := from.Add(366 * 24 * time.Hour)
	for t.Before(limit) {
		if e.Mon[int(t.Month())] && e.Dom[t.Day()] && e.Dow[int(t.Weekday())] &&
			e.Hour[t.Hour()] && e.Min[t.Minute()] {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// Human renders a short human description of the schedule.
func (e *Expr) Human() string { return e.raw }

// Task is one scheduled job.
type Task struct {
	ID        string    `json:"id"`
	Cron      string    `json:"cron"`
	Prompt    string    `json:"prompt"`
	Recurring bool      `json:"recurring"`
	Durable   bool      `json:"durable"`
	Created   time.Time `json:"created"`
	NextRun   time.Time `json:"nextRun"`
}

// Runner enqueues a fired task's prompt. It must not block the scheduler.
type Runner func(prompt string)

// Store holds scheduled tasks and (optionally) runs them.
type Store struct {
	mu      sync.Mutex
	tasks   map[string]*Task
	nextID  int
	path    string // durable persistence file
	runner  Runner
	stop    chan struct{}
	running bool
}

// NewStore returns a store persisting durable tasks under projectDir/.claude.
func NewStore(projectDir string, runner Runner) *Store {
	s := &Store{
		tasks:  map[string]*Task{},
		nextID: 1,
		path:   filepath.Join(projectDir, ".claude", "scheduled_tasks.json"),
		runner: runner,
	}
	s.loadDurable()
	return s
}

func (s *Store) loadDurable() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var tasks []*Task
	if json.Unmarshal(data, &tasks) != nil {
		return
	}
	for _, t := range tasks {
		s.tasks[t.ID] = t
		if n, _ := strconv.Atoi(strings.TrimPrefix(t.ID, "cron_")); n >= s.nextID {
			s.nextID = n + 1
		}
	}
}

func (s *Store) saveDurable() {
	var durable []*Task
	for _, t := range s.tasks {
		if t.Durable {
			durable = append(durable, t)
		}
	}
	if len(durable) == 0 {
		os.Remove(s.path)
		return
	}
	sort.Slice(durable, func(i, j int) bool { return durable[i].ID < durable[j].ID })
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	data, _ := json.MarshalIndent(durable, "", "  ")
	os.WriteFile(s.path, data, 0o644)
}

// Add validates and registers a task. Returns the task or an error.
func (s *Store) Add(cronExpr, prompt string, recurring, durable bool) (*Task, error) {
	e := Parse(cronExpr)
	if e == nil {
		return nil, fmt.Errorf("invalid cron expression %q; expected 5 fields: M H DoM Mon DoW", cronExpr)
	}
	next := e.Next(time.Now())
	if next.IsZero() {
		return nil, fmt.Errorf("cron expression %q matches no date in the next year", cronExpr)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tasks) >= MaxJobs {
		return nil, fmt.Errorf("too many scheduled jobs (max %d); cancel one first", MaxJobs)
	}
	id := fmt.Sprintf("cron_%d", s.nextID)
	s.nextID++
	t := &Task{ID: id, Cron: cronExpr, Prompt: prompt, Recurring: recurring, Durable: durable, Created: time.Now(), NextRun: next}
	s.tasks[id] = t
	if durable {
		s.saveDurable()
	}
	return t, nil
}

// Delete removes a task by id. Returns false if unknown.
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return false
	}
	delete(s.tasks, id)
	s.saveDurable()
	return true
}

// List returns all tasks in id order.
func (s *Store) List() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Start launches the scheduler loop (idempotent). It ticks every minute, fires
// due tasks via the runner, reschedules recurring ones, and drops one-shots and
// expired recurring tasks.
func (s *Store) Start() {
	s.mu.Lock()
	if s.running || s.runner == nil {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	stop := s.stop
	s.mu.Unlock()

	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-tick.C:
				s.fireDue(now)
			}
		}
	}()
}

// Stop halts the scheduler loop.
func (s *Store) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		close(s.stop)
		s.running = false
	}
}

func (s *Store) fireDue(now time.Time) {
	s.mu.Lock()
	var toFire []string
	changed := false
	for id, t := range s.tasks {
		if now.Sub(t.Created) > DefaultMaxAgeDays*24*time.Hour {
			delete(s.tasks, id)
			changed = true
			continue
		}
		if !t.NextRun.IsZero() && !now.Before(t.NextRun) {
			toFire = append(toFire, t.Prompt)
			if t.Recurring {
				if e := Parse(t.Cron); e != nil {
					t.NextRun = e.Next(now)
				}
			} else {
				delete(s.tasks, id)
			}
			changed = true
		}
	}
	if changed {
		s.saveDurable()
	}
	runner := s.runner
	s.mu.Unlock()
	for _, p := range toFire {
		runner(p)
	}
}
