package guardpause

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
		err  bool
	}{
		{"", DefaultDuration, false},
		{"30m", 30 * time.Minute, false},
		{"2h", MaxDuration, false},
		{"nope", 0, true},
		{"0s", 0, true},
		{"-1m", 0, true},
		{"2h1s", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseDuration(tt.in)
		if tt.err {
			if err == nil {
				t.Fatalf("ParseDuration(%q) = %v, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseDuration(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseDuration(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestCanonicalRoot(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err == nil {
		got, err := CanonicalRoot(filepath.Join(link, "..", "link"))
		if err != nil {
			t.Fatal(err)
		}
		want, err := filepath.EvalSymlinks(real)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("CanonicalRoot symlink = %q, want %q", got, want)
		}
	}
	got, err := CanonicalRoot(filepath.Join("a", "..", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) || filepath.Clean(got) != got {
		t.Fatalf("CanonicalRoot = %q, want absolute clean path", got)
	}
}

func setupPauseEnv(t *testing.T) (root, registry, audit string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	root = t.TempDir()
	registry = filepath.Join(home, ".kern", "guard", "pauses.jsonl")
	audit = filepath.Join(home, ".kern", "audit", "bypass.jsonl")
	for _, dir := range []string{filepath.Dir(registry), filepath.Dir(audit)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, registry, audit
}

func TestCreateRecordActiveResumeAndAudit(t *testing.T) {
	root, registry, audit := setupPauseEnv(t)
	if err := os.WriteFile(registry, []byte("foreign-record\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p, err := CreateRecord(root, "", "  focus on the failing test  ", now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Scope != "repo" || p.Reason != "focus on the failing test" {
		t.Fatalf("pause = %+v", p)
	}
	active, ok, err := Active(root)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || active.Root != p.Root || active.Reason != p.Reason {
		t.Fatalf("Active = %+v, %v", active, ok)
	}
	if _, err := CreateRecord(root, "1m", "duplicate", now.Add(time.Minute)); err == nil {
		t.Fatal("duplicate active pause was accepted")
	}
	removed, err := Resume(root)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("Resume reported no active pause")
	}
	if _, ok, err := Active(root); err != nil || ok {
		t.Fatalf("Active after Resume = %v, %v", ok, err)
	}
	raw, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "foreign-record") {
		t.Fatalf("foreign registry line was dropped: %q", raw)
	}
	auditRaw, err := os.ReadFile(audit)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{
		"kern-guard-pause-request",
		"kern-guard-pause-active",
		"kern-guard-resume-request",
		"kern-guard-resume-complete",
	} {
		if !strings.Contains(string(auditRaw), event) {
			t.Fatalf("audit missing %s: %q", event, auditRaw)
		}
	}
}

func TestActiveIgnoresExpiredPause(t *testing.T) {
	root, registry, _ := setupPauseEnv(t)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	p := Pause{
		Schema:   Schema,
		Scope:    "repo",
		Root:     root,
		Reason:   "expired",
		Expires:  now.Add(-time.Minute).Format(time.RFC3339),
		Recorded: now.Add(-time.Hour).Format(time.RFC3339),
	}
	j, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registry, append(j, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Active(root); err != nil || ok {
		t.Fatalf("Active expired = %v, %v", ok, err)
	}
}

func TestCreateRecordValidationAndAuditFailure(t *testing.T) {
	root, _, audit := setupPauseEnv(t)
	if _, err := CreateRecord(root, "30m", "   ", time.Now().UTC()); err == nil {
		t.Fatal("blank reason accepted")
	}
	if _, err := CreateRecord(root, "3h", "too long", time.Now().UTC()); err == nil {
		t.Fatal("over-max duration accepted")
	}
	if err := os.MkdirAll(audit, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateRecord(root, "30m", "audit failure", time.Now().UTC()); err == nil {
		t.Fatal("audit failure accepted")
	}
	if _, ok, err := Active(root); err != nil || ok {
		t.Fatalf("pause active after audit failure: %v, %v", ok, err)
	}
}

func TestBashSteeringScope(t *testing.T) {
	root, _, _ := setupPauseEnv(t)
	now := time.Now().UTC()
	if _, err := CreateRecordScoped(root, "30m", "serve misbehaving", "nonsense", now); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if _, err := CreateRecordScoped(root, "30m", "serve misbehaving", ScopeBashSteering, now); err != nil {
		t.Fatal(err)
	}
	// Coexistence: a repo pause and a bash-steering pause are independent.
	if _, err := CreateRecordScoped(root, "30m", "read steering off", ScopeRepo, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ActiveScoped(root, ScopeBashSteering); !ok {
		t.Fatal("bash-steering pause not active")
	}
	if _, ok, _ := ActiveScoped(root, ScopeRepo); !ok {
		t.Fatal("repo pause not active")
	}
	// Duplicate same-scope pause is refused.
	if _, err := CreateRecordScoped(root, "30m", "dup", ScopeBashSteering, now.Add(2*time.Second)); err == nil {
		t.Fatal("duplicate bash-steering pause accepted")
	}
	// Resume removes every scope for the root.
	removed, err := Resume(root)
	if err != nil || !removed {
		t.Fatalf("Resume = %v, %v", removed, err)
	}
	if _, ok, _ := ActiveScoped(root, ScopeBashSteering); ok {
		t.Fatal("bash-steering pause survived resume")
	}
	if _, ok, _ := ActiveScoped(root, ScopeRepo); ok {
		t.Fatal("repo pause survived resume")
	}
}
