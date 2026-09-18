// Package cli holds the Blueprint gate suite migrated from the legacy
// cmd/blueprint shim.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// g17RunDoctor runs blueprint's RunDoctor in-process — the kern binary's
// `doctor` subcommand is kern's own preflight, not the blueprint doctor — with
// stdout/stderr captured and returns (combined output, exit code).
func g17RunDoctor(t *testing.T, dir string, extraEnv ...string) (string, int) {
	t.Helper()
	for _, kv := range extraEnv {
		key, val, _ := strings.Cut(kv, "=")
		t.Setenv(key, val)
	}
	code := 0
	stdout, stderr := captureRun(t, func() {
		code = RunDoctor([]string{"--repo", dir})
	})
	return stdout + stderr, code
}

// g17HealthyRepo builds a git repo with valid boundaries, valid config, and
// one committed file.
func g17HealthyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, ".blueprint/config.yaml", "version: 1\nmode: enforce\n")
	g4WriteFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")
	return dir
}

// G17-1: healthy environment → exit 0 with "verdict: 0".
func TestG17_Healthy(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	kernPath := requireKernPath(t)
	dir := g17HealthyRepo(t)

	out, code := g17RunDoctor(t, dir, "KERN_BINARY="+kernPath)
	if code != 0 {
		t.Fatalf("exit=%d want 0 (healthy); output:\n%s", code, out)
	}
	if !strings.Contains(out, "verdict: 0") {
		t.Fatalf("output missing 'verdict: 0':\n%s", out)
	}
}

// G17-2: KERN_BINARY pointing at nothing → exit 2 (env error). NewKernClient
// errors on a bad KERN_BINARY (resolveKernBinary returns an error without
// falling back to PATH), so this never depends on the real kern binary.
func TestG17_MissingKern(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	dir := g17HealthyRepo(t)

	out, code := g17RunDoctor(t, dir, "KERN_BINARY=/nonexistent/kern/binary")
	if code != 2 {
		t.Fatalf("exit=%d want 2 (env error); output:\n%s", code, out)
	}
	// The degraded-mode warn must be reported (informational — it does not
	// change the verdict, which stays 2 from the kern-binary env error). The
	// terminal renderer prints the check name with "-" as a space, so assert
	// on the space form.
	if !strings.Contains(out, "degraded mode") {
		t.Fatalf("output missing 'degraded mode' warn check:\n%s", out)
	}
}

// G17-3: broken .blueprint/config.yaml → exit 3 (config error), even though
// the environment (kern) is healthy.
func TestG17_InvalidConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	kernPath := requireKernPath(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, ".blueprint/config.yaml", "version: 1\npolicies:\n  architecure: block\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	out, code := g17RunDoctor(t, dir, "KERN_BINARY="+kernPath)
	if code != 3 {
		t.Fatalf("exit=%d want 3 (config error); output:\n%s", code, out)
	}
}

// G17-4: malformed .kern/boundaries.json → exit 3 (config error).
func TestG17_InvalidBoundaries(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	kernPath := requireKernPath(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, ".kern/boundaries.json", "{not json")
	g4WriteFile(t, dir, ".blueprint/config.yaml", "version: 1\nmode: enforce\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	out, code := g17RunDoctor(t, dir, "KERN_BINARY="+kernPath)
	if code != 3 {
		t.Fatalf("exit=%d want 3 (config error); output:\n%s", code, out)
	}
}

// G17-5: no .kern dir → exit 0 with a boundaries warning (not an error).
func TestG17_NoBoundariesWarns(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	kernPath := requireKernPath(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteFile(t, dir, ".blueprint/config.yaml", "version: 1\nmode: enforce\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	out, code := g17RunDoctor(t, dir, "KERN_BINARY="+kernPath)
	if code != 0 {
		t.Fatalf("exit=%d want 0 (warning only); output:\n%s", code, out)
	}
	if !strings.Contains(out, "warn") {
		t.Fatalf("output missing 'warn':\n%s", out)
	}
}

// G17-6: --json output shape — verdict 0 and all six expected check names.
func TestG17_JSONShape(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	t.Setenv("KERN_BINARY", requireKernPath(t))
	dir := g17HealthyRepo(t)

	code := 0
	stdout, _ := captureRun(t, func() {
		code = RunDoctor([]string{"--json", "--repo", dir})
	})
	if code != 0 {
		t.Fatalf("exit=%d want 0; output:\n%s", code, stdout)
	}
	out := []byte(stdout)

	var payload struct {
		Verdict int `json:"verdict"`
		Checks  []struct {
			Name     string `json:"name"`
			Status   string `json:"status"`
			Category string `json:"category"`
			Detail   string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, out)
	}
	if payload.Verdict != 0 {
		t.Fatalf("verdict=%d want 0", payload.Verdict)
	}
	if len(payload.Checks) < 6 {
		t.Fatalf("checks=%d want >= 6; output:\n%s", len(payload.Checks), out)
	}
	wantNames := []string{"kern-binary", "kern-contract", "boundaries", "config", "git", "hook"}
	for _, name := range wantNames {
		found := false
		for _, c := range payload.Checks {
			if c.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing check %q; got %+v", name, payload.Checks)
		}
	}
	for _, c := range payload.Checks {
		if c.Status != "ok" && c.Status != "warn" && c.Status != "error" && c.Status != "info" {
			t.Fatalf("check %q has invalid status %q", c.Name, c.Status)
		}
		if c.Category != "env" && c.Category != "config" && c.Category != "info" {
			t.Fatalf("check %q has invalid category %q", c.Name, c.Category)
		}
	}
}

// --- P1.4 audit-chain doctor checks ---

// writeAuditRecords appends n PASS records to the repo's audit chain using
// the audit package directly (the local chain must not depend on a kern
// binary, so resolution is forced off for the test process).
func writeAuditRecords(t *testing.T, dir string, n int) {
	t.Helper()
	t.Setenv("KERN_BINARY", filepath.Join(t.TempDir(), "no-such-kern"))
	w := audit.NewWriter(filepath.Join(dir, ".blueprint", "audit", "audit.jsonl"))
	for i := 0; i < n; i++ {
		if err := w.Write(audit.Record{
			CorrelationID: fmt.Sprintf("doctor-%d", i),
			Timestamp:     time.Now().UTC(),
			Source:        domain.SourceHuman,
			Operation:     domain.OpCommit,
			RepoRoot:      dir,
			Status:        domain.StatusPass,
			ExitCode:      0,
		}); err != nil {
			t.Fatalf("write audit record %d: %v", i, err)
		}
	}
}

// TestDoctor_AuditChainIntact (P1.4): a repo with a valid audit chain reports
// the audit-chain check as OK and keeps the doctor verdict at 0.
func TestDoctor_AuditChainIntact(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	kernPath := requireKernPath(t)
	dir := g17HealthyRepo(t)
	writeAuditRecords(t, dir, 2)

	out, code := g17RunDoctor(t, dir, "KERN_BINARY="+kernPath)
	if code != 0 {
		t.Fatalf("exit=%d want 0 (intact chain); output:\n%s", code, out)
	}
	if !strings.Contains(out, "audit chain intact (2 records") {
		t.Fatalf("output missing intact audit-chain check:\n%s", out)
	}
}

// TestDoctor_AuditChainBroken (P1.4): tampering with a record on disk breaks
// VerifyChain; the audit-chain check reports BROKEN and flips the verdict to
// 3 (config-class error). This holds regardless of kern availability (a
// config error outranks any env error).
func TestDoctor_AuditChainBroken(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	dir := g17HealthyRepo(t)
	writeAuditRecords(t, dir, 2)

	// Tamper with the first record's status on disk.
	auditPath := filepath.Join(dir, ".blueprint", "audit", "audit.jsonl")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit chain: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("parse first audit record: %v", err)
	}
	rec["status"] = "BLOCK" // stored hash no longer recomputes
	b, _ := json.Marshal(rec)
	lines[0] = string(b)
	if err := os.WriteFile(auditPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write tampered audit chain: %v", err)
	}

	out, code := g17RunDoctor(t, dir)
	if code != 3 {
		t.Fatalf("exit=%d want 3 (broken audit chain is a config-class error); output:\n%s", code, out)
	}
	if !strings.Contains(out, "audit chain BROKEN") {
		t.Fatalf("output missing broken audit-chain check:\n%s", out)
	}
}

// TestDoctor_KernGatesVisibility (M3): doctor reports which phase-gate
// registry entries are kern-dependent and whether they can run. When kern is
// missing the check warns with the exact skip set (so an environment cannot
// silently ship with a shrunken gate set); when kern is present it reports ok.
func TestDoctor_KernGatesVisibility(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E gate test — full pipeline; runs in nightly non-short suite")
	}
	t.Run("missing kern warns with gate list", func(t *testing.T) {
		dir := g17HealthyRepo(t)

		out, code := g17RunDoctor(t, dir, "KERN_BINARY=/nonexistent/kern/binary")
		if code != 2 {
			t.Fatalf("exit=%d want 2 (kern-binary env error); output:\n%s", code, out)
		}
		if !strings.Contains(out, "7 gates require kern and will skip") {
			t.Fatalf("output missing kern-gates warn marker:\n%s", out)
		}
		for _, g := range KernDependentGates() {
			if !strings.Contains(out, g) {
				t.Fatalf("output missing gate %s in the kern skip list:\n%s", g, out)
			}
		}
	})
	t.Run("kern present reports ok", func(t *testing.T) {
		kernPath := requireKernPath(t)
		dir := g17HealthyRepo(t)

		out, code := g17RunDoctor(t, dir, "KERN_BINARY="+kernPath)
		if code != 0 {
			t.Fatalf("exit=%d want 0 (healthy); output:\n%s", code, out)
		}
		if !strings.Contains(out, "kern-dependent gates can run") {
			t.Fatalf("output missing kern-gates ok marker:\n%s", out)
		}
		if !strings.Contains(out, "G27") {
			t.Fatalf("output missing gate ids in kern-gates ok detail:\n%s", out)
		}
	})
}
