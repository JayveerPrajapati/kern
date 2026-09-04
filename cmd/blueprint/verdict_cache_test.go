package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Verdict cache (keyless CI replay) tests ---
//
// These tests exercise the verdict cache in `blueprint ci`: a byte-identical
// validation input set (staged files + content, policy, kern version,
// blueprint version, flags) replays the prior verdict instead of re-running
// the full scan. Kern invocations are counted with a logging wrapper so a
// cache hit can be proven to perform zero kern subprocess calls.

// writeKernWrapper creates a shim around the real kern binary that logs every
// invocation (argv) to logPath and then execs the real binary, so tests can
// assert how many times kern was spawned.
func writeKernWrapper(t *testing.T, realKern, logPath string) string {
	t.Helper()
	wrapper := filepath.Join(t.TempDir(), "kern-wrapper")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> \"%s\"\nexec \"%s\" \"$@\"\n", logPath, realKern)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("write kern wrapper: %v", err)
	}
	return wrapper
}

func countKernInvocations(logPath string) int {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// runCICommandEnv is runCICommand with extra environment variables appended.
func runCICommandEnv(t *testing.T, binPath, repoDir, kernPath string, env []string, extraArgs ...string) (string, string, int, CIArtifact) {
	t.Helper()
	artifactPath := filepath.Join(t.TempDir(), "result.json")
	args := []string{"ci", "--repo", repoDir, "--base", "main", "--head", "feature", "--artifact-file", artifactPath, "--no-human"}
	args = append(args, extraArgs...)
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), "KERN_BINARY="+kernPath)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	exitCode := 0
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("run blueprint ci: %v", err)
		}
	}
	artifactBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var artifact CIArtifact
	if err := json.Unmarshal(artifactBytes, &artifact); err != nil {
		t.Fatalf("parse artifact: %v\nraw: %s", err, artifactBytes)
	}
	return stdout.String(), stderr.String(), exitCode, artifact
}

// TestCIVerdictCacheHit runs CI on a diff twice. The second run must replay
// the first run's verdict from the cache: same exit code, same findings, a
// "hit" marker on the artifact, and zero kern subprocess invocations.
func TestCIVerdictCacheHit(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	logPath := filepath.Join(t.TempDir(), "kern.log")
	wrapper := writeKernWrapper(t, kernPath, logPath)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		})

	// Run 1: full validation (cache miss).
	_, _, exit1, art1 := runCICommand(t, binPath, dir, wrapper)
	if art1.CacheStatus == "hit" {
		t.Fatalf("first run must be a cache miss, got cache_status=%q", art1.CacheStatus)
	}
	if art1.CacheKey == "" {
		t.Fatal("first run should report its cache key")
	}
	if exit1 != 1 {
		t.Fatalf("run 1 exit = %d, want 1 (BLOCK); artifact: %+v", exit1, art1)
	}
	inv1 := countKernInvocations(logPath)
	if inv1 == 0 {
		t.Fatal("run 1 should have invoked kern (full validation)")
	}

	// Run 2: identical inputs -> verdict cache hit, zero kern invocations.
	_, _, exit2, art2 := runCICommand(t, binPath, dir, wrapper)
	inv2 := countKernInvocations(logPath)
	if art2.CacheStatus != "hit" {
		t.Fatalf("second run should be served from cache, got cache_status=%q", art2.CacheStatus)
	}
	if art2.CacheKey != art1.CacheKey {
		t.Errorf("cache key differs between runs: %s vs %s", art1.CacheKey, art2.CacheKey)
	}
	if exit2 != exit1 {
		t.Errorf("cached exit code = %d, want %d (identical verdict)", exit2, exit1)
	}
	if inv2 != inv1 {
		t.Errorf("kern was invoked on a cache hit: invocations %d -> %d", inv1, inv2)
	}
	if art2.Status != art1.Status {
		t.Errorf("cached status = %s, want %s", art2.Status, art1.Status)
	}
	if art2.FindingsCount != art1.FindingsCount {
		t.Errorf("cached findings_count = %d, want %d", art2.FindingsCount, art1.FindingsCount)
	}
	if len(art2.Findings) != len(art1.Findings) {
		t.Errorf("cached findings = %d, want %d", len(art2.Findings), len(art1.Findings))
	}
	for i := range art1.Findings {
		if i >= len(art2.Findings) {
			break
		}
		if art2.Findings[i].RuleID != art1.Findings[i].RuleID || art2.Findings[i].Message != art1.Findings[i].Message {
			t.Errorf("cached finding[%d] differs from the fresh run", i)
		}
	}
}

// TestCIVerdictCacheMissOnChange runs CI on a diff, modifies a staged file on
// the feature branch, and runs CI again: the changed content must change the
// cache key and force a full re-validation.
func TestCIVerdictCacheMissOnChange(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	logPath := filepath.Join(t.TempDir(), "kern.log")
	wrapper := writeKernWrapper(t, kernPath, logPath)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		})

	_, _, exit1, art1 := runCICommand(t, binPath, dir, wrapper)
	if art1.CacheStatus == "hit" {
		t.Fatalf("first run must be a cache miss, got cache_status=%q", art1.CacheStatus)
	}
	inv1 := countKernInvocations(logPath)

	// Modify the staged file on the feature branch (new commit -> new head).
	g11Git(t, dir, "checkout", "feature")
	g11Write(t, dir, "web/bad.go", "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\nfunc Bad2() { db.Query() }\n")
	g11Git(t, dir, "add", "-A")
	g11Git(t, dir, "commit", "-qm", "feature v2")
	g11Git(t, dir, "checkout", "main")

	_, _, exit2, art2 := runCICommand(t, binPath, dir, wrapper)
	inv2 := countKernInvocations(logPath)

	if art2.CacheStatus == "hit" {
		t.Errorf("modified staged file must miss the cache, got cache_status=%q", art2.CacheStatus)
	}
	if art2.CacheKey == art1.CacheKey {
		t.Errorf("cache key must change when a staged file changes: %s", art1.CacheKey)
	}
	if inv2 <= inv1 {
		t.Errorf("expected full re-validation (more kern invocations): %d -> %d", inv1, inv2)
	}
	if exit2 != exit1 {
		t.Errorf("exit = %d, want %d (both runs still BLOCK)", exit2, exit1)
	}
}

// TestCIVerdictCacheMissOnPolicyChange runs CI, changes the policy config, and
// runs CI again: the policy hash is part of the cache key, so the second run
// must miss. Reverting the config must hit again.
func TestCIVerdictCacheMissOnPolicyChange(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	logPath := filepath.Join(t.TempDir(), "kern.log")
	wrapper := writeKernWrapper(t, kernPath, logPath)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		nil)

	configPath := filepath.Join(dir, ".blueprint", "config.yaml")

	// Run 1 (--head HEAD on main, no config): miss.
	_, _, exit1, art1 := runCICommand(t, binPath, dir, wrapper, "--head", "HEAD")
	if art1.CacheStatus == "hit" {
		t.Fatalf("first run must be a cache miss, got cache_status=%q", art1.CacheStatus)
	}

	// Change the policy config in the working tree (not part of the git diff).
	g11Write(t, dir, ".blueprint/config.yaml", "version: 1\nmode: warn\npolicies:\n  architecture: warn\n")

	// Run 2: policy hash changed -> miss.
	_, _, exit2, art2 := runCICommand(t, binPath, dir, wrapper, "--head", "HEAD")
	if art2.CacheStatus == "hit" {
		t.Errorf("policy config change must miss the cache, got cache_status=%q", art2.CacheStatus)
	}
	if art2.CacheKey == art1.CacheKey {
		t.Errorf("cache key must change when the policy config changes: %s", art1.CacheKey)
	}
	inv2 := countKernInvocations(logPath)

	// Revert the policy config: must hit the run-1 entry again.
	os.Remove(configPath)
	_, _, exit3, art3 := runCICommand(t, binPath, dir, wrapper, "--head", "HEAD")
	inv3 := countKernInvocations(logPath)
	if art3.CacheStatus != "hit" {
		t.Errorf("reverted policy config should hit the cached verdict, got cache_status=%q", art3.CacheStatus)
	}
	if art3.CacheKey != art1.CacheKey {
		t.Errorf("reverted policy config should restore the run-1 key: %s vs %s", art1.CacheKey, art3.CacheKey)
	}
	if inv3 != inv2 {
		t.Errorf("kern was invoked on a cache hit: invocations %d -> %d", inv2, inv3)
	}
	if exit1 != 0 || exit2 != 0 || exit3 != 0 {
		t.Errorf("empty diff should PASS (exit 0) on all runs: %d, %d, %d", exit1, exit2, exit3)
	}
}

// TestCIVerdictCacheBypass primes the cache, then re-runs with --no-cache:
// the cache must not be read (no "hit" marker), a fresh validation must run,
// and the cache must not be written.
func TestCIVerdictCacheBypass(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	logPath := filepath.Join(t.TempDir(), "kern.log")
	wrapper := writeKernWrapper(t, kernPath, logPath)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		})

	// Prime the cache with a normal run.
	_, _, _, art1 := runCICommand(t, binPath, dir, wrapper)
	if art1.CacheStatus == "hit" {
		t.Fatalf("priming run must be a cache miss, got cache_status=%q", art1.CacheStatus)
	}
	inv1 := countKernInvocations(logPath)
	cacheDir := filepath.Join(dir, ".blueprint", "verdict-cache")
	filesBefore := listCacheFiles(t, cacheDir)
	if len(filesBefore) == 0 {
		t.Fatal("priming run should have written cache entries")
	}

	// Bypass with --no-cache: cache not read, full validation runs, no writes.
	_, _, exit2, art2 := runCICommand(t, binPath, dir, wrapper, "--no-cache")
	inv2 := countKernInvocations(logPath)
	if art2.CacheStatus == "hit" {
		t.Errorf("--no-cache must not read the cache, got cache_status=%q", art2.CacheStatus)
	}
	if inv2 <= inv1 {
		t.Errorf("--no-cache must run a fresh full validation: invocations %d -> %d", inv1, inv2)
	}
	if exit2 != 1 {
		t.Errorf("--no-cache exit = %d, want 1 (BLOCK)", exit2)
	}
	filesAfter := listCacheFiles(t, cacheDir)
	if len(filesAfter) != len(filesBefore) {
		t.Errorf("--no-cache must not write cache entries: %d before, %d after", len(filesBefore), len(filesAfter))
	}
}

// TestCIVerdictCacheBypassEnv is TestCIVerdictCacheBypass via the
// BLUEPRINT_NO_CACHE=1 environment variable instead of the flag.
func TestCIVerdictCacheBypassEnv(t *testing.T) {
	kernPath := requireKernPath(t)
	binPath := buildBlueprint(t)
	logPath := filepath.Join(t.TempDir(), "kern.log")
	wrapper := writeKernWrapper(t, kernPath, logPath)
	dir := g11Repo(t,
		map[string]string{
			"db/db.go":   "package db\nfunc Query() {}\n",
			"web/web.go": "package web\nfunc Handle() {}\n",
		},
		map[string]string{
			"web/bad.go": "package web\nimport \"example.com/repo/db\"\nfunc Bad() { db.Query() }\n",
		})

	_, _, _, art1 := runCICommand(t, binPath, dir, wrapper)
	if art1.CacheStatus == "hit" {
		t.Fatalf("priming run must be a cache miss, got cache_status=%q", art1.CacheStatus)
	}
	inv1 := countKernInvocations(logPath)
	cacheDir := filepath.Join(dir, ".blueprint", "verdict-cache")
	filesBefore := listCacheFiles(t, cacheDir)

	_, _, exit2, art2 := runCICommandEnv(t, binPath, dir, wrapper, []string{"BLUEPRINT_NO_CACHE=1"})
	inv2 := countKernInvocations(logPath)
	if art2.CacheStatus == "hit" {
		t.Errorf("BLUEPRINT_NO_CACHE=1 must not read the cache, got cache_status=%q", art2.CacheStatus)
	}
	if inv2 <= inv1 {
		t.Errorf("BLUEPRINT_NO_CACHE=1 must run a fresh full validation: invocations %d -> %d", inv1, inv2)
	}
	if exit2 != 1 {
		t.Errorf("BLUEPRINT_NO_CACHE=1 exit = %d, want 1 (BLOCK)", exit2)
	}
	filesAfter := listCacheFiles(t, cacheDir)
	if len(filesAfter) != len(filesBefore) {
		t.Errorf("BLUEPRINT_NO_CACHE=1 must not write cache entries: %d before, %d after", len(filesBefore), len(filesAfter))
	}
}

// listCacheFiles returns the non-meta files in the verdict cache dir (the
// per-key entries), for asserting that a bypassed run does not write.
func listCacheFiles(t *testing.T, cacheDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "meta.json" {
			continue
		}
		files = append(files, e.Name())
	}
	return files
}
