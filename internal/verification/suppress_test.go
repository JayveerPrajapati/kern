package verification

// Tests for the security-finding suppression/triage layer (suppress.go):
// matching semantics (line-specific and file-wide), reason surfacing in the
// rendered output, verdict semantics (unsuppressed findings still fail,
// all-suppressed passes), the optional .kern/verify-suppressions.json user
// file (including malformed input), and a pin that the 16 built-in defaults
// match the CURRENT scanner output so they cannot silently drift.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/secscan"
	"github.com/JayveerPrajapati/kern/internal/verdict"
)

func finding(file string, line int, rule string) secscan.Finding {
	return secscan.Finding{File: file, Line: line, Rule: rule, Severity: "info", Message: rule}
}

// TestSuppressionMatchByLine pins the line-specific key: file:line:rule
// matches exactly, and a differing line, rule, or file does not.
func TestSuppressionMatchByLine(t *testing.T) {
	reg := &SuppressionRegistry{Suppressions: []Suppression{
		{File: "a.go", Line: 5, Rule: "hardcoded-secret", Reason: "fixture constant, not a credential"},
	}}

	cases := []struct {
		name string
		f    secscan.Finding
		want bool
	}{
		{"exact file:line:rule", finding("a.go", 5, "hardcoded-secret"), true},
		{"wrong line", finding("a.go", 6, "hardcoded-secret"), false},
		{"wrong rule", finding("a.go", 5, "sql-injection"), false},
		{"wrong file", finding("b.go", 5, "hardcoded-secret"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reason, ok := reg.Match(c.f)
			if ok != c.want {
				t.Errorf("Match(%s:%d %s) = %v, want %v", c.f.File, c.f.Line, c.f.Rule, ok, c.want)
			}
			if c.want && reason == "" {
				t.Error("a match must carry a non-empty reason")
			}
		})
	}
}

// TestSuppressionMatchByFileWideRule pins the file-wide key: an entry with no
// line matches every finding of that rule in that file, regardless of line.
func TestSuppressionMatchByFileWideRule(t *testing.T) {
	reg := &SuppressionRegistry{Suppressions: []Suppression{
		{File: "sdk.py", Rule: "py-subprocess", Reason: "the SDK's own execution API"},
	}}

	for _, line := range []int{1, 58, 118, 999} {
		if _, ok := reg.Match(finding("sdk.py", line, "py-subprocess")); !ok {
			t.Errorf("file-wide entry must match %s:%d py-subprocess", "sdk.py", line)
		}
	}
	// A different rule on the same file is NOT covered by the file-wide entry.
	if _, ok := reg.Match(finding("sdk.py", 58, "code-eval")); ok {
		t.Error("file-wide entry must not cover other rules on the same file")
	}
}

// TestAllSuppressedPasses: a critical (error) finding covered by a user
// suppression file must NOT fail the security check — all findings
// suppressed means the check passes, with the triage visible.
func TestAllSuppressedPasses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-execution verification in -short mode")
	}
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		// error severity: dynamic SQL built from a variable.
		"sql.go": "package app\nimport \"fmt\"\nfunc f(q string) { db.Query(fmt.Sprintf(\"SELECT * FROM t WHERE id=%s\", q)) }\n",
		// user triage file suppressing exactly that finding.
		".kern/verify-suppressions.json": `{"suppressions":[{"file":"sql.go","line":3,"rule":"sql-injection","reason":"test fixture: constant SQL shape, not user input."}]}`,
	})
	sr := NewEngine(dir).VerifySecurity()
	if sr == nil {
		t.Fatal("nil security result")
	}
	if !sr.OK {
		t.Errorf("all findings suppressed: security check must pass, got OK=%v", sr.OK)
	}
	if sr.Suppressed != 1 {
		t.Errorf("Suppressed = %d, want 1", sr.Suppressed)
	}
	if sr.Critical != 0 {
		t.Errorf("a suppressed critical finding must not count in Critical, got %d", sr.Critical)
	}
	if sr.Count != 1 || sr.Count != len(sr.Findings) {
		t.Errorf("Count = %d, findings = %d, want 1 (suppressed findings stay listed)", sr.Count, len(sr.Findings))
	}
	if len(sr.Findings) != 1 || !sr.Findings[0].Suppressed || sr.Findings[0].SuppressionReason == "" {
		t.Errorf("the suppressed finding must be marked with its reason, got %+v", sr.Findings)
	}
}

// TestSuppressionReasonSurfacesInOutput: the rendered report must show
// suppressed findings explicitly — marked [suppressed] with their reason —
// so triage is visible in both directions.
func TestSuppressionReasonSurfacesInOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-execution verification in -short mode")
	}
	t.Parallel()
	dir := t.TempDir()
	const reason = "test fixture: constant SQL shape, not user input."
	writeTree(t, dir, map[string]string{
		"sql.go":                         "package app\nimport \"fmt\"\nfunc f(q string) { db.Query(fmt.Sprintf(\"SELECT * FROM t WHERE id=%s\", q)) }\n",
		".kern/verify-suppressions.json": `{"suppressions":[{"file":"sql.go","line":3,"rule":"sql-injection","reason":"` + reason + `"}]}`,
	})
	sr := NewEngine(dir).VerifySecurity()
	compact := verdict.RenderCompact(verdict.VerificationResult{Security: sr})
	if !strings.Contains(compact, "[suppressed] sql.go:3") {
		t.Errorf("render must mark the suppressed finding, got:\n%s", compact)
	}
	if !strings.Contains(compact, reason) {
		t.Errorf("render must surface the suppression reason, got:\n%s", compact)
	}
	if !strings.Contains(compact, "(1 suppressed)") {
		t.Errorf("summary line must count suppressed findings, got:\n%s", compact)
	}
}

// TestUnsuppressedFindingsStillFail: findings NOT covered by any suppression
// keep today's semantics — an unsuppressed critical (error) finding still
// fails the security check (no verdict downgrade, no hiding).
func TestUnsuppressedFindingsStillFail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-execution verification in -short mode")
	}
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		// error severity, deliberately NOT suppressed.
		"sql.go": "package app\nimport \"fmt\"\nfunc f(q string) { db.Query(fmt.Sprintf(\"SELECT * FROM t WHERE id=%s\", q)) }\n",
	})
	sr := NewEngine(dir).VerifySecurity()
	if sr.OK {
		t.Error("an unsuppressed critical finding must fail the security check (OK=false)")
	}
	if sr.Critical != 1 {
		t.Errorf("Critical = %d, want 1", sr.Critical)
	}
	if sr.Suppressed != 0 {
		t.Errorf("Suppressed = %d, want 0", sr.Suppressed)
	}
}

// TestUserSuppressionFileFileWide: a user suppression entry without a line
// suppresses every finding of that rule in that file.
func TestUserSuppressionFileFileWide(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-execution verification in -short mode")
	}
	t.Parallel()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		// Two weak-crypto (warning) findings on different lines.
		"crypto.go": "package app\nimport \"crypto/md5\"\nvar a = md5.New()\nvar b = md5.Sum([]byte(\"x\"))\n",
		// File-wide suppression: no "line" key.
		".kern/verify-suppressions.json": `{"suppressions":[{"file":"crypto.go","rule":"weak-crypto","reason":"test fixture: deprecated crypto in a fixture, not production code."}]}`,
	})
	sr := NewEngine(dir).VerifySecurity()
	if !sr.OK {
		t.Errorf("file-wide suppression must clear the check, got OK=%v", sr.OK)
	}
	if sr.Suppressed != 2 {
		t.Errorf("Suppressed = %d, want 2 (both weak-crypto findings)", sr.Suppressed)
	}
	if sr.High != 0 {
		t.Errorf("suppressed warnings must not count in High, got %d", sr.High)
	}
	for _, f := range sr.Findings {
		if !f.Suppressed || f.SuppressionReason == "" {
			t.Errorf("finding %s:%d must be marked suppressed with a reason: %+v", f.File, f.Line, f)
		}
	}
}

// R8: the user suppression ledger is operator triage policy, so malformed
// JSON and incomplete entries fail closed instead of being silently ignored.
// A missing file remains the normal built-ins-only path.
func TestLoadSuppressionRegistryFailClosed(t *testing.T) {
	root := t.TempDir()

	reg, err := LoadSuppressionRegistry(root)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if len(reg.Suppressions) != len(defaultSuppressions) {
		t.Fatalf("missing file changed built-in count: got %d, want %d", len(reg.Suppressions), len(defaultSuppressions))
	}

	for name, content := range map[string]string{
		"malformed":      `{not valid json`,
		"missing-file":   `{"suppressions":[{"line":3,"rule":"sql-injection","reason":"missing file"}]}`,
		"missing-rule":   `{"suppressions":[{"file":"sql.go","line":3,"reason":"missing rule"}]}`,
		"missing-reason": `{"suppressions":[{"file":"sql.go","line":3,"rule":"sql-injection"}]}`,
		"blank-reason":   `{"suppressions":[{"file":"sql.go","line":3,"rule":"sql-injection","reason":"   "}]}`,
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".kern"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".kern", "verify-suppressions.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSuppressionRegistry(dir); err == nil {
			t.Errorf("%s suppression ledger was accepted: %s", name, content)
		}
	}

	dir := t.TempDir()
	valid := `{"suppressions":[{"file":"sql.go","line":3,"rule":"sql-injection","reason":"test fixture: constant SQL shape, not user input."}]}`
	if err := os.MkdirAll(filepath.Join(dir, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".kern", "verify-suppressions.json"), []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err = LoadSuppressionRegistry(dir)
	if err != nil {
		t.Fatalf("valid user suppression rejected: %v", err)
	}
	if len(reg.Suppressions) != len(defaultSuppressions)+1 {
		t.Fatalf("valid user suppression not merged: got %d entries, want %d", len(reg.Suppressions), len(defaultSuppressions)+1)
	}
}

// R8: VerifySecurity propagates loader failures instead of silently treating
// malformed operator triage as absent.
func TestVerifySecurityFailsClosedOnMalformedSuppressions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-execution verification in -short mode")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"sql.go":                         "package app\nimport \"fmt\"\nfunc f(q string) { db.Query(fmt.Sprintf(\"SELECT * FROM t WHERE id=%s\", q)) }\n",
		".kern/verify-suppressions.json": `{not valid json`,
	})
	sr := NewEngine(dir).VerifySecurity()
	if sr == nil {
		t.Fatal("nil security result")
	}
	if sr.OK {
		t.Error("malformed suppression ledger must fail closed")
	}
	if !strings.Contains(sr.Error, "malformed") {
		t.Errorf("error must identify malformed suppression ledger, got %q", sr.Error)
	}
}

// TestBuiltinSuppressionsMatchCurrentScan pins the exact built-in defaults
// against the CURRENT scanner output on kern's own repository, so the
// defaults cannot silently drift: every built-in entry must still match a
// real finding at its file:line:rule (line-specific) or at least one finding
// of its rule in the file (file-wide), and every current finding on those
// files must be covered by a built-in entry.
func TestBuiltinSuppressionsMatchCurrentScan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-execution verification in -short mode")
	}
	// Repo root relative to this package's directory (go test runs with cwd =
	// the package dir). Skip when the repo tree is absent (e.g. the package
	// was vendored elsewhere).
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("kern repository not present here; skipping built-in drift pin")
	}

	reg := &SuppressionRegistry{Suppressions: defaultSuppressions, resolver: newFuncResolver(root)}
	seen := map[string]bool{}
	for _, s := range defaultSuppressions {
		key := fmt.Sprintf("%s:%d:%s", s.File, s.Line, s.Rule)
		if s.Func != "" {
			key = fmt.Sprintf("%s:func:%s:%s", s.File, s.Func, s.Rule)
		}
		if seen[key] {
			t.Fatalf("duplicate built-in suppression %s", key)
		}
		seen[key] = true
		if s.Reason == "" {
			t.Errorf("built-in suppression %s must carry a reason", key)
		}
	}

	// Direction 1: every built-in entry matches a finding the scanner
	// currently produces — for a line-specific entry (Line > 0) at that exact
	// file:line:rule; for a file-wide entry (Line == 0) at least one finding
	// of its rule must exist in the file.
	// Direction 2: every current finding on those files is covered by a
	// built-in entry (a new finding = drift to fix).
	for _, s := range defaultSuppressions {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.File)))
		if err != nil {
			t.Fatalf("read %s: %v", s.File, err)
		}
		var findings []secscan.Finding
		if strings.HasSuffix(s.File, ".py") {
			findings = secscan.ScanPythonFile(s.File, src)
		} else {
			findings = secscan.ScanFile(s.File, src)
		}
		matchedFinding := false
		for _, f := range findings {
			grounding := false
			if s.Func != "" {
				// func-scoped key: any finding of Rule inside the named function
				// grounds the entry — survives line drift within the function.
				grounding = f.Rule == s.Rule && (s.Label == "" || s.Label == f.Label) && reg.resolver.funcFor(f.File, f.Line) == s.Func
			} else if s.Line == 0 {
				// file-wide key: any finding of Rule in File grounds the entry
				// (narrowed to the entry's label when it sets one).
				grounding = f.Rule == s.Rule && (s.Label == "" || s.Label == f.Label)
			} else {
				grounding = f.Line == s.Line && f.Rule == s.Rule
			}
			if grounding {
				matchedFinding = true
				if reason, ok := reg.Match(f); !ok || reason != s.Reason {
					t.Errorf("built-in entry %s must match its own finding with its own reason", fmt.Sprintf("%s:%d:%s", s.File, s.Line, s.Rule))
				}
			}
			if _, ok := reg.Match(f); !ok {
				t.Errorf("CURRENT scan finding %s:%d [%s] on a default-suppressed file has NO built-in suppression — add it or fix the drift", f.File, f.Line, f.Rule)
			}
		}
		if !matchedFinding {
			t.Errorf("built-in suppression %s no longer matches the scanner output — the defaults drifted (site stopped producing the finding?)", s.File+":"+fmt.Sprint(s.Line)+":"+s.Rule)
		}
	}
}

// TestBuiltinCountIsSixteen pins the default list size so adding/removing a
// triage entry is a deliberate, test-visible act. 16 -> 14 on 2026-10-18:
// the two internal/blueprint/adapters/kern unsafe-exec entries left with the
// blueprint family (P4 deletion). 14 -> 13 on 2026-10-19: the ci.yml:198
// hardcoded-secret entry went stale when the IPV6 rule stopped matching the
// single-char ::a group of the GitHub Actions ::error:: workflow-command
// prefix (QA 2026-10-19 — the IPV6 rule now requires ≥2-char hex groups).
// 13 -> 16 on 2026-10-19: triage pass adding the evaluate/bench fixture
// unsafe-http entry, the calibrate rev-list unsafe-exec entry and the
// commitmsg changelog wrapper unsafe-exec entry (all line-specific).
// 16 -> 15 on 2026-10-19: the commitmsg changelog entry was removed with the
// `kern commitmsg` command (the changelog draft feature was deleted; only
// `kern commit` survives in internal/commitmsg). 15 -> 16 on 2026-10-19:
// added the surviving kern commit git wrappers (commitmsgcmd.go:225) the
// staged scan re-flagged once the changelog entry was gone. 16 -> 83 on
// 2026-10-21: the full dogfood triage pass — the remaining 67 unsuppressed
// unsafe-exec/unsafe-http findings on kern's own tree, triaged class by
// class (fixed-argv git plumbing, scanner probes, operator-configured
// LLM/SDK/webhook endpoints, the governed sandbox/exec machinery). After
// this, an UNSUPPRESSED security finding on kern's own tree is a real
// signal again, not noise. 83 -> 84 on 2026-10-23: the apply campaign's
// runGo (internal/apply/apply.go:233) — `go test` with internally derived
// package paths, same class as the refactor/mutation compile steps; the
// MCP boundary carries the documented engine-default carve-out. 84 -> 83 on
// 2026-10-24: the orphaned internal/app/surface_drift.go suppression was
// removed when commit 2f5cfeae deleted the file — the stale entry could
// never match, so deleting it restores the drift pin to the verified set.
// 83 -> 82 on 2026-10-24: the two worktree.go line pins (Diff's two fixed-argv
// git diff forms at :121/:162) merged into one Func-scoped entry.
func TestBuiltinCountIsSixteen(t *testing.T) {
	if len(defaultSuppressions) != 82 {
		t.Errorf("len(defaultSuppressions) = %d, want 82 (the verified finding set on kern's repo)", len(defaultSuppressions))
	}
}
