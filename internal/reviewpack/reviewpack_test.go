package reviewpack

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// fixtureRepo writes a tiny Go repo and commits it (when commit=true).
func fixtureRepo(t *testing.T, commit bool) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":       "module fixture\n\ngo 1.23\n",
		"main.go":      "package main\n\nfunc Count(s string) int { return len(s) }\n\nfunc main() { println(Count(\"hi\")) }\n",
		"helper.go":    "package main\n\nfunc Helper(x int) int { return x * 2 }\n",
		"util_test.go": "package main\n\nimport \"testing\"\n\nfunc TestCount(t *testing.T) { if Count(\"ab\") != 2 { t.Fail() } }\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if commit {
		for _, args := range [][]string{
			{"-C", root, "init", "-q"},
			{"-C", root, "add", "-A"},
			{"-C", root, "-c", "user.email=test@fixture", "-c", "user.name=test", "commit", "-q", "-m", "init"},
		} {
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v (%s)", args, err, out)
			}
		}
	}
	return root
}

func samplePacket() *domain.ContextPacket {
	return &domain.ContextPacket{
		Task: "fix the Count panic",
		Facts: []domain.Claim{
			{Type: domain.ClaimFact, Statement: "Count is called on nil", Status: domain.ClaimStatusObserved,
				Evidence: []domain.Evidence{{Type: domain.EvidenceGraph, Source: "fixture", Digest: "d1"}}},
			{Type: domain.ClaimInference, Statement: "Helper may overflow", Status: domain.ClaimStatusInferred},
			{Type: domain.ClaimFact, Statement: "test covers Count", Status: domain.ClaimStatusVerifiedDerived,
				Evidence: []domain.Evidence{{Type: domain.EvidenceTest, Source: "util_test.go", Digest: "d2"}}},
		},
		Symbols: []domain.Symbol{
			{Name: "Count", Kind: "func", File: "main.go", Line: 3},
			{Name: "Helper", Kind: "func", File: "helper.go", Line: 3},
		},
		ArchitectureRules: []domain.Policy{
			{Name: "no-cross-boundary", Rule: "internal may not import cmd", Enabled: true},
		},
	}
}

func buildFixture(t *testing.T, root string, pkt *domain.ContextPacket) *ReviewPack {
	t.Helper()
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index build: %v", err)
	}
	p, err := Build(root, pkt.Task, pkt, ix, Options{Lens: "security", MaxTokens: 2000})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return p
}

func TestBuildSections(t *testing.T) {
	root := fixtureRepo(t, true)
	p := buildFixture(t, root, samplePacket())

	if p.SchemaVersion != 1 {
		t.Errorf("schema version = %d", p.SchemaVersion)
	}
	if p.Commit == "" {
		t.Error("commit empty in git repo")
	}
	if len(p.DirtyHash) != 64 {
		t.Errorf("dirty hash = %q", p.DirtyHash)
	}
	if p.Lens != "security" {
		t.Errorf("lens = %q", p.Lens)
	}
	if len(p.Evidence) == 0 {
		t.Error("no planner evidence selections")
	}
	for _, s := range p.Evidence {
		if s.Reason == "" {
			t.Error("selection missing reason")
		}
	}
	if len(p.Symbols) == 0 || p.Symbols[0].Name != "Count" {
		t.Errorf("symbols = %+v", p.Symbols)
	}
	if len(p.Claims) == 0 {
		t.Error("no observed claims")
	}
	foundAssumption := false
	for _, a := range p.Assumptions {
		if strings.Contains(a.Statement, "overflow") {
			foundAssumption = true
		}
	}
	if !foundAssumption {
		t.Errorf("inferred claim not classified as assumption: %+v", p.Assumptions)
	}
	if len(p.Constraints) != 1 || p.Constraints[0].Name != "no-cross-boundary" {
		t.Errorf("constraints = %+v", p.Constraints)
	}
	if p.TokenCount <= 0 {
		t.Error("token count = 0")
	}
	// Exact token accounting: render-body count matches, sections sum matches.
	if got := tokenize.Count(renderBody(p)); got != p.TokenCount {
		t.Errorf("render tokens = %d, pack says %d", got, p.TokenCount)
	}
	sum := 0
	for _, s := range p.Sections {
		sum += s.Tokens
	}
	if sum != p.TokenCount {
		t.Errorf("sections sum %d != token count %d", sum, p.TokenCount)
	}
	if len(p.ContentHash) != 64 {
		t.Errorf("content hash = %q", p.ContentHash)
	}
}

func TestBuildDeterministic(t *testing.T) {
	root := fixtureRepo(t, true)
	p1 := buildFixture(t, root, samplePacket())
	p2 := buildFixture(t, root, samplePacket())
	if p1.ContentHash != p2.ContentHash {
		t.Errorf("hash differs across identical builds: %s vs %s", p1.ContentHash, p2.ContentHash)
	}
	// Full JSON must be identical too, with GeneratedAt zeroed (timestamp is
	// provenance metadata, intentionally excluded from the hash).
	p1.GeneratedAt = time.Time{}
	p2.GeneratedAt = time.Time{}
	b1, _ := json.Marshal(p1)
	b2, _ := json.Marshal(p2)
	if string(b1) != string(b2) {
		t.Error("pack JSON differs across identical builds")
	}
}

func TestDirtyHashChangesOnEdit(t *testing.T) {
	root := fixtureRepo(t, true)
	p1 := buildFixture(t, root, samplePacket())
	// Dirty the tree: append to a tracked file.
	if err := os.WriteFile(filepath.Join(root, "helper.go"), []byte("package main\n\nfunc Helper(x int) int { return x * 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p2 := buildFixture(t, root, samplePacket())
	if p1.DirtyHash == p2.DirtyHash {
		t.Error("dirty hash unchanged after file edit")
	}
	if len(p2.ChangedFiles) == 0 {
		t.Error("changed files empty on dirty tree")
	}
}

func TestBuildNonGit(t *testing.T) {
	root := fixtureRepo(t, false) // no git init
	p := buildFixture(t, root, samplePacket())
	if p.Commit != "" {
		t.Errorf("commit = %q in non-git dir", p.Commit)
	}
	if len(p.ChangedFiles) != 0 {
		t.Errorf("changed files = %v in non-git dir", p.ChangedFiles)
	}
	if p.DirtyHash == "" {
		t.Error("dirty hash empty (should be deterministic sha256 of empty state)")
	}
}

func TestBuildUnknownLens(t *testing.T) {
	root := fixtureRepo(t, true)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(root, "task", samplePacket(), ix, Options{Lens: "no-such-lens"}); err == nil {
		t.Fatal("expected error for unknown lens")
	}
}

func TestBuildErrors(t *testing.T) {
	root := fixtureRepo(t, true)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(root, "", samplePacket(), ix, Options{}); err == nil {
		t.Error("expected error for empty task")
	}
	if _, err := Build(root, "task", nil, ix, Options{}); err == nil {
		t.Error("expected error for nil packet")
	}
	if _, err := Build(root, "task", samplePacket(), nil, Options{}); err == nil {
		t.Error("expected error for nil index")
	}
}

func TestReviewPackJSONRoundTrip(t *testing.T) {
	root := fixtureRepo(t, true)
	p := buildFixture(t, root, samplePacket())
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back ReviewPack
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.ContentHash != p.ContentHash {
		t.Error("content hash lost in round trip")
	}
	if len(back.Symbols) != len(p.Symbols) {
		t.Error("symbols lost in round trip")
	}
}

func TestGeneratedAtStableFormat(t *testing.T) {
	root := fixtureRepo(t, true)
	p := buildFixture(t, root, samplePacket())
	// GeneratedAt must be UTC for determinism across machines.
	if _, err := time.Parse(time.RFC3339, p.GeneratedAt.Format(time.RFC3339)); err != nil {
		t.Errorf("generated_at not RFC3339: %v", err)
	}
	if p.GeneratedAt.Location() != time.UTC {
		t.Errorf("generated_at not UTC: %v", p.GeneratedAt.Location())
	}
}
