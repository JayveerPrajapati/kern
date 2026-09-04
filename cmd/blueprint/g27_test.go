package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// g27RequireKern returns a kern binary that actually supports `kern audit
// append` (source-fresh builds do; released/PATH binaries may predate the
// subcommand and would treat "append" as a task-id filter). Candidates are
// tried in adapters/kern resolution order — KERN_BINARY, $PATH, then
// ../kern/bin/kern relative to the blueprint repo root — and each is probed
// with a real append in a temp dir. The test is skipped when no candidate
// works, so G27 runs wherever a current kern is reachable.
func g27RequireKern(t *testing.T) string {
	t.Helper()
	var candidates []string
	if p := os.Getenv("KERN_BINARY"); p != "" {
		candidates = append(candidates, absOr(p))
	}
	candidates = append(candidates, filepath.Join(findRepoRoot(t), "bin", "kern"))
	candidates = append(candidates, filepath.Join(findRepoRoot(t), "..", "kern", "bin", "kern"))
	if p, err := exec.LookPath("kern"); err == nil {
		candidates = append(candidates, absOr(p))
	}

	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		if info, err := os.Stat(c); err != nil || info.IsDir() {
			continue
		}
		if g27KernSupportsAppend(t, c) {
			return c
		}
	}
	t.Skipf("no kern binary with `audit append` found (tried: %v)", candidates)
	return ""
}

// g27KernSupportsAppend probes whether bin can link an entry: it runs
// `kern audit append --root <fresh temp dir>` with a minimal entry and
// requires exit 0 plus an "appended audit-..." acknowledgement.
func g27KernSupportsAppend(t *testing.T, bin string) bool {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command(bin, "audit", "append", "--root", root)
	cmd.Stdin = strings.NewReader(`{"AgentID":"probe","Action":"probe","Resource":"probe","Result":"PASS"}`)
	out, err := cmd.CombinedOutput()
	return err == nil && strings.Contains(string(out), "appended audit-")
}

// g27Entry mirrors kern's internal/governance/audit.AuditEntry as persisted
// under <root>/.kern/audit/audit-*.json (kern's struct has no json tags, so
// the file uses the exported field names). Field order matches kern's
// domain.Risk so %v renders identically during hash recomputation.
type g27Entry struct {
	ID        string    `json:"ID"`
	Timestamp time.Time `json:"Timestamp"`
	AgentID   string    `json:"AgentID"`
	Action    string    `json:"Action"`
	Resource  string    `json:"Resource"`
	Risk      g27Risk   `json:"Risk"`
	Approved  bool      `json:"Approved"`
	Result    string    `json:"Result"`
	Hash      string    `json:"Hash"`
	TaskID    string    `json:"TaskID"`
}

type g27Risk struct {
	Level            string   `json:"Level"`
	Factors          []string `json:"Factors"`
	Score            float64  `json:"Score"`
	Mitigation       string   `json:"Mitigation"`
	Blocked          bool     `json:"Blocked"`
	ApprovalRequired bool     `json:"ApprovalRequired"`
}

// g27Hash is the test-side reimplementation of kern's computeAuditHash:
// sha256 over prevHash|ID|AgentID|Action|Resource|Timestamp.UnixNano|Risk|
// Approved|Result|TaskID. Used to verify chain integrity without importing
// the kern module (blueprint must stay a standalone module).
func g27Hash(e g27Entry, prev string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%v|%v|%v|%s|%s", prev, e.ID, e.AgentID, e.Action, e.Resource, e.Timestamp.UnixNano(), e.Risk, e.Approved, e.Result, e.TaskID)
	return hex.EncodeToString(h.Sum(nil))
}

// g27ReadChain reads every persisted entry from <root>/.kern/audit/, sorted
// by key exactly like kern's storage.Store.List (string order).
func g27ReadChain(t *testing.T, root string) []g27Entry {
	t.Helper()
	dir := filepath.Join(root, ".kern", "audit")
	chainFile := filepath.Join(dir, "chain.jsonl")
	if raw, err := os.ReadFile(chainFile); err == nil {
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		var out []g27Entry
		for _, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			var env struct {
				K string   `json:"k"`
				V g27Entry `json:"v"`
			}
			if err := json.Unmarshal([]byte(l), &env); err == nil && env.V.ID != "" {
				out = append(out, env.V)
				continue
			}
			var e g27Entry
			if err := json.Unmarshal([]byte(l), &e); err == nil && e.ID != "" {
				out = append(out, e)
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	matches, err := filepath.Glob(filepath.Join(dir, "audit-*.json"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	sort.Strings(matches)
	var out []g27Entry
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("read %s: %v", m, err)
		}
		var e g27Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("parse %s: %v", m, err)
		}
		out = append(out, e)
	}
	return out
}

// TestG27_AuditChainLinked is the end-to-end gate for B2: a blueprint
// validation that BLOCKs must (a) write the local self-hashed JSONL and (b)
// best-effort link a mapped entry into kern's tamper-evident chain under the
// repo's .kern/audit/ — and that chain must verify (each hash covers the
// previous). Requires a real kern binary (skipped when unavailable).
func TestG27_AuditChainLinked(t *testing.T) {
	kernBin := g27RequireKern(t)
	t.Setenv("KERN_BINARY", kernBin)

	bin := g4BuildBinary(t)
	dir := t.TempDir()
	g4GitRepo(t, dir)
	g4WriteBoundaries(t, dir)
	g4WriteFile(t, dir, "db/db.go", "package db\nfunc Query() {}\n")
	g4WriteFile(t, dir, "web/web.go", "package web\nfunc Handle() {}\n")
	g4WriteFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	g4RunGit(t, dir, "add", "-A")
	g4RunGit(t, dir, "commit", "-qm", "init")

	// Stage a violating change → the validation must BLOCK.
	g4WriteFile(t, dir, "web/web2.go", "package web\nimport \"example.com/repo/db\"\nfunc Handle2() { db.Query() }\n")
	g4RunGit(t, dir, "add", "web/web2.go")

	out, code := g26Check(t, bin, dir)
	if code != 1 {
		t.Fatalf("exit=%d want 1 (BLOCK); output:\n%s", code, out)
	}
	res := g26Parse(t, out)
	if res.Status != domain.StatusBlock {
		t.Fatalf("status = %q, want %q; output:\n%s", res.Status, domain.StatusBlock, out)
	}

	// 1. The local blueprint JSONL is written (existing P1-1 behavior).
	localPath := filepath.Join(dir, ".blueprint", "audit", "audit.jsonl")
	raw, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("local audit JSONL not written: %v", err)
	}
	var rec audit.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("parse local audit record: %v", err)
	}
	if rec.Hash == "" {
		t.Error("local audit record has no self-hash")
	}
	if rec.Status != domain.StatusBlock {
		t.Errorf("local record status = %q, want BLOCK", rec.Status)
	}

	// 2. kern's chain has the linked entry: exactly one validation ran, so
	//    exactly one chain link exists under <dir>/.kern/audit/.
	chain := g27ReadChain(t, dir)
	if len(chain) == 0 {
		t.Fatal("no linked entry in kern's .kern/audit/ chain")
	}
	linked := chain[len(chain)-1]
	if linked.Result != "BLOCK" {
		t.Errorf("linked entry Result = %q, want BLOCK (the blueprint verdict)", linked.Result)
	}
	absRoot, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if linked.Resource != absRoot {
		t.Errorf("linked entry Resource = %q, want %q (the repo root)", linked.Resource, absRoot)
	}
	if linked.AgentID == "" || linked.Action == "" || linked.Hash == "" {
		t.Errorf("linked entry incomplete: %+v", linked)
	}

	// 3. The chain verifies: replay every persisted entry, recomputing each
	//    hash from the previous one (kern VerifyChain semantics).
	prev := ""
	for i, e := range chain {
		if e.Hash != g27Hash(e, prev) {
			t.Fatalf("chain broken at entry %d (%s): stored hash %q != recomputed %q", i, e.ID, e.Hash, g27Hash(e, prev))
		}
		prev = e.Hash
	}

	// 4. Tampering with the linked blueprint line breaks the chain: flip a
	//    field in the persisted kern entry and re-verify.
	chainFile := filepath.Join(dir, ".kern", "audit", "chain.jsonl")
	if raw, err := os.ReadFile(chainFile); err == nil {
		tampered := strings.Replace(string(raw), `"AgentID":"`+linked.AgentID+`"`, `"AgentID":"evil-agent"`, 1)
		if err := os.WriteFile(chainFile, []byte(tampered), 0o644); err != nil {
			t.Fatalf("write tampered chain: %v", err)
		}
	} else {
		tamperedPath := filepath.Join(dir, ".kern", "audit", "audit-"+linked.ID+".json")
		var te g27Entry
		tamperedRaw, err := os.ReadFile(tamperedPath)
		if err != nil {
			t.Fatalf("read linked entry: %v", err)
		}
		if err := json.Unmarshal(tamperedRaw, &te); err != nil {
			t.Fatalf("parse linked entry: %v", err)
		}
		te.AgentID = "evil-agent"
		reMarshaled, err := json.Marshal(te)
		if err != nil {
			t.Fatalf("marshal tampered: %v", err)
		}
		if err := os.WriteFile(tamperedPath, reMarshaled, 0o644); err != nil {
			t.Fatalf("write tampered entry: %v", err)
		}
	}
	tamperedChain := g27ReadChain(t, dir)
	broken := false
	prevHash := ""
	for _, e := range tamperedChain {
		if e.Hash != g27Hash(e, prevHash) {
			broken = true
			break
		}
		prevHash = e.Hash
	}
	if !broken {
		t.Error("tampered entry still verifies — tampering undetected")
	}
}
