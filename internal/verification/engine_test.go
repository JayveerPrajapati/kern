package verification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// repoRoot returns the repository root that the verification package lives in
// (the parent of this package's directory).
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd)
}

// writeTree writes the given relative-path->content map under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

func trunc(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

// verifyFixture writes a tiny standalone Go module (with a passing test) into
// a temp dir and returns its root. Running the build/test verification against
// this trivial module completes in seconds — running it against the whole kern
// repository previously hung for minutes (600s) spawning runaway go processes.
func verifyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module gatefixture\n\ngo 1.20\n",
		"main.go": `package main

func helper() string { return "h" }

func main() { println(helper()) }
`,
		"main_test.go": `package main

import "testing"

func TestHelper(t *testing.T) {
	if helper() != "h" {
		t.Fail()
	}
}
`,
	})
	return dir
}

// TestVerdictEnum asserts the three extended verdicts exist with the right
// string values, are distinct from the original PASS/FAIL/WARN, and are
// classified as non-fail by a switch that handles them explicitly.
func TestVerdictEnum(t *testing.T) {
	extended := map[Verdict]string{
		VerdictPassWithWarning: "PASS_WITH_WARNING",
		VerdictBlocked:         "BLOCKED",
		VerdictNotRun:          "NOT_RUN",
	}
	// Each new verdict must have the correct string value.
	for v, want := range extended {
		if string(v) != want {
			t.Errorf("verdict %v: expected %q, got %q", v, want, string(v))
		}
	}
	// New verdicts must be distinct from the legacy PASS/FAIL/WARN.
	legacy := map[Verdict]bool{
		VerdictPass: true,
		VerdictFail: true,
		VerdictWarn: true,
	}
	for v := range extended {
		if legacy[v] {
			t.Errorf("verdict %v collides with a legacy verdict", v)
		}
	}
	// A switch on Verdict that includes the new values must compile and
	// classify them as not-fail.
	for v := range extended {
		isFail := false
		switch v {
		case VerdictPass, VerdictPassWithWarning, VerdictBlocked, VerdictNotRun, VerdictWarn:
			isFail = false
		case VerdictFail:
			isFail = true
		}
		if isFail {
			t.Errorf("verdict %v should not be treated as a failure", v)
		}
	}
}

// TestVerifyBuild runs the build verification against a tiny fixture module
// and asserts a passing build. Scoped to the fixture so it completes in
// seconds instead of building the whole kern repo. `go build` is silent on
// success, so the fixture's build output may legitimately be empty.
func TestVerifyBuild(t *testing.T) {
	e := NewEngine(verifyFixture(t))
	br := e.VerifyBuild()
	if br == nil {
		t.Fatal("nil build result")
	}
	if !br.OK {
		t.Errorf("build should pass on the fixture: %s", trunc(br.Output))
	}
	if br.Duration == 0 {
		t.Error("build should report a duration")
	}
}

// TestVerifyTests runs the test verification (go test ./...) against a tiny
// fixture module and asserts the package is exercised and no failures are
// reported. Scoped to the fixture so it completes in seconds.
func TestVerifyTests(t *testing.T) {
	e := NewEngine(verifyFixture(t))
	tr := e.VerifyTests()
	if tr == nil {
		t.Fatal("nil test result")
	}
	if !tr.OK {
		t.Errorf("tests should pass: %s", trunc(tr.Output))
	}
	if !strings.Contains(tr.Output, "gatefixture") {
		t.Error("output should reference the fixture package")
	}
	if tr.Failed != 0 {
		t.Errorf("expected 0 test failures, got %d", tr.Failed)
	}
	if tr.Passed == 0 {
		t.Error("expected at least one passing test")
	}
	if tr.Passed+tr.Failed+tr.Skipped < 0 {
		t.Error("counts are negative")
	}
}

// TestVerifySecurity scans a small fixture containing a weak-crypto use and
// asserts the finding is detected deterministically.
func TestVerifySecurity(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"app.go": "package app\nimport \"crypto/md5\"\nvar h = md5.New()\n",
	})
	e := NewEngine(dir)
	sr := e.VerifySecurity()
	if sr == nil {
		t.Fatal("nil security result")
	}
	if !sr.OK {
		t.Error("security scan should run OK")
	}
	if sr.Count == 0 {
		t.Error("expected at least one weak-crypto finding, got 0")
	}
	if len(sr.Findings) != sr.Count {
		t.Errorf("findings length %d != count %d", len(sr.Findings), sr.Count)
	}
	found := false
	for _, f := range sr.Findings {
		if f.Rule == "weak-crypto" {
			found = true
		}
	}
	if !found {
		t.Error("expected a weak-crypto finding")
	}
}

// TestVerifySecuritySeverityMapping verifies that internal/sec's
// error/warning/info severities are mapped into the SecurityResult risk
// ladder (error→Critical, warning→High, info→Low) and that a critical finding
// makes the security check block (OK=false). Previously only "critical"/"high"
// were counted, so every severity read 0 and findings never blocked.
func TestVerifySecuritySeverityMapping(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		// error severity: dynamic SQL built from a variable.
		"sql.go": `package app
import "fmt"
func f(id string) { db.Query(fmt.Sprintf("SELECT * FROM t WHERE id=%s", id)) }
`,
		// warning severity: weak crypto.
		"crypto.go": "package app\nimport \"crypto/md5\"\nvar h = md5.New()\n",
		// info severity: dynamic code evaluation.
		"eval.go": "package app\nvar x = eval(\"1+1\")\n",
	})
	e := NewEngine(dir)
	sr := e.VerifySecurity()
	if sr == nil {
		t.Fatal("nil security result")
	}
	if sr.Critical == 0 {
		t.Errorf("expected error findings mapped to Critical, got 0")
	}
	if sr.High == 0 {
		t.Errorf("expected warning findings mapped to High, got 0")
	}
	if sr.Low == 0 {
		t.Errorf("expected info findings mapped to Low, got 0")
	}
	if sr.Count != len(sr.Findings) {
		t.Errorf("count %d != findings %d", sr.Count, len(sr.Findings))
	}
	if sr.OK {
		t.Error("a critical finding must make the security check block (OK=false)")
	}
}

// TestVerifySecurityCriticalBlocksVerdict asserts that a critical security
// finding produces a FAIL verdict, while a warning-only scan produces WARN.
func TestVerifySecurityCriticalBlocksVerdict(t *testing.T) {
	// Critical (error severity) fixture → FAIL.
	critDir := t.TempDir()
	writeTree(t, critDir, map[string]string{
		"sql.go": "package app\nimport \"fmt\"\nfunc f(q string) { db.Query(fmt.Sprintf(\"SELECT * FROM t WHERE id=%s\", q)) }\n",
	})
	crit := NewEngine(critDir).Verify([]string{"security"})
	if crit.Verdict != VerdictFail {
		t.Errorf("critical security finding should produce VerdictFail, got %q", crit.Verdict)
	}

	// Warning-only fixture → WARN (non-blocking).
	warnDir := t.TempDir()
	writeTree(t, warnDir, map[string]string{
		"crypto.go": "package app\nimport \"crypto/md5\"\nvar h = md5.New()\n",
	})
	warn := NewEngine(warnDir).Verify([]string{"security"})
	if warn.Verdict != VerdictWarn {
		t.Errorf("warning-only security scan should produce VerdictWarn, got %q", warn.Verdict)
	}
}

// and asserts the violation is detected.
// TestVerifySecurityEmitsEvidenceClaims verifies the evidence factory
// (evidence.FromSecurityFinding) is invoked through the production security
// path: a security scan must emit an evidence-backed Claim into the result.
func TestVerifySecurityEmitsEvidenceClaim(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"sql.go": "package app\nimport \"fmt\"\nfunc f(q string) { db.Query(fmt.Sprintf(\"SELECT * FROM t WHERE id=%s\", q)) }\n",
	})
	res := NewEngine(dir).Verify([]string{"security"})
	if res.Security == nil {
		t.Fatal("nil security result")
	}
	if len(res.Security.Claims) == 0 {
		t.Fatal("expected at least one security claim emitted via the evidence factory")
	}
	if len(res.Claims) == 0 {
		t.Fatal("expected aggregated claims on the verification result")
	}
	found := false
	for _, c := range res.Claims {
		if c.Type == domain.ClaimFact && strings.HasPrefix(c.Provenance, "sec:") {
			found = true
		}
	}
	if !found {
		t.Error("expected a FromSecurityFinding claim (provenance sec:...) in the aggregated result")
	}
}

// TestVerifyDependencyModuleMissingModule verifies G4: a real dependency check
// surfaces a missing-module finding (fail-closed, never a fabricated PASS).
func TestVerifyDependencyModuleMissingModule(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":  "module depfixture\n\ngo 1.20\n",
		"main.go": "package main\nimport \"example.com/missing/lib\"\nvar _ = lib.X\n",
	})
	dr := NewEngine(dir).VerifyDependency("")
	if dr == nil {
		t.Fatal("nil dependency result")
	}
	if dr.OK {
		t.Error("a missing module must fail the dependency check (fail-closed)")
	}
	if len(dr.Findings) == 0 {
		t.Fatal("expected a missing-module finding")
	}
	found := false
	for _, f := range dr.Findings {
		if strings.Contains(f, "missing module for import example.com/missing/lib") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing-module finding, got %v", dr.Findings)
	}
}

// TestVerifyDependencyModuleDuplicateRequire verifies G4 detects version
// duplication (a module required more than once).
func TestVerifyDependencyModuleDuplicateRequire(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":  "module dupfixture\n\ngo 1.20\n\nrequire (\n\texample.com/a v1.0.0\n\texample.com/a v1.1.0\n)\n",
		"main.go": "package main\nfunc main() {}\n",
	})
	dr := NewEngine(dir).VerifyDependency("")
	if dr == nil {
		t.Fatal("nil dependency result")
	}
	if dr.OK {
		t.Error("a duplicated require must fail the dependency check")
	}
	if len(dr.Findings) == 0 {
		t.Fatal("expected a duplicate-require finding")
	}
}

// TestVerifyDependencyModuleClean verifies a consistent module passes G4 with
// no findings.
func TestVerifyDependencyModuleClean(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":  "module cleanfixture\n\ngo 1.20\n",
		"main.go": "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hi\") }\n",
	})
	dr := NewEngine(dir).VerifyDependency("")
	if dr == nil {
		t.Fatal("nil dependency result")
	}
	if !dr.OK {
		t.Errorf("clean module should pass, got findings %v", dr.Findings)
	}
	if len(dr.Findings) != 0 {
		t.Errorf("expected no findings for a clean module, got %v", dr.Findings)
	}
}

// TestVerifyDependencyModuleFailClosedOnNoGomod verifies G4 stays fail-closed:
// a project without a readable go.mod is NOT reported as a PASS.
func TestVerifyDependencyModuleFailClosedOnNoGomod(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"main.go": "package main\nfunc main() {}\n",
	})
	dr := NewEngine(dir).VerifyDependency("")
	if dr == nil {
		t.Fatal("nil dependency result")
	}
	if dr.OK {
		t.Error("a project without a readable go.mod must not fabricate a PASS")
	}
	if len(dr.Findings) == 0 {
		t.Error("expected a fail-closed finding when go.mod is unreadable")
	}
}

// TestVerifyArchitecture creates a temp boundary rule forbidding client->lib
// and asserts the violation is detected.
func TestVerifyArchitecture(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"lib/lib.go": `package lib

func Public() string { return "x" }
`,
		"client/client.go": `package client

import "lib"

func Caller() string { return lib.Public() }
`,
	})
	writeTree(t, dir, map[string]string{
		".kern/boundaries.json": `{"rules":[{"from":"client","to":"lib","action":"forbid"}]}`,
	})
	e := NewEngine(dir)
	ar := e.VerifyArchitecture()
	if ar == nil {
		t.Fatal("nil architecture result")
	}
	if ar.OK {
		t.Error("expected a client->lib boundary violation, got OK")
	}
	if len(ar.Violations) == 0 {
		t.Error("expected at least one violation entry")
	}
}

// TestVerifyDependency checks the intelligence graph for a real symbol in the
// fixture and asserts node/edge counts are populated. Scoped to the fixture so
// it does not re-index the whole kern repository.
func TestVerifyDependency(t *testing.T) {
	e := NewEngine(verifyFixture(t))
	dr := e.VerifyDependency("helper")
	if dr == nil {
		t.Fatal("nil dependency result")
	}
	if !dr.OK {
		t.Error("dependency OK should be true for the real symbol helper")
	}
	if dr.GraphNodes == 0 {
		t.Error("expected a non-zero node count")
	}
	if dr.GraphEdges == 0 {
		t.Error("expected a non-zero edge count")
	}
}

// TestToEvidence asserts the evidence fields produced for a build result.
func TestToEvidence(t *testing.T) {
	now := time.Now()
	res := &VerificationResult{
		Verdict:     VerdictPass,
		Summary:     "build: PASS",
		GeneratedAt: now,
		Build:       &BuildResult{OK: true},
	}
	ev := ToEvidence(res.Verdict, res)
	if ev.Source != "verification" {
		t.Errorf("expected source verification, got %q", ev.Source)
	}
	if ev.Type != domain.EvidenceBuild {
		t.Errorf("expected EvidenceBuild, got %q", ev.Type)
	}
	if ev.Digest == "" {
		t.Error("expected a non-empty digest")
	}
	if ev.Content == "" {
		t.Error("expected a non-empty content")
	}
}

// TestAnnotate asserts the claim types and counts produced by Annotate.
func TestAnnotate(t *testing.T) {
	now := time.Now()
	res := &VerificationResult{
		Verdict:     VerdictWarn,
		Summary:     "security, build: WARN",
		GeneratedAt: now,
		Build:       &BuildResult{OK: true},
		Security:    &SecurityResult{Count: 2, Critical: 0, High: 1, OK: true},
	}
	claims := Annotate(res)
	// build + security facts + inference = 3
	if len(claims) != 3 {
		t.Fatalf("expected 3 claims, got %d", len(claims))
	}
	facts := 0
	for _, c := range claims {
		if c.Type == domain.ClaimFact {
			facts++
		}
		if c.Confidence != 1.0 {
			t.Errorf("claim confidence should be 1.0, got %v", c.Confidence)
		}
	}
	if facts != 2 {
		t.Errorf("expected 2 FACT claims, got %d", facts)
	}
	last := claims[len(claims)-1]
	if last.Type != domain.ClaimInference {
		t.Errorf("expected final claim to be an inference, got %q", last.Type)
	}
	if !strings.Contains(last.Statement, string(res.Verdict)) {
		t.Errorf("inference should mention the verdict: %q", last.Statement)
	}
}

// TestVerifyStaticAnalysis runs Verify with ["static-analysis"] on the fixture
// module and asserts StaticAnalysis is non-nil and OK (the fixture has no vet
// issues).
func TestVerifyStaticAnalysis(t *testing.T) {
	res := NewEngine(verifyFixture(t)).Verify([]string{"static-analysis"})
	if res.StaticAnalysis == nil {
		t.Fatal("nil static analysis result")
	}
	if !res.StaticAnalysis.OK {
		t.Errorf("static analysis should pass on the clean fixture: %s", trunc(res.StaticAnalysis.Output))
	}
	if res.StaticAnalysis.Tool != "go vet" {
		t.Errorf("expected tool go vet, got %q", res.StaticAnalysis.Tool)
	}
	if len(res.StaticAnalysis.Findings) != 0 {
		t.Errorf("expected no findings on the clean fixture, got %v", res.StaticAnalysis.Findings)
	}
	if res.Verdict == VerdictFail {
		t.Error("clean static analysis must not fail the verdict")
	}
}

// TestVerifyE2E runs Verify with ["e2e"] on a fixture without e2e-tagged
// tests; the result must be nil (not run) and the engine must not panic.
func TestVerifyE2E(t *testing.T) {
	res := NewEngine(verifyFixture(t)).Verify([]string{"e2e"})
	if res.E2ETests != nil {
		t.Error("expected nil E2ETests when no e2e-tagged tests are detected")
	}
	if res.Verdict == VerdictFail {
		t.Error("absent E2E coverage must not fail the verdict")
	}
}

// TestVerifyE2EPresent runs Verify with ["e2e"] on a fixture carrying an
// e2e-tagged test and asserts the result is populated and passing.
func TestVerifyE2ERun(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module e2efixture\n\ngo 1.20\n",
		"e2e_test.go": `package main

import "testing"

func TestEndToEnd(t *testing.T) {}
`,
	})
	res := NewEngine(dir).Verify([]string{"e2e"})
	if res.E2ETests == nil {
		t.Fatal("expected E2ETests to run when an e2e-tagged test exists")
	}
	if !res.E2ETests.OK {
		t.Errorf("e2e should pass: %s", trunc(res.E2ETests.Output))
	}
	if res.E2ETests.Passed == 0 {
		t.Errorf("expected at least one passing e2e test, got %d", res.E2ETests.Passed)
	}
}

// TestVerifyPerformanceNilWhenNoBenchmarks verifies that Verify(["performance"])
// returns a nil Performance when the fixture has no benchmarks ("where
// available").
func TestVerifyPerformanceNilWhenNoBenchmarks(t *testing.T) {
	res := NewEngine(verifyFixture(t)).Verify([]string{"performance"})
	if res.Performance != nil {
		t.Error("expected nil Performance when the fixture has no benchmarks")
	}
	if res.Verdict == VerdictFail {
		t.Error("advisory performance must not fail the verdict when absent")
	}
}

// TestVerifyPerformanceRuns verifies benchmark parsing against a fixture that
// declares a benchmark; Performance must be populated and advisory (a failed
// bench run does not fail the verdict).
func TestVerifyPerformanceRuns(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod": "module benchfixture\n\ngo 1.20\n",
		"bench_test.go": `package benchfixture

import "testing"

func BenchmarkSum(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = 1 + 1
	}
}
`,
	})
	res := NewEngine(dir).Verify([]string{"performance"})
	if res.Performance == nil {
		t.Fatal("expected Performance to be populated when benchmarks exist")
	}
	if len(res.Performance.Benchmarks) == 0 {
		t.Error("expected at least one parsed benchmark result")
	}
	if res.Verdict == VerdictFail {
		t.Error("advisory performance must not fail the verdict")
	}
}
