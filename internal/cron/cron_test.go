package cron

import (
	"testing"
	"time"
)

func TestParseValid(t *testing.T) {
	for _, s := range []string{"*/5 * * * *", "30 14 28 2 *", "0 0 * * 0", "0,30 9-17 * * 1-5"} {
		if Parse(s) == nil {
			t.Errorf("Parse(%q) = nil, want expr", s)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, s := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a * * * *"} {
		if Parse(s) != nil {
			t.Errorf("Parse(%q) != nil, want nil", s)
		}
	}
}

func TestNext(t *testing.T) {
	e := Parse("*/15 * * * *")
	if e == nil {
		t.Fatal("parse failed")
	}
	from := time.Date(2026, 7, 18, 10, 7, 0, 0, time.UTC)
	next := e.Next(from)
	if next.Minute() != 15 || next.Hour() != 10 {
		t.Errorf("Next = %v, want 10:15", next)
	}
}

func TestNextOneShotDate(t *testing.T) {
	e := Parse("30 14 28 2 *") // Feb 28 14:30
	from := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	next := e.Next(from)
	if next.Month() != time.February || next.Day() != 28 || next.Hour() != 14 || next.Minute() != 30 {
		t.Errorf("Next = %v, want Feb 28 14:30", next)
	}
}

func TestStoreAddDeleteList(t *testing.T) {
	s := NewStore(t.TempDir(), func(string) {})
	task, err := s.Add("*/5 * * * *", "do the thing", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 1 || got[0].ID != task.ID {
		t.Fatalf("List = %v", got)
	}
	if !s.Delete(task.ID) {
		t.Fatal("Delete returned false")
	}
	if len(s.List()) != 0 {
		t.Fatal("task not deleted")
	}
}

func TestDurablePersists(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir, func(string) {})
	task, err := s1.Add("0 9 * * *", "morning", true, true)
	if err != nil {
		t.Fatal(err)
	}
	s2 := NewStore(dir, func(string) {})
	got := s2.List()
	if len(got) != 1 || got[0].ID != task.ID {
		t.Fatalf("durable task did not persist: %v", got)
	}
}

func TestAddInvalidRejected(t *testing.T) {
	s := NewStore(t.TempDir(), func(string) {})
	if _, err := s.Add("bad", "x", true, false); err == nil {
		t.Fatal("expected error for invalid cron")
	}
}
