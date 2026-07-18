package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFrontmatter(t *testing.T) {
	sk := parse("---\nname: foo\ndescription: \"does a thing\"\n---\n\nBody here.")
	if sk.Name != "foo" || sk.Description != "does a thing" {
		t.Fatalf("frontmatter parse: %+v", sk)
	}
	if sk.Body != "Body here." {
		t.Fatalf("body = %q", sk.Body)
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	sk := parse("just a body")
	if sk.Name != "" || sk.Body != "just a body" {
		t.Fatalf("got %+v", sk)
	}
}

func TestLoadAndOverride(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	writeSkill(t, home, "shared", "---\nname: shared\ndescription: user version\n---\nUSER")
	writeSkill(t, home, "useronly", "---\nname: useronly\ndescription: u\n---\nU")
	writeSkill(t, proj, "shared", "---\nname: shared\ndescription: project version\n---\nPROJECT")

	set := Load(home, proj)
	if set.Len() != 2 {
		t.Fatalf("expected 2 skills, got %d", set.Len())
	}
	sk, ok := set.Get("shared")
	if !ok || sk.Body != "PROJECT" || sk.Source != SourceProject {
		t.Fatalf("project should override user: %+v", sk)
	}
}

func TestNameFallsBackToDir(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "mydir", "no frontmatter body")
	set := Load(home, "")
	if _, ok := set.Get("mydir"); !ok {
		t.Fatalf("skill should be keyed by dir name; have %v", set.List())
	}
}

func TestMissingDir(t *testing.T) {
	set := Load(t.TempDir(), t.TempDir())
	if set.Len() != 0 {
		t.Fatal("missing skills dir should yield empty set")
	}
}
