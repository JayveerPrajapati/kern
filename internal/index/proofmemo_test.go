package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The freshness-proof memo (M#10/M#13, 2026-10-24): a non-strict
// FreshnessProof re-serves the last verdict for
// KERN_FRESHNESS_PROOF_CACHE_MS, so a tree mutation is only observed by
// the proof once the TTL expires — unless the memo is disabled (0). These
// tests pin all three behaviors on a single index instance, mirroring how
// a resident MCP server calls the proof per tool call.

func TestProofMemoServesVerdictWithinTTL(t *testing.T) {
	t.Setenv("KERN_FRESHNESS_PROOF_CACHE_MS", "60000")
	dir := writeTree(t, map[string]string{"main.go": "package main\n\nfunc A() {}\n"})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p := ix.FreshnessProof(dir); p.Verdict != FreshnessFresh {
		t.Fatalf("initial verdict = %q, want fresh", p.Verdict)
	}
	// Mutate; the memoized verdict must be re-served within the TTL window.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := ix.FreshnessProof(dir); p.Verdict != FreshnessFresh {
		t.Fatalf("within TTL: verdict = %q, want fresh (memoized)", p.Verdict)
	}
}

func TestProofMemoExpiresAndObservesMutation(t *testing.T) {
	t.Setenv("KERN_FRESHNESS_PROOF_CACHE_MS", "1")
	dir := writeTree(t, map[string]string{"main.go": "package main\n\nfunc A() {}\n"})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p := ix.FreshnessProof(dir); p.Verdict != FreshnessFresh {
		t.Fatalf("initial verdict = %q, want fresh", p.Verdict)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // TTL=1ms: the memo must have expired
	if p := ix.FreshnessProof(dir); p.Verdict != FreshnessStale {
		t.Fatalf("after TTL: verdict = %q, want stale (mutation must be observed once the memo expires)", p.Verdict)
	}
}

func TestProofMemoDisabledByEnvZero(t *testing.T) {
	t.Setenv("KERN_FRESHNESS_PROOF_CACHE_MS", "0")
	dir := writeTree(t, map[string]string{"main.go": "package main\n\nfunc A() {}\n"})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p := ix.FreshnessProof(dir); p.Verdict != FreshnessFresh {
		t.Fatalf("initial verdict = %q, want fresh", p.Verdict)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := ix.FreshnessProof(dir); p.Verdict != FreshnessStale {
		t.Fatalf("memo disabled: verdict = %q, want stale (live computation)", p.Verdict)
	}
}
