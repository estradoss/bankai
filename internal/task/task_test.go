package task

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitStatus(t *testing.T, r *Registry, id string, want Status) Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, ok := r.Get(id)
		if !ok {
			t.Fatalf("task %s vanished", id)
		}
		if s.Status == want {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task %s never reached %s", id, want)
	return Snapshot{}
}

func TestCreateCompletes(t *testing.T) {
	r := NewRegistry(func(ctx context.Context, prompt string) (string, error) {
		return "done: " + prompt, nil
	})
	s, err := r.Create("desc", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != StatusRunning {
		t.Fatalf("initial status should be running, got %s", s.Status)
	}
	final := waitStatus(t, r, s.ID, StatusCompleted)
	if final.Output != "done: hello" {
		t.Fatalf("output = %q", final.Output)
	}
}

func TestCreateFails(t *testing.T) {
	r := NewRegistry(func(ctx context.Context, prompt string) (string, error) {
		return "", errors.New("boom")
	})
	s, _ := r.Create("d", "p")
	final := waitStatus(t, r, s.ID, StatusFailed)
	if final.Error != "boom" {
		t.Fatalf("error = %q", final.Error)
	}
}

func TestStopCancels(t *testing.T) {
	r := NewRegistry(func(ctx context.Context, prompt string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	s, _ := r.Create("d", "p")
	if !r.Stop(s.ID) {
		t.Fatal("Stop returned false for known task")
	}
	final := waitStatus(t, r, s.ID, StatusStopped)
	if final.Status != StatusStopped {
		t.Fatalf("status = %s", final.Status)
	}
}

func TestUnknownTask(t *testing.T) {
	r := NewRegistry(func(context.Context, string) (string, error) { return "", nil })
	if _, ok := r.Get("nope"); ok {
		t.Fatal("Get should miss")
	}
	if r.Stop("nope") {
		t.Fatal("Stop should return false for unknown")
	}
}

func TestListOrder(t *testing.T) {
	r := NewRegistry(func(context.Context, string) (string, error) { return "x", nil })
	a, _ := r.Create("first", "1")
	b, _ := r.Create("second", "2")
	list := r.List()
	if len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("list order wrong: %+v", list)
	}
}
