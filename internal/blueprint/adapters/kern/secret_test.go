package kern

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

func TestSecretCheckClean(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"findings":[]}`, "", 0, nil)}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "main.go"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Name != "secret:scan" {
		t.Errorf("Name = %q, want secret:scan", cr.Name)
	}
	if cr.Status != domain.StatusPass {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusPass)
	}
	if len(cr.Findings) != 0 {
		t.Errorf("Findings = %d, want 0", len(cr.Findings))
	}
}

func TestSecretCheckErrorFinding(t *testing.T) {
	const out = `{"schema_version":2,"findings":[{"file": "main.go", "line": 9, "rule": "hardcoded-secret", "severity": "error", "message": "hardcoded API key detected", "snippet": "sk_live_ABC123"}]}`
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(out, "", 1, nil)}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "main.go"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusBlock)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}

	f := cr.Findings[0]
	if f.RuleID != "secret:hardcoded-secret" {
		t.Errorf("RuleID = %q, want secret:hardcoded-secret", f.RuleID)
	}
	if f.Severity != domain.SeverityBlock {
		t.Errorf("Severity = %q, want %q", f.Severity, domain.SeverityBlock)
	}
	if f.Category != domain.CategorySecret {
		t.Errorf("Category = %q, want %q", f.Category, domain.CategorySecret)
	}
	if f.File != "main.go" {
		t.Errorf("File = %q, want main.go", f.File)
	}
	if f.Line != 9 {
		t.Errorf("Line = %d, want 9", f.Line)
	}
	if f.Message != "hardcoded API key detected" {
		t.Errorf("Message = %q, want hardcoded API key detected", f.Message)
	}
	if f.Explanation == "" {
		t.Error("Explanation is empty")
	}
	if !f.Redacted {
		t.Error("Redacted = false, want true")
	}
	// The raw snippet must never propagate into Blueprint results
	// (spec Rule: "Do not send secrets to agents").
	if strings.Contains(f.Explanation, "sk_live") || strings.Contains(f.Explanation, "ABC123") {
		t.Error("snippet leaked into Explanation")
	}
	if len(f.Evidence) != 1 {
		t.Fatalf("Evidence = %d, want 1", len(f.Evidence))
	}
	if f.Evidence[0].Kind != "pattern-match" {
		t.Errorf("Evidence.Kind = %q, want pattern-match", f.Evidence[0].Kind)
	}
	if f.Evidence[0].Location != "main.go:9" {
		t.Errorf("Evidence.Location = %q, want main.go:9", f.Evidence[0].Location)
	}
}

func TestSecretCheckWarningSeverity(t *testing.T) {
	const out = `{"schema_version":2,"findings":[{"file": "config.go", "line": 3, "rule": "aws-access-key", "severity": "warning", "message": "possible AWS key", "snippet": "AKIA..."}]}`
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(out, "", 1, nil)}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "config.go"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}
	if cr.Findings[0].Severity != domain.SeverityWarn {
		t.Errorf("Findings[0].Severity = %q, want %q", cr.Findings[0].Severity, domain.SeverityWarn)
	}
	if cr.Status != domain.StatusWarn {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusWarn)
	}
}

func TestSecretCheckScansDirectoryOnce(t *testing.T) {
	var gotArgs [][]string
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			gotArgs = append(gotArgs, append([]string(nil), args...))
			return `{"schema_version":2,"findings":[]}`, "", 0, nil
		},
	}
	chk := NewSecretCheck(client)
	if _, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}},
	}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	// Phase 3: SecretCheck scans the repo directory ONCE (kern sec does not
	// support scanning individual files). It then filters findings to changed
	// files in the ChangeRequest.
	if len(gotArgs) != 1 {
		t.Fatalf("SecScan calls = %d, want 1 (directory scan, not per-file)", len(gotArgs))
	}
	args := gotArgs[0]
	if len(args) != 3 || args[0] != "sec" || args[1] != "--json" || args[2] != "." {
		t.Errorf("call args = %v, want [sec --json .]", args)
	}
}

func TestSecretCheckToolError(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner("", "kern sec: failed to read file", 1, nil)}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "main.go"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if !strings.Contains(cr.Error, "kern sec") {
		t.Errorf("Error = %q, want mention of kern sec", cr.Error)
	}
}

func TestSecretCheckLaunchFailure(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner("", "", -1, errors.New("executable not found"))}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "main.go"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
}

func TestSecretCheckRequiresRepositoryRoot(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"findings":[]}`, "", 0, nil)}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		Files: []domain.FileChange{{Path: "main.go"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if cr.Status != domain.StatusError {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusError)
	}
	if cr.Error != "repository root required" {
		t.Errorf("Error = %q, want repository root required", cr.Error)
	}
}

// secretCacheFixture builds a repo root containing `main.go` (which exists on
// disk so the cache's stat validation can match it) plus a counting runner
// that returns the given canned findings for every `kern sec` call.
func secretCacheFixture(t *testing.T, canned string) (*KernClient, *int, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nconst k = \"x\"\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	calls := 0
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			calls++
			return canned, "", 1, nil
		},
	}
	return client, &calls, root
}

func cacheSecretReq(root string) domain.ChangeRequest {
	return domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "main.go", Op: domain.OpWrite}},
	}
}

// TestSecretCheckCacheHit: the second Run over the SAME staged file state must
// replay cached findings — the runner is not called again and findings are
// identical.
func TestSecretCheckCacheHit(t *testing.T) {
	const canned = `{"schema_version":2,"findings":[{"file": "main.go", "line": 2, "rule": "hardcoded-secret", "severity": "error", "message": "hardcoded API key detected", "snippet": "sk_live_ABC"}]}`
	client, calls, root := secretCacheFixture(t, canned)
	chk := NewSecretCheck(client)
	req := cacheSecretReq(root)

	cr1, err := chk.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("runner calls after first Run = %d, want 1", *calls)
	}

	cr2, err := chk.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("runner calls after second Run = %d, want 1 (cache replay)", *calls)
	}
	if cr1.Status != cr2.Status || len(cr1.Findings) != len(cr2.Findings) {
		t.Fatalf("findings differ between runs: %+v vs %+v", cr1.Findings, cr2.Findings)
	}
	for i := range cr1.Findings {
		if cr1.Findings[i].File != cr2.Findings[i].File || cr1.Findings[i].Line != cr2.Findings[i].Line {
			t.Errorf("finding %d differs: %+v vs %+v", i, cr1.Findings[i], cr2.Findings[i])
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".blueprint", "sec-cache.json")); err != nil {
		t.Errorf("sec-cache.json not written: %v", err)
	}
}

// TestSecretCheckCacheHitWithCleanFile: negative caching — a staged file with
// NO findings still gets a cache entry, so a repeat run over an unchanged
// mixed set (one clean + one dirty file) replays from cache and never invokes
// `kern sec` again.
func TestSecretCheckCacheHitWithCleanFile(t *testing.T) {
	const canned = `{"schema_version":2,"findings":[
		{"file": "main.go", "line": 2, "rule": "hardcoded-secret", "severity": "error", "message": "hardcoded API key detected", "snippet": "sk_live_ABC"}
	]}`
	root := t.TempDir()
	// One clean file (no secret) and one dirty file (with a secret).
	if err := os.WriteFile(filepath.Join(root, "clean.go"), []byte("package main\nconst c = 1\n"), 0o644); err != nil {
		t.Fatalf("write clean.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nconst k = \"x\"\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	calls := 0
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			calls++
			return canned, "", 1, nil
		},
	}
	chk := NewSecretCheck(client)
	req := domain.ChangeRequest{
		RepositoryRoot: root,
		Files:          []domain.FileChange{{Path: "clean.go", Op: domain.OpWrite}, {Path: "main.go", Op: domain.OpWrite}},
	}

	if _, err := chk.Run(context.Background(), req); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if calls != 1 {
		t.Fatalf("runner calls after first Run = %d, want 1", calls)
	}

	cr2, err := chk.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if calls != 1 {
		t.Fatalf("runner calls after second Run = %d, want 1 (clean file cached: no rescan)", calls)
	}
	if cr2.Status != domain.StatusBlock {
		t.Errorf("Status = %q, want %q (dirty file finding replayed from cache)", cr2.Status, domain.StatusBlock)
	}
	if len(cr2.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1 (only the dirty file)", len(cr2.Findings))
	}

	// Negative caching: the clean file must have its own cache entry with
	// zero findings.
	cache := loadSecCache(secCachePath(root))
	cleanEntry, ok := cache.Files["clean.go"]
	if !ok {
		t.Errorf("cache missing entry for clean file clean.go (negative caching)")
	}
	if len(cleanEntry.Findings) != 0 {
		t.Errorf("clean.go cached findings = %d, want 0", len(cleanEntry.Findings))
	}
	if _, ok := cache.Files["main.go"]; !ok {
		t.Errorf("cache missing entry for dirty file main.go")
	}
}

// TestSecretCheckCacheMissOnChange: modifying the staged file (new size/mtime)
// invalidates the cache entry, so the runner is called again and the cache is
// refreshed.
func TestSecretCheckCacheMissOnChange(t *testing.T) {
	const canned = `{"schema_version":2,"findings":[{"file": "main.go", "line": 2, "rule": "hardcoded-secret", "severity": "error", "message": "hardcoded API key detected", "snippet": "sk_live_ABC"}]}`
	client, calls, root := secretCacheFixture(t, canned)
	chk := NewSecretCheck(client)
	req := cacheSecretReq(root)

	if _, err := chk.Run(context.Background(), req); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("runner calls after first Run = %d, want 1", *calls)
	}

	// Change the staged file: longer content => different size AND mtime.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nconst k = \"a much longer secret value\"\n"), 0o644); err != nil {
		t.Fatalf("rewrite main.go: %v", err)
	}
	if _, err := chk.Run(context.Background(), req); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("runner calls after modified file = %d, want 2 (cache miss)", *calls)
	}

	// Third run on the now-cached state is a hit again.
	if _, err := chk.Run(context.Background(), req); err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("runner calls after third Run = %d, want 2 (refreshed cache hit)", *calls)
	}
}

// TestSecretCheckEmptyFilesSkipsScan: an empty staged set returns PASS without
// invoking the runner at all.
func TestSecretCheckEmptyFilesSkipsScan(t *testing.T) {
	calls := 0
	client := &KernClient{
		binaryPath: "kern",
		runner: func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
			calls++
			return `{"schema_version":2,"findings":[]}`, "", 0, nil
		},
	}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusPass {
		t.Errorf("Status = %q, want %q", cr.Status, domain.StatusPass)
	}
	if calls != 0 {
		t.Errorf("runner calls = %d, want 0 for empty staged set", calls)
	}
}

// TestSecretCheckDedupeSameLine: kern emits multiple rules for one line (e.g.
// STRIPE + TOKEN on the same line); dedupe keeps a single finding per
// (file, line).
func TestSecretCheckDedupeSameLine(t *testing.T) {
	const canned = `{"schema_version":2,"findings":[
		{"file": "main.go", "line": 3, "rule": "stripe-key", "severity": "error", "message": "hardcoded secret: STRIPE", "snippet": "sk_..."},
		{"file": "main.go", "line": 3, "rule": "token", "severity": "error", "message": "hardcoded secret: TOKEN", "snippet": "tok_..."},
		{"file": "main.go", "line": 7, "rule": "aws-access-key", "severity": "warning", "message": "possible AWS key", "snippet": "AKIA..."}
	]}`
	client, _, root := secretCacheFixture(t, canned)
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), cacheSecretReq(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cr.Findings) != 2 {
		t.Fatalf("findings = %d, want 2 (line 3 deduped to one, line 7 kept); got %v", len(cr.Findings), fmt.Sprintf("%+v", cr.Findings))
	}
	if cr.Findings[0].Line != 3 {
		t.Errorf("first finding line = %d, want 3 (first occurrence kept)", cr.Findings[0].Line)
	}
	if cr.Findings[1].Line != 7 {
		t.Errorf("second finding line = %d, want 7", cr.Findings[1].Line)
	}
}

// TestSecretCheckCorruptCacheFallsBackToScan: a corrupt cache file must never
// fail validation — it is treated as empty and a fresh scan runs.
func TestSecretCheckCorruptCacheFallsBackToScan(t *testing.T) {
	const canned = `{"schema_version":2,"findings":[{"file": "main.go", "line": 2, "rule": "hardcoded-secret", "severity": "error", "message": "hardcoded API key detected", "snippet": "sk_live_ABC"}]}`
	client, calls, root := secretCacheFixture(t, canned)
	dir := filepath.Join(root, ".blueprint")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sec-cache.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), cacheSecretReq(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cr.Status != domain.StatusBlock {
		t.Errorf("Status = %q, want %q (scan must still run on corrupt cache)", cr.Status, domain.StatusBlock)
	}
	if *calls != 1 {
		t.Errorf("runner calls = %d, want 1", *calls)
	}
}

// --- P2-4 (G25): Kern 2.0 Evidence provenance stamping ---

// TestSecretCheckRuleVersionConfidence verifies secret findings carry
// rule_version "1", confidence 0.95, and scope "file".
func TestSecretCheckRuleVersionConfidence(t *testing.T) {
	const out = `{"schema_version":2,"findings":[{"file": "main.go", "line": 9, "rule": "hardcoded-secret", "severity": "error", "message": "hardcoded API key detected", "snippet": "sk_live_ABC123"}]}`
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(out, "", 1, nil)}
	chk := NewSecretCheck(client)
	cr, err := chk.Run(context.Background(), domain.ChangeRequest{
		RepositoryRoot: repoRoot(t),
		Files:          []domain.FileChange{{Path: "main.go"}},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(cr.Findings) != 1 {
		t.Fatalf("Findings = %d, want 1", len(cr.Findings))
	}
	f := cr.Findings[0]
	if f.RuleVersion != "1" {
		t.Errorf("RuleVersion = %q, want \"1\"", f.RuleVersion)
	}
	if f.Confidence != 0.95 {
		t.Errorf("Confidence = %v, want 0.95", f.Confidence)
	}
	if f.Scope != "file" {
		t.Errorf("Scope = %q, want \"file\"", f.Scope)
	}
}
