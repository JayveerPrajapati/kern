package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/sandbox/landlock"
)

// fixtureHome builds a $HOME-shaped tree containing the sensitive entries
// plus ordinary entries a build legitimately reads.
func fixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, d := range []string{".ssh", ".aws", ".gnupg", ".kube", ".config", ".config/gcloud", ".docker", "go"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		".ssh/id_rsa":                     "secret-key",
		".netrc":                          "machine x login y password z",
		".gitconfig":                      "[user]\n\tname = T\n",
		"go/file.txt":                     "ok",
		".config/gcloud/credentials.json": "gcp-secret",
		".docker/config.json":             "{}",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(home, rel), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func ruleFor(rules []landlock.AllowRule, path string) (landlock.AllowRule, bool) {
	for _, r := range rules {
		if r.Path == path {
			return r, true
		}
	}
	return landlock.AllowRule{}, false
}

func TestLandlockAllowPathsExcludesSensitiveHome(t *testing.T) {
	home := fixtureHome(t)
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	rules, err := landlock.LandlockAllowPaths("/ws", "true", env, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".aws"),
		filepath.Join(home, ".gnupg"),
		filepath.Join(home, ".kube"),
		filepath.Join(home, ".netrc"),
		filepath.Join(home, ".config", "gcloud"),
		filepath.Join(home, ".docker", "config.json"),
	} {
		if _, ok := ruleFor(rules, sensitive); ok {
			t.Errorf("sensitive path %q must NOT be granted", sensitive)
		}
	}
	// Ordinary home entries ARE granted.
	for _, want := range []string{
		filepath.Join(home, ".gitconfig"),
		filepath.Join(home, "go"),
		filepath.Join(home, ".config"),
		filepath.Join(home, ".docker"),
	} {
		if _, ok := ruleFor(rules, want); !ok {
			t.Errorf("expected grant for %q", want)
		}
	}
}

func TestLandlockAllowPathsWriteTier(t *testing.T) {
	home := fixtureHome(t)
	env := []string{
		"HOME=" + home,
		"PATH=/usr/bin:/bin",
		"GOCACHE=" + filepath.Join(home, ".cache", "go-build"),
		"GOMODCACHE=" + filepath.Join(home, "go", "pkg", "mod"),
	}
	rules, err := landlock.LandlockAllowPaths("/ws", "true", env, true)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := ruleFor(rules, "/ws"); !ok || r.Access != landlock.FsAccessWrite {
		t.Errorf("workspace root must get the full write mask, got %+v (ok=%v)", r, ok)
	}
	if r, ok := ruleFor(rules, filepath.Join(home, ".cache", "go-build")); !ok || r.Access != landlock.FsAccessWrite {
		t.Errorf("GOCACHE must get the write mask, got %+v (ok=%v)", r, ok)
	}
	if r, ok := ruleFor(rules, filepath.Join(home, "go", "pkg", "mod")); !ok || r.Access != landlock.FsAccessWrite {
		t.Errorf("GOMODCACHE must get the write mask, got %+v (ok=%v)", r, ok)
	}
	// Read-only mode: no write grants anywhere.
	rules, err = landlock.LandlockAllowPaths("/ws", "true", env, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.Access&landlock.FsAccessWriteFile != 0 {
			t.Errorf("read-only mode must not grant write access: %+v", r)
		}
	}
}

func TestLandlockAllowPathsTargetBinaryDir(t *testing.T) {
	// A custom toolchain dir on PATH is granted for its binaries.
	toolDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(toolDir, "mytool"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules, err := landlock.LandlockAllowPaths("/ws", "mytool", []string{"HOME=/tmp", "PATH=" + toolDir + ":/usr/bin"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ruleFor(rules, toolDir); !ok {
		t.Errorf("target binary dir %s must be granted (custom toolchain PATH)", toolDir)
	}
	// An unresolvable target yields no error and no grant for its dir.
	rules, err = landlock.LandlockAllowPaths("/ws", "definitely-not-a-real-binary-xyz", []string{"HOME=/tmp", "PATH=/usr/bin"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) == 0 {
		t.Fatal("expected system-path rules even with an unresolvable target")
	}
}

func TestLandlockAllowPathsMissingEnvHomeFallsBack(t *testing.T) {
	// No HOME in env: the builder falls back to the process home and still
	// produces rules without erroring.
	rules, err := landlock.LandlockAllowPaths("/ws", "true", []string{"PATH=/usr/bin:/bin"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) == 0 {
		t.Fatal("expected non-empty rules")
	}
}

func TestStripEnvVarRemovesMarker(t *testing.T) {
	env := []string{"A=1", landlock.ChildSpecEnv + `={"root":"/"}`, "B=2"}
	out := landlock.StripEnvVar(env, landlock.ChildSpecEnv)
	if len(out) != 2 || out[0] != "A=1" || out[1] != "B=2" {
		t.Fatalf("unexpected result: %v", out)
	}
	// No mutation of the input slice's logical content beyond the marker.
	if len(env) != 3 {
		t.Fatalf("input mutated: %v", env)
	}
}

func TestChildSpecJSONRoundTrip(t *testing.T) {
	specJSON, err := landlock.ChildSpecJSON("/ws", "go", []string{"test"}, []string{"HOME=/h"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"root":"/ws","cmd":"go","args":["test"],"env":["HOME=/h"]}`
	if specJSON != want {
		t.Fatalf("unexpected spec JSON: %s", specJSON)
	}
}
