package profiles

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryRegisterSelectList(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(MachineJSONProfile()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// duplicate -> error
	if err := r.Register(MachineJSONProfile()); err == nil {
		t.Error("duplicate registration should error")
	}
	// empty name -> error
	if err := r.Register(OutputProfile{Name: ""}); err == nil {
		t.Error("empty-name registration should error")
	}
	// select ok
	p, ok := r.Select("machine-json")
	if !ok || p.Name != "machine-json" {
		t.Errorf("Select(machine-json) = %+v, %v; want ok", p, ok)
	}
	// unknown select -> false
	if _, ok := r.Select("nope"); ok {
		t.Error("Select(unknown) should be false")
	}
	// List sorted
	if err := r.Register(OutputProfile{Name: "zeta"}); err != nil {
		t.Fatalf("Register zeta: %v", err)
	}
	if err := r.Register(OutputProfile{Name: "alpha"}); err != nil {
		t.Fatalf("Register alpha: %v", err)
	}
	got := r.List()
	want := []string{"alpha", "machine-json", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("List() = %v, want %v", got, want)
		}
	}
}

func TestBuiltinProfiles(t *testing.T) {
	builtins := []OutputProfile{MachineJSONProfile(), ActionFirstProfile(), HumanReadableProfile(), DebugProfile()}
	wantNames := []string{"machine-json", "action-first", "human-readable", "debug"}
	for i, p := range builtins {
		if p.Name != wantNames[i] {
			t.Errorf("builtin[%d].Name = %q, want %q", i, p.Name, wantNames[i])
		}
		if p.Name == "" || p.Style == "" || p.Format == "" || p.Language == "" {
			t.Errorf("builtin[%d] has empty fields: %+v", i, p)
		}
	}
	// Style/format expectations.
	if s, f := ActionFirstProfile().Style, ActionFirstProfile().Format; s != "action-first" || f != "text" {
		t.Errorf("action-first style/format = %s/%s", s, f)
	}
	if s, f := HumanReadableProfile().Style, HumanReadableProfile().Format; s != "plain" || f != "markdown" {
		t.Errorf("human-readable style/format = %s/%s", s, f)
	}
	if s, f := DebugProfile().Style, DebugProfile().Format; s != "debug" || f != "text" {
		t.Errorf("debug style/format = %s/%s", s, f)
	}
	if s, f := MachineJSONProfile().Style, MachineJSONProfile().Format; s != "json" || f != "json" {
		t.Errorf("machine-json style/format = %s/%s", s, f)
	}

	// Registry with builtins: 4 names.
	if got := NewRegistryWithBuiltins().List(); len(got) != 4 {
		t.Errorf("builtin registry List() = %v, want 4 names", got)
	}
}

func TestApplyProfileMachineJSON(t *testing.T) {
	content := "line with \"quotes\" and\nnewline"
	out := ApplyProfile(MachineJSONProfile(), content)
	var m struct {
		Profile string `json:"profile"`
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if m.Profile != "machine-json" || m.Format != "json" || m.Content != content {
		t.Errorf("decoded = %+v, want profile=machine-json format=json content=%q", m, content)
	}
}

func TestApplyProfileActionFirst(t *testing.T) {
	content := "some context line\n- create the widget\nnote about it\n> run the tests\nfinal line"
	out := ApplyProfile(ActionFirstProfile(), content)
	lines := strings.Split(out, "\n")
	if len(lines) != 6 {
		t.Fatalf("output = %d lines, want 6: %q", len(lines), out)
	}
	if lines[0] != "- create the widget" || lines[1] != "> run the tests" {
		t.Errorf("action lines should move to top in original order, got %q", lines[0:2])
	}
	if lines[2] != "---" {
		t.Errorf("separator missing, got %q", lines[2])
	}
	if lines[3] != "some context line" || lines[4] != "note about it" || lines[5] != "final line" {
		t.Errorf("non-action lines should follow in original order, got %q", lines[3:])
	}

	// No action lines -> content unchanged (byte-identical).
	plain := "no actions here\njust text"
	if got := ApplyProfile(ActionFirstProfile(), plain); got != plain {
		t.Errorf("no action lines should leave content unchanged, got %q", got)
	}

	// Word boundary: "creates" is not the verb "create".
	boundary := "creates a problem\nnote"
	if got := ApplyProfile(ActionFirstProfile(), boundary); got != boundary {
		t.Errorf("verb word boundary should keep line in place, got %q", got)
	}
}

func TestApplyProfileHumanReadable(t *testing.T) {
	content := "plain markdown content\nwith lines"
	if got := ApplyProfile(HumanReadableProfile(), content); got != content {
		t.Errorf("human-readable should be identity, got %q", got)
	}
}

func TestApplyProfileDebug(t *testing.T) {
	content := "hello world"
	out := ApplyProfile(DebugProfile(), content)
	prefix := "profile: debug style=debug format=text language=en tokens="
	if !strings.HasPrefix(out, prefix) {
		t.Errorf("debug prefix missing, got %q", out)
	}
	if !strings.HasSuffix(out, content) {
		t.Errorf("debug should preserve content after the prefix, got %q", out)
	}
}

func TestApplyProfileUnknownStyle(t *testing.T) {
	p := OutputProfile{Name: "bogus", Style: "bogus", Format: "text", Language: "en"}
	content := "anything"
	if got := ApplyProfile(p, content); got != content {
		t.Errorf("unknown style should be identity, got %q", got)
	}
}

func TestApplyProfileEmptyContent(t *testing.T) {
	if out := ApplyProfile(MachineJSONProfile(), ""); out == "" {
		t.Error("machine-json should wrap empty content in a JSON envelope")
	}
	if out := ApplyProfile(ActionFirstProfile(), ""); out != "" {
		t.Errorf("action-first empty content should stay empty, got %q", out)
	}
	if out := ApplyProfile(DebugProfile(), ""); !strings.HasPrefix(out, "profile: debug") {
		t.Errorf("debug empty content should still carry the prefix, got %q", out)
	}
	// No panics for any built-in on empty content.
	_ = ApplyProfile(HumanReadableProfile(), "")
	_ = ApplyProfile(OutputProfile{Style: "bogus"}, "")
}

func writeProfileFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFileValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	writeProfileFile(t, path, `[{"name":"terse","style":"plain","format":"text","language":"en"}]`)
	ps, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(ps) != 1 || ps[0].Name != "terse" || ps[0].Style != "plain" {
		t.Fatalf("LoadFile = %+v, want one terse/plain profile", ps)
	}
}

func TestLoadFileInvalidEntries(t *testing.T) {
	dir := t.TempDir()
	t.Run("empty name", func(t *testing.T) {
		path := filepath.Join(dir, "noname.json")
		writeProfileFile(t, path, `[{"name":"","style":"plain"}]`)
		_, err := LoadFile(path)
		if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "entry 1") || !strings.Contains(err.Error(), "empty name") {
			t.Errorf("LoadFile error = %v, want file+index+empty name", err)
		}
	})
	t.Run("empty style", func(t *testing.T) {
		path := filepath.Join(dir, "nostyle.json")
		writeProfileFile(t, path, `[{"name":"ok","style":"plain"},{"name":"broken","style":""}]`)
		_, err := LoadFile(path)
		if err == nil || !strings.Contains(err.Error(), "entry 2") || !strings.Contains(err.Error(), "empty style") {
			t.Errorf("LoadFile error = %v, want entry 2 + empty style", err)
		}
	})
	t.Run("malformed json", func(t *testing.T) {
		path := filepath.Join(dir, "bad.json")
		writeProfileFile(t, path, `{not json`)
		if _, err := LoadFile(path); err == nil {
			t.Error("malformed JSON should error")
		}
	})
}

func TestLoadFileMissingFile(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("missing file should error")
	}
}

func TestNewRegistryWithUserProfilesNoFile(t *testing.T) {
	r := NewRegistryWithUserProfiles(t.TempDir())
	want := []string{"action-first", "debug", "human-readable", "machine-json"}
	got := r.List()
	if len(got) != len(want) {
		t.Fatalf("List() = %v, want builtins %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("List() = %v, want %v", got, want)
		}
	}
}

func TestNewRegistryWithUserProfilesAdds(t *testing.T) {
	root := t.TempDir()
	writeProfileFile(t, filepath.Join(root, ".kern", "profiles.json"), `[{"name":"terse","style":"plain","format":"text","language":"en"}]`)
	r := NewRegistryWithUserProfiles(root)
	p, ok := r.Select("terse")
	if !ok || p.Name != "terse" || p.Style != "plain" {
		t.Errorf("Select(terse) = %+v, %v; want user profile", p, ok)
	}
	// Built-ins still present.
	for _, name := range []string{"machine-json", "action-first", "human-readable", "debug"} {
		if _, ok := r.Select(name); !ok {
			t.Errorf("built-in %q missing after adding user profile", name)
		}
	}
}

func TestNewRegistryWithUserProfilesOverridesBuiltin(t *testing.T) {
	root := t.TempDir()
	writeProfileFile(t, filepath.Join(root, ".kern", "profiles.json"), `[{"name":"debug","style":"plain","format":"text","language":"en"}]`)
	r := NewRegistryWithUserProfiles(root)
	p, ok := r.Select("debug")
	if !ok {
		t.Fatal("debug should still resolve")
	}
	if p.Style != "plain" {
		t.Errorf("user override of debug lost: Style = %q, want plain", p.Style)
	}
}
