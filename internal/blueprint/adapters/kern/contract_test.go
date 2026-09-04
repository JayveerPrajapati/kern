package kern

import (
	"context"
	"strings"
	"testing"
)

// Contract (G14 gate) tests: Blueprint must FAIL CLOSED on the versioned kern
// JSON contract. kern emits {"schema_version":2,"violations":[...]} for
// `guard check --json` and {"schema_version":2,"findings":[...]} for
// `sec --json`. Any missing, wrong, or malformed contract must surface as an
// error (the check layer converts it to StatusError -> final ERROR) — never
// as a silent misparse.
//
// These tests use fakeRunner (architecture_test.go) so they never depend on a
// real kern binary; the runner only passes canned stdout through.

func TestContractGuardValidVersion(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"violations":[]}`, "", 0, nil)}
	violations, _, code, err := client.GuardCheck(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("GuardCheck returned error: %v", err)
	}
	if code != 0 {
		t.Errorf("exitCode = %d, want 0", code)
	}
	if len(violations) != 0 {
		t.Errorf("violations = %d, want 0", len(violations))
	}
}

func TestContractGuardWrongVersion(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":3,"violations":[]}`, "", 0, nil)}
	if _, _, _, err := client.GuardCheck(context.Background(), t.TempDir()); err == nil {
		t.Fatal("GuardCheck returned no error, want contract mismatch error")
	} else if !strings.Contains(err.Error(), "contract") {
		t.Errorf("error = %q, want mention of contract", err.Error())
	}
}

func TestContractGuardMissingVersion(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"violations":[]}`, "", 0, nil)}
	if _, _, _, err := client.GuardCheck(context.Background(), t.TempDir()); err == nil {
		t.Fatal("GuardCheck returned no error, want contract mismatch error")
	} else if !strings.Contains(err.Error(), "contract") {
		t.Errorf("error = %q, want mention of contract", err.Error())
	}
}

func TestContractGuardMalformed(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`not json`, "", 0, nil)}
	if _, _, _, err := client.GuardCheck(context.Background(), t.TempDir()); err == nil {
		t.Fatal("GuardCheck returned no error, want parse error")
	}
}

func TestContractSecValid(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":2,"findings":[]}`, "", 0, nil)}
	findings, _, code, err := client.SecScan(context.Background(), t.TempDir(), ".")
	if err != nil {
		t.Fatalf("SecScan returned error: %v", err)
	}
	if code != 0 {
		t.Errorf("exitCode = %d, want 0", code)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %d, want 0", len(findings))
	}
}

func TestContractSecLegacyArray(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`[]`, "", 0, nil)}
	if _, _, _, err := client.SecScan(context.Background(), t.TempDir(), "."); err == nil {
		t.Fatal("SecScan returned no error, want legacy-array contract error")
	} else if !strings.Contains(err.Error(), "contract") {
		t.Errorf("error = %q, want mention of contract", err.Error())
	}
}

func TestContractSecWrongVersion(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"schema_version":3,"findings":[]}`, "", 0, nil)}
	if _, _, _, err := client.SecScan(context.Background(), t.TempDir(), "."); err == nil {
		t.Fatal("SecScan returned no error, want contract mismatch error")
	} else if !strings.Contains(err.Error(), "contract") {
		t.Errorf("error = %q, want mention of contract", err.Error())
	}
}

func TestContractSecObjectMissingVersion(t *testing.T) {
	client := &KernClient{binaryPath: "kern", runner: fakeRunner(`{"findings":[]}`, "", 0, nil)}
	if _, _, _, err := client.SecScan(context.Background(), t.TempDir(), "."); err == nil {
		t.Fatal("SecScan returned no error, want contract mismatch error")
	} else if !strings.Contains(err.Error(), "contract") {
		t.Errorf("error = %q, want mention of contract", err.Error())
	}
}
