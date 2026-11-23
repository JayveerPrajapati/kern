package highlevel

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestVerifyMapsFailedTestsToIndexedSymbols(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	setVerifyExecEnv(t)
	dir := verifyTiersFixture(t)
	out, err := Verify(context.Background(), verifyHooks(dir), map[string]any{
		"root":  dir,
		"types": "build,test",
		"full":  true,
	})
	if err != nil {
		t.Fatalf("Verify(full=true): %v", err)
	}
	want := "TestFullOnlyFailure -> b.TestFullOnlyFailure — b/b_test.go:5"
	if !strings.Contains(out, want) {
		t.Errorf("full failure output missing indexed anchor %q:\n%s", want, out)
	}
}
