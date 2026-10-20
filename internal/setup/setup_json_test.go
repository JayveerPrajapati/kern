package setup

import (
	"encoding/json"
	"os"
	"testing"
)

// setup_json_test.go — `kern setup --json`: the machine-readable output must
// be exactly one valid JSON document covering the check inventory and the
// dry-run would-write preview, with zero writes in dry-run mode.

func TestSetupJSONCheckOutput(t *testing.T) {
	removeTestHome(t)
	root := t.TempDir()
	var code int
	out := captureSetupOut(t, func() { code = RunSetup([]string{"--check", "--json", "--root", root}) })
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	var got setupJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--check --json output is not valid JSON: %v\n%s", err, out)
	}
	if got.Scope != "check (read-only inventory)" {
		t.Errorf("scope = %q, want the check scope", got.Scope)
	}
	if len(got.Statuses) == 0 {
		t.Error("--check --json produced no status rows")
	}
	if got.Summary == "" {
		t.Error("--check --json missing summary line")
	}
	if got.Detected == nil {
		t.Error("--check --json missing detected list")
	}
}

func TestSetupJSONDryRunWritesNothing(t *testing.T) {
	removeTestHome(t)
	root := t.TempDir()
	var code int
	out := captureSetupOut(t, func() { code = RunSetup([]string{"--dry-run", "--json", "--root", root}) })
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	var got setupJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--dry-run --json output is not valid JSON: %v\n%s", err, out)
	}
	if !got.DryRun {
		t.Error("dryRun flag not set in JSON output")
	}
	if entries, _ := os.ReadDir(root); len(entries) > 0 {
		t.Errorf("--dry-run --json must not write anything; root now has %d entries", len(entries))
	}
}
