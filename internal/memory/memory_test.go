package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveListGet(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(Memory{Name: "User Profile", Description: "who", Type: TypeUser, Body: "Go expert"}); err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("list = %d", len(list))
	}
	if list[0].Name != "user-profile" {
		t.Fatalf("name not slugified: %q", list[0].Name)
	}
	m, ok := s.Get("user-profile")
	if !ok || m.Type != TypeUser || m.Body != "Go expert" {
		t.Fatalf("get: %+v ok=%v", m, ok)
	}
}

func TestIndexWritten(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	_ = s.Save(Memory{Name: "a", Description: "first", Body: "x"})
	_ = s.Save(Memory{Name: "b", Description: "second", Body: "y"})
	idx := s.Index()
	if !strings.Contains(idx, "a.md") || !strings.Contains(idx, "second") {
		t.Fatalf("index = %q", idx)
	}
	if _, err := os.Stat(filepath.Join(dir, "MEMORY.md")); err != nil {
		t.Fatalf("MEMORY.md missing: %v", err)
	}
}

func TestDeleteRemovesAndUpdatesIndex(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	_ = s.Save(Memory{Name: "keep", Body: "1"})
	_ = s.Save(Memory{Name: "drop", Body: "2"})
	if err := s.Delete("drop"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("drop"); ok {
		t.Fatal("drop should be gone")
	}
	if strings.Contains(s.Index(), "drop") {
		t.Fatal("index still references drop")
	}
	// Deleting the last one removes MEMORY.md.
	_ = s.Delete("keep")
	if s.Index() != "" {
		t.Fatalf("index should be empty, got %q", s.Index())
	}
}

func TestFindRelevant(t *testing.T) {
	s := NewStore(t.TempDir())
	_ = s.Save(Memory{Name: "auth", Description: "authentication flow", Body: "uses OAuth tokens and refresh"})
	_ = s.Save(Memory{Name: "colors", Description: "ui palette", Body: "blue and green theme"})
	got := s.FindRelevant("how does oauth authentication work", 5)
	if len(got) == 0 || got[0].Name != "auth" {
		t.Fatalf("relevant = %+v", got)
	}
	if len(s.FindRelevant("nonexistent-topic-xyz", 5)) != 0 {
		t.Fatal("expected no matches")
	}
}

func TestParseRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	_ = s.Save(Memory{Name: "rt", Description: "d", Type: TypeFeedback, Body: "line one\nline two"})
	m, _ := s.Get("rt")
	if m.Type != TypeFeedback || !strings.Contains(m.Body, "line two") {
		t.Fatalf("round trip: %+v", m)
	}
}

func TestParseTypeDefaults(t *testing.T) {
	s := NewStore(t.TempDir())
	_ = s.Save(Memory{Name: "notype", Body: "x"}) // no type -> project
	m, _ := s.Get("notype")
	if m.Type != TypeProject {
		t.Fatalf("default type = %q", m.Type)
	}
}
