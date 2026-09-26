package heal

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// fakePlaybook is an in-memory heal.Playbook for tests: configurable Lookup
// hits plus recorded writes so tests can assert what the loop recorded.
type fakePlaybook struct {
	mu          sync.Mutex
	hits        map[string][]Replacement
	recorded    map[string][]Replacement
	lookupCalls int
	recordCalls int
}

func (f *fakePlaybook) Lookup(signature string) ([]Replacement, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookupCalls++
	reps, ok := f.hits[signature]
	return reps, ok
}

func (f *fakePlaybook) Record(signature string, reps []Replacement) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordCalls++
	if f.recorded == nil {
		f.recorded = map[string][]Replacement{}
	}
	f.recorded[signature] = reps
	return nil
}

// TestSignatureForDeterministic: the same (task,file) always yields the same
// signature; a different task or file yields a different one.
func TestSignatureForDeterministic(t *testing.T) {
	sig := SignatureFor("fix the build", "app.go")
	if sig != SignatureFor("fix the build", "app.go") {
		t.Fatal("same inputs must yield the same signature")
	}
	if len(sig) != 12 {
		t.Fatalf("signature length = %d, want 12 (contentHash12-style)", len(sig))
	}
	if sig == SignatureFor("fix the tests", "app.go") {
		t.Error("different task must yield a different signature")
	}
	if sig == SignatureFor("fix the build", "main.go") {
		t.Error("different file must yield a different signature")
	}
}

// TestEncodeDecodeReplacementsRoundTrip pins the deterministic text format:
// EncodeReplacements renders one "path|old|new" line per replacement (old/new
// base64) and DecodeReplacements reconstructs the replacements exactly.
func TestEncodeDecodeReplacementsRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("old content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reps := []Replacement{
		{Path: "a.go", Content: "package main\n\nfunc main() {}\n"},
		{Path: "b.go", Content: "package b\n"},
	}
	steps := EncodeReplacements(root, reps)
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}
	// Deterministic: identical root state + reps produce identical steps.
	if !reflect.DeepEqual(steps, EncodeReplacements(root, reps)) {
		t.Fatal("EncodeReplacements must be deterministic")
	}
	// Round-trip: decode yields the original replacements.
	got := DecodeReplacements(steps)
	if !reflect.DeepEqual(got, reps) {
		t.Fatalf("round-trip = %+v, want %+v", got, reps)
	}
	// Paths containing '|' are not representable and are skipped.
	if len(EncodeReplacements(root, []Replacement{{Path: "a|b.go", Content: "x"}})) != 0 {
		t.Fatal("path containing '|' must be skipped")
	}
	// Malformed lines are skipped, never fatal.
	if got := DecodeReplacements([]string{"bad", "a|b", "x|y|z!!"}); len(got) != 0 {
		t.Fatalf("malformed lines must be skipped, got %+v", got)
	}
}

// TestRunWithPlaybookNilIsRun: a nil Playbook must be exactly Run — no
// signature computation, no playbook consult, same result shape.
func TestRunWithPlaybookNilIsRun(t *testing.T) {
	root := newBrokenGoProject(t)
	srv := mockOllama(t, "### FILE: app.go\npackage main\n\nfunc main() {}\n")
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	base := Run(context.Background(), root, "fix the syntax errors", "", 3, 60*time.Second, false)
	withPB := RunWithPlaybook(context.Background(), root, "fix the syntax errors", "", 3, 60*time.Second, false, nil)
	if withPB.Err != nil {
		t.Fatalf("nil-pb run: %v", withPB.Err)
	}
	if base.Err != nil {
		t.Fatalf("base run: %v", base.Err)
	}
	if !withPB.Validated || withPB.Iterations != base.Iterations || !reflect.DeepEqual(withPB.Changes, base.Changes) {
		t.Fatalf("nil-pb result differs from Run: base=%+v withPB=%+v", base, withPB)
	}
	if withPB.UsedPlaybook {
		t.Fatal("nil pb must never set UsedPlaybook")
	}
}

// TestRunWithPlaybookRecordsOnSuccess: a full-heal success records the
// applied replacements under the deterministic signature, and the recorded
// steps round-trip through the text format back to the same replacements.
func TestRunWithPlaybookRecordsOnSuccess(t *testing.T) {
	root := newBrokenGoProject(t)
	srv := mockOllama(t, "### FILE: app.go\npackage main\n\nfunc main() {}\n")
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	pb := &fakePlaybook{hits: map[string][]Replacement{}}
	res := RunWithPlaybook(context.Background(), root, "fix the syntax errors", "", 3, 60*time.Second, false, pb)
	if res.Err != nil {
		t.Fatalf("heal: %v", res.Err)
	}
	if !res.Validated {
		t.Fatalf("expected validated, last output:\n%s", res.LastOutput)
	}
	pb.mu.Lock()
	defer pb.mu.Unlock()
	sig := SignatureFor("fix the syntax errors", "")
	recorded, ok := pb.recorded[sig]
	if !ok {
		t.Fatalf("expected Record under signature %s, recorded=%v (calls=%d)", sig, pb.recorded, pb.recordCalls)
	}
	if len(recorded) != 1 || recorded[0].Path != "app.go" || !reflect.DeepEqual(recorded[0].Content, "package main\n\nfunc main() {}") {
		t.Fatalf("recorded replacements = %+v, want the applied fix for app.go", recorded)
	}
	// The recorded steps round-trip through the deterministic text format.
	steps := EncodeReplacements(root, recorded)
	if back := DecodeReplacements(steps); !reflect.DeepEqual(back, recorded) {
		t.Fatalf("steps round-trip = %+v, want %+v", back, recorded)
	}
}

// TestRunFileWithPlaybookLookupHitSkipsLLM: a matching recorded fix is
// applied and verified without any LLM round (no provider configured at all);
// the result is marked UsedPlaybook.
func TestRunFileWithPlaybookLookupHitSkipsLLM(t *testing.T) {
	root := newBrokenGoProject(t)
	// No OLLAMA_HOST / provider env: if the loop tried an LLM round it would
	// fail immediately, so success proves the fast path skipped the LLM.
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	fix := []Replacement{{Path: "app.go", Content: "package main\n\nfunc main() {}\n"}}
	pb := &fakePlaybook{hits: map[string][]Replacement{SignatureFor("fix the syntax errors", "app.go"): fix}}
	res := RunFileWithPlaybook(context.Background(), root, "fix the syntax errors", "", "app.go", 3, 60*time.Second, false, pb)
	if res.Err != nil {
		t.Fatalf("fast path: %v", res.Err)
	}
	if !res.Validated || !res.UsedPlaybook {
		t.Fatalf("expected playbook fast-path success, got %+v", res)
	}
	if res.Iterations != 0 {
		t.Fatalf("no LLM rounds expected, got %d", res.Iterations)
	}
	if len(res.Changes) != 1 || res.Changes[0] != "app.go" {
		t.Fatalf("changes = %v, want [app.go]", res.Changes)
	}
	if !reflect.DeepEqual(res.Reps, fix) {
		t.Fatalf("Reps = %+v, want the replayed fix %+v", res.Reps, fix)
	}
}

// TestRunWithPlaybookFailedFixEscalates: a recorded fix that fails
// validation must escalate to the full LLM loop — never a silent bypass —
// and the loop's success re-records the working fix.
func TestRunWithPlaybookFailedFixEscalates(t *testing.T) {
	root := newBrokenGoProject(t)
	srv := mockOllama(t, "### FILE: app.go\npackage main\n\nfunc main() {}\n")
	defer srv.Close()
	t.Setenv("OLLAMA_HOST", srv.URL)
	t.Setenv("KERN_LLM_PROVIDER", "ollama")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	brokenFix := []Replacement{{Path: "app.go", Content: "package main\n\nfunc broken(\n"}}
	pb := &fakePlaybook{hits: map[string][]Replacement{SignatureFor("fix the syntax errors", ""): brokenFix}}
	res := RunWithPlaybook(context.Background(), root, "fix the syntax errors", "", 3, 60*time.Second, false, pb)
	if res.Err != nil {
		t.Fatalf("heal: %v", res.Err)
	}
	if !res.Validated {
		t.Fatalf("expected validated via escalation, last output:\n%s", res.LastOutput)
	}
	if res.UsedPlaybook {
		t.Fatal("a failed recorded fix must not be reported as a playbook hit")
	}
	if res.Iterations < 1 {
		t.Fatalf("expected the full LLM loop to run, got %d iterations", res.Iterations)
	}
	// The successful loop re-records the working fix under the signature.
	pb.mu.Lock()
	defer pb.mu.Unlock()
	sig := SignatureFor("fix the syntax errors", "")
	recorded, ok := pb.recorded[sig]
	if !ok {
		t.Fatalf("expected Record under signature %s, recorded=%v", sig, pb.recorded)
	}
	if len(recorded) != 1 || !reflect.DeepEqual(recorded[0].Content, "package main\n\nfunc main() {}") {
		t.Fatalf("re-recorded fix = %+v, want the working fix", recorded)
	}
}
