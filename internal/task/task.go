// Package task provides an in-process registry of background sub-agent tasks.
// It is the Go port of vibelearn's async Task management (src/tasks/): tasks are
// launched asynchronously, run to completion in their own goroutine, and can be
// polled (Get/List/Output) or cancelled (Stop) while running.
package task

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Status is a task's lifecycle state.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusStopped   Status = "stopped"
)

// Runner executes a task's prompt to completion and returns its final output.
// It must honor ctx cancellation for Stop to work promptly.
type Runner func(ctx context.Context, prompt string) (string, error)

// Task is a single background sub-agent invocation.
type Task struct {
	ID          string
	Description string
	Prompt      string

	mu      sync.Mutex
	status  Status
	output  string
	err     string
	started time.Time
	ended   time.Time
	cancel  context.CancelFunc
}

// Snapshot is an immutable view of a task's state.
type Snapshot struct {
	ID          string
	Description string
	Status      Status
	Output      string
	Error       string
	Started     time.Time
	Ended       time.Time
}

func (t *Task) snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Snapshot{
		ID:          t.ID,
		Description: t.Description,
		Status:      t.status,
		Output:      t.output,
		Error:       t.err,
		Started:     t.started,
		Ended:       t.ended,
	}
}

// Registry stores and runs tasks.
type Registry struct {
	mu     sync.Mutex
	tasks  map[string]*Task
	order  []string
	nextID int
	runner Runner
}

// NewRegistry returns a registry that launches tasks with the given runner.
func NewRegistry(runner Runner) *Registry {
	return &Registry{tasks: map[string]*Task{}, nextID: 1, runner: runner}
}

// Create registers a task and starts it running in the background. It returns
// the new task's snapshot immediately (status running).
func (r *Registry) Create(description, prompt string) (Snapshot, error) {
	if r.runner == nil {
		return Snapshot{}, fmt.Errorf("task runner not configured")
	}
	r.mu.Lock()
	id := fmt.Sprintf("task_%d", r.nextID)
	r.nextID++
	ctx, cancel := context.WithCancel(context.Background())
	t := &Task{
		ID:          id,
		Description: description,
		Prompt:      prompt,
		status:      StatusRunning,
		started:     time.Now(),
		cancel:      cancel,
	}
	r.tasks[id] = t
	r.order = append(r.order, id)
	runner := r.runner
	r.mu.Unlock()

	go func() {
		out, err := runner(ctx, prompt)
		t.mu.Lock()
		defer t.mu.Unlock()
		t.ended = time.Now()
		if t.status == StatusStopped {
			return // Stop already set terminal state
		}
		if err != nil {
			if ctx.Err() != nil {
				t.status = StatusStopped
			} else {
				t.status = StatusFailed
			}
			t.err = err.Error()
			return
		}
		t.status = StatusCompleted
		t.output = out
	}()

	return t.snapshot(), nil
}

// Get returns a task's snapshot.
func (r *Registry) Get(id string) (Snapshot, bool) {
	r.mu.Lock()
	t, ok := r.tasks[id]
	r.mu.Unlock()
	if !ok {
		return Snapshot{}, false
	}
	return t.snapshot(), true
}

// List returns snapshots of all tasks in creation order.
func (r *Registry) List() []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Snapshot, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.tasks[id].snapshot())
	}
	return out
}

// Stop cancels a running task. Returns false if the task is unknown. Stopping an
// already-finished task is a no-op that returns true.
func (r *Registry) Stop(id string) bool {
	r.mu.Lock()
	t, ok := r.tasks[id]
	r.mu.Unlock()
	if !ok {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status == StatusRunning {
		t.status = StatusStopped
		t.ended = time.Now()
		t.cancel()
	}
	return true
}
