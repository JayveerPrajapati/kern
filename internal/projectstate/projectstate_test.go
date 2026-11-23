package projectstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, root string, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInspectExplicitAndDisabled(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"schema_version":1,"managed":true,"guard":{"enabled":true}}`)
	if s := Inspect(root); !s.Managed || s.Source != SourceExplicit {
		t.Fatalf("explicit marker = %+v, want managed explicit", s)
	}
	writeConfig(t, root, `{"schema_version":1,"managed":false,"guard":{"enabled":true}}`)
	if s := Inspect(root); s.Managed || s.Source != SourceDisabled {
		t.Fatalf("managed=false = %+v, want disabled", s)
	}
	writeConfig(t, root, `{"schema_version":1,"managed":true,"guard":{"enabled":false}}`)
	if s := Inspect(root); s.Managed || s.Source != SourceDisabled {
		t.Fatalf("guard.enabled=false = %+v, want disabled", s)
	}
}

func TestInspectLegacyAndAncestors(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "index.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if s := Inspect(child); !s.Managed || s.Source != SourceLegacy {
		t.Fatalf("child legacy = %+v, want managed legacy", s)
	}
	writeConfig(t, root, `{"schema_version":1,"managed":true,"guard":{"enabled":true}}`)
	if s := Inspect(child); !s.Managed || s.Source != SourceExplicit {
		t.Fatalf("child explicit ancestor = %+v, want managed explicit", s)
	}
	writeConfig(t, child, `{"schema_version":1,"managed":false,"guard":{"enabled":false}}`)
	if s := Inspect(child); s.Managed || s.Source != SourceDisabled {
		t.Fatalf("nearest opt-out = %+v, want disabled", s)
	}
}

func TestInspectMalformedPreservesLegacy(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{`)
	if err := os.WriteFile(filepath.Join(root, ".kern", "index.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := Inspect(root)
	if !s.Managed || s.Source != SourceLegacy || s.Err == nil {
		t.Fatalf("malformed + legacy = %+v, want managed legacy with error", s)
	}
	writeConfig(t, root, `{"managed":true,"guard":{"enabled":true}}`)
	if err := os.Remove(filepath.Join(root, ".kern", "index.sqlite")); err != nil {
		t.Fatal(err)
	}
	if s := Inspect(root); !s.Managed || s.Source != SourceExplicit || s.Err != nil {
		t.Fatalf("comment-free explicit marker = %+v, want explicit without error", s)
	}
}

func TestEnsureMarkerPreservesOptOutAndCustomKeys(t *testing.T) {
	root := t.TempDir()
	path, changed, err := EnsureMarker(root)
	if err != nil || !changed {
		t.Fatalf("EnsureMarker(new) = %q changed=%v err=%v", path, changed, err)
	}
	var cfg map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["managed"] != true {
		t.Fatalf("new managed = %#v, want true", cfg["managed"])
	}
	guard, _ := cfg["guard"].(map[string]any)
	if guard["enabled"] != true {
		t.Fatalf("new guard.enabled = %#v, want true", guard["enabled"])
	}

	writeConfig(t, root, `{"schema_version":1,"llm":{"model":"custom"},"managed":false,"guard":{"enabled":true}}`)
	if _, changed, err := EnsureMarker(root); err != nil || changed {
		t.Fatalf("EnsureMarker(opt-out) changed=%v err=%v, want no-op", changed, err)
	}
	b, _ = os.ReadFile(path)
	var after map[string]any
	if err := json.Unmarshal(b, &after); err != nil {
		t.Fatal(err)
	}
	if after["managed"] != false {
		t.Fatalf("opt-out was overwritten: %s", b)
	}
	llm, _ := after["llm"].(map[string]any)
	if llm["model"] != "custom" {
		t.Fatalf("custom key was overwritten: %s", b)
	}
}

func TestEnsureMarkerUpgradesExistingConfig(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `{"llm":{"model":"custom"}}`)
	path, changed, err := EnsureMarker(root)
	if err != nil || !changed {
		t.Fatalf("EnsureMarker(upgrade) changed=%v err=%v", changed, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["managed"] != true {
		t.Fatalf("upgraded managed = %#v, want true", cfg["managed"])
	}
	llm, _ := cfg["llm"].(map[string]any)
	if llm["model"] != "custom" {
		t.Fatalf("custom key was overwritten: %s", b)
	}
}
