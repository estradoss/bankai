package goal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Status string

const (
	StatusActive        Status = "active"
	StatusPaused        Status = "paused"
	StatusBlocked       Status = "blocked"
	StatusBudgetLimited Status = "budget_limited"
	StatusComplete      Status = "complete"
)

// Goal is the persistent per-session goal record.
type Goal struct {
	Objective        string    `json:"objective"`
	Status           Status    `json:"status"`
	TokenBudget      int       `json:"token_budget,omitempty"`
	TokensUsed       int       `json:"tokens_used"`
	TimeUsedSeconds  int64     `json:"time_used_seconds"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (g *Goal) IsActive() bool { return g.Status == StatusActive }

func (g *Goal) IsTerminal() bool {
	return g.Status == StatusBudgetLimited || g.Status == StatusComplete
}

// RemainingTokens returns budget-tokens_used or -1 if no budget.
func (g *Goal) RemainingTokens() int {
	if g.TokenBudget <= 0 {
		return -1
	}
	rem := g.TokenBudget - g.TokensUsed
	if rem < 0 {
		return 0
	}
	return rem
}

// Store is a thread-safe on-disk goal for one session.
type Store struct {
	mu   sync.RWMutex
	path string
	goal *Goal
}

func NewStore(sessionDir string) *Store {
	return &Store{path: filepath.Join(sessionDir, "goal.json")}
}

// Load reads goal.json into memory. Missing file is not an error.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var g Goal
	if err := json.Unmarshal(b, &g); err != nil {
		return err
	}
	s.goal = &g
	return nil
}

func (s *Store) Get() *Goal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.goal == nil {
		return nil
	}
	cp := *s.goal
	return &cp
}

func (s *Store) Set(g *Goal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.goal = g
	return s.persistLocked()
}

func (s *Store) Update(mut func(*Goal)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.goal == nil {
		return fmt.Errorf("no active goal")
	}
	mut(s.goal)
	s.goal.UpdatedAt = time.Now()
	return s.persistLocked()
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.goal = nil
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) persistLocked() error {
	if s.goal == nil {
		return nil
	}
	b, err := json.MarshalIndent(s.goal, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

// AddUsage bumps tokens_used and time. Flips to budget_limited if over budget.
func (s *Store) AddUsage(tokens int, elapsed time.Duration) error {
	return s.Update(func(g *Goal) {
		g.TokensUsed += tokens
		g.TimeUsedSeconds += int64(elapsed.Seconds())
		if g.TokenBudget > 0 && g.TokensUsed >= g.TokenBudget && g.Status == StatusActive {
			g.Status = StatusBudgetLimited
		}
	})
}
