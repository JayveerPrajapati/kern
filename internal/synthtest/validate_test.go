package synthtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixtureModule writes a tiny Go module (go.mod + one source file) so
// the generated test can be validated with the real go toolchain.
func writeFixtureModule(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fixture\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mathutil.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSynthesizeApplyValidatesAndRefusesRuntimeFailure pins M5: a generated
// test that compiles (vet OK) but fails at runtime must NOT be applied. The
// zero-value boundary case of At(s, i) = s[i] panics on s="", so the
// generated test fails; the apply must be refused with the failure output in
// the message, and no test file may be left on disk.
func TestSynthesizeApplyValidatesAndRefusesRuntimeFailure(t *testing.T) {
	dir := writeFixtureModule(t, `package fixture

// At returns the i-th byte of s. It panics when i is out of range.
func At(s string, i int) byte { return s[i] }
`)
	res, err := Synthesize(Request{
		Target: "At",
		File:   "mathutil.go",
		Root:   dir,
		Apply:  true,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if res.Applied {
		t.Fatalf("runtime-failing generated test was applied: %+v", res)
	}
	if !strings.Contains(res.Message, "FAILED at runtime") {
		t.Fatalf("refusal message must explain the runtime failure, got: %s", res.Message)
	}
	if _, serr := os.Stat(filepath.Join(dir, "mathutil_test.go")); serr == nil {
		t.Fatalf("test file left on disk after refused apply:\n%s", res.Message)
	}
	// The message must carry the toolchain's actual failure output.
	if !strings.Contains(res.Message, "panic") && !strings.Contains(res.Message, "FAIL") {
		t.Fatalf("refusal message must include the failure output, got: %s", res.Message)
	}
}

// TestSynthesizeApplyValidatesAndAppliesPassingTest pins the happy path: a
// generated test that passes is applied and left on disk.
func TestSynthesizeApplyValidatesAndAppliesPassingTest(t *testing.T) {
	dir := writeFixtureModule(t, `package fixture

// Add returns the sum of a and b.
func Add(a, b int) int { return a + b }
`)
	res, err := Synthesize(Request{
		Target: "Add",
		File:   "mathutil.go",
		Root:   dir,
		Apply:  true,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if !res.Applied {
		t.Fatalf("passing generated test not applied: %+v", res)
	}
	if res.Message != "" {
		t.Fatalf("unexpected message on successful apply: %s", res.Message)
	}
	content, serr := os.ReadFile(filepath.Join(dir, "mathutil_test.go"))
	if serr != nil {
		t.Fatalf("applied test file missing: %v", serr)
	}
	if !strings.Contains(string(content), "func TestAdd(t *testing.T)") {
		t.Fatalf("applied test file missing TestAdd:\n%s", content)
	}
}

// TestSynthesizeApplyRefusalRestoresExistingTestFile pins the rollback path:
// when the apply is refused, a PRE-EXISTING test file is restored to its
// original content, not deleted.
func TestSynthesizeApplyRefusalRestoresExistingTestFile(t *testing.T) {
	dir := writeFixtureModule(t, `package fixture

// At returns the i-th byte of s. It panics when i is out of range.
func At(s string, i int) byte { return s[i] }
`)
	existing := "package fixture\n\nfunc TestExisting(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "mathutil_test.go"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Synthesize(Request{
		Target: "At",
		File:   "mathutil.go",
		Root:   dir,
		Apply:  true,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if res.Applied {
		t.Fatalf("runtime-failing generated test was applied: %+v", res)
	}
	content, serr := os.ReadFile(filepath.Join(dir, "mathutil_test.go"))
	if serr != nil {
		t.Fatalf("existing test file deleted by rollback: %v", serr)
	}
	if string(content) != existing {
		t.Fatalf("existing test file not restored:\n got: %s\nwant: %s", content, existing)
	}
}
