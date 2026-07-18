package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func mkPlugin(t *testing.T, home, name, manifestRel, manifest string, withSkills bool) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "plugins", name)
	mp := filepath.Join(dir, manifestRel)
	if err := os.MkdirAll(filepath.Dir(mp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mp, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if withSkills {
		sk := filepath.Join(dir, "skills", "demo")
		_ = os.MkdirAll(sk, 0o755)
		_ = os.WriteFile(filepath.Join(sk, "SKILL.md"), []byte("---\nname: demo\n---\nbody"), 0o644)
	}
}

func TestLoadBothManifestLocations(t *testing.T) {
	home := t.TempDir()
	mkPlugin(t, home, "alpha", "plugin.json",
		`{"name":"alpha","description":"A","version":"1.0","mcpServers":{"s":{"command":"x"}}}`, true)
	mkPlugin(t, home, "beta", ".claude-plugin/plugin.json",
		`{"name":"beta","description":"B"}`, false)

	ps := Load(home, nil)
	if len(ps) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(ps))
	}
	if ps[0].Name != "alpha" || ps[1].Name != "beta" {
		t.Fatalf("sort/name: %+v", ps)
	}
	if ps[0].SkillsDir == "" {
		t.Fatal("alpha should have a skills dir")
	}
	if ps[1].SkillsDir != "" {
		t.Fatal("beta should have no skills dir")
	}
}

func TestDisabledSkipped(t *testing.T) {
	home := t.TempDir()
	mkPlugin(t, home, "gamma", "plugin.json", `{"name":"gamma"}`, false)
	ps := Load(home, map[string]bool{"gamma": true})
	if len(ps) != 0 {
		t.Fatalf("disabled plugin should be skipped, got %d", len(ps))
	}
}

func TestCollectMCPServersNamespaced(t *testing.T) {
	home := t.TempDir()
	mkPlugin(t, home, "alpha", "plugin.json",
		`{"name":"alpha","mcpServers":{"db":{"command":"x"}}}`, false)
	ps := Load(home, nil)
	m := CollectMCPServers(ps)
	if _, ok := m["alpha:db"]; !ok {
		t.Fatalf("expected namespaced server alpha:db, got %v", m)
	}
}

func TestMissingPluginsDir(t *testing.T) {
	if ps := Load(t.TempDir(), nil); ps != nil {
		t.Fatalf("no plugins dir should yield nil, got %+v", ps)
	}
}
