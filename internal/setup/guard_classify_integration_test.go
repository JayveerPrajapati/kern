package setup

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestKernGuardClassifiesCompoundRead pins the shell hook's R1 dependency:
// the CURRENT repository binary must classify the exact hook-test command as
// a Tier-1 serve. This isolates classifier drift from hook JSON parsing.
func TestKernGuardClassifiesCompoundRead(t *testing.T) {
	bin := buildCurrentKern(t)
	cmd := exec.Command(bin, "guard", "classify", "--json", "git status && cat guard_test.go")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("guard classify: %v\n%s", err, out)
	}
	t.Logf("classifier output: %s", strings.TrimSpace(string(out)))
	var got struct {
		Verdict string `json:"verdict"`
		Tier    int    `json:"tier"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode classifier JSON %q: %v", out, err)
	}
	if got.Verdict != "serve" || got.Tier != 1 {
		t.Fatalf("classifier = %+v, want serve tier 1", got)
	}
}
