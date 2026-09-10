package main

import (
	"strings"
	"testing"
)

// TestParseFlagsDefaults pins the default flag values (E3 test-first: the
// 300+ line parseFlags god function had zero tests).
func TestParseFlagsDefaults(t *testing.T) {
	f, rest, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags(nil): %v", err)
	}
	if f.days != 7 || f.timeout != 120 || f.depth != -1 || f.commits != 60 {
		t.Errorf("int defaults = days %d timeout %d depth %d commits %d, want 7/120/-1/60",
			f.days, f.timeout, f.depth, f.commits)
	}
	if f.thresholds != "2.0,4.0,6.0,8.0" {
		t.Errorf("thresholds default = %q", f.thresholds)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want empty", rest)
	}
}

// TestParseFlagsScalarAndBoolFlags covers the string/int/bool flag families
// and positional passthrough in one mixed invocation.
func TestParseFlagsScalarAndBoolFlags(t *testing.T) {
	f, rest, err := parseFlags([]string{
		"a.go", "--root", "/repo", "--days", "3", "--json", "--strict",
		"--attach", "log.txt", "b.go", "--bpe", "-1", "-",
	})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.root != "/repo" || f.days != 3 || !f.json || !f.strict || !f.bpe {
		t.Errorf("parsed = root %q days %d json %v strict %v bpe %v", f.root, f.days, f.json, f.strict, f.bpe)
	}
	if f.attach != "log.txt" {
		t.Errorf("attach = %q, want log.txt", f.attach)
	}
	// Positionals keep order; "-1" (negative number) and "-" (stdin) are
	// NOT flags.
	want := []string{"a.go", "b.go", "-1", "-"}
	if len(rest) != len(want) {
		t.Fatalf("rest = %v, want %v", rest, want)
	}
	for i := range want {
		if rest[i] != want[i] {
			t.Fatalf("rest[%d] = %q, want %q", i, rest[i], want[i])
		}
	}
}

// TestParseFlagsErrors pins the fail-closed contract: unknown flags and
// invalid integers are rejected with descriptive errors.
func TestParseFlagsErrors(t *testing.T) {
	if _, _, err := parseFlags([]string{"--bogus"}); err == nil || !strings.Contains(err.Error(), "unknown flag: --bogus") {
		t.Fatalf("unknown flag err = %v, want rejection", err)
	}
	if _, _, err := parseFlags([]string{"-x"}); err == nil || !strings.Contains(err.Error(), "unknown flag: -x") {
		t.Fatalf("short unknown flag err = %v, want rejection", err)
	}
	if _, _, err := parseFlags([]string{"--days", "abc"}); err == nil || !strings.Contains(err.Error(), "--days: invalid integer") {
		t.Fatalf("invalid int err = %v, want rejection", err)
	}
	// Fail-fast: the FIRST invalid value wins; later ones are not evaluated.
	if _, _, err := parseFlags([]string{"--days", "abc", "--commits", "x"}); err == nil || !strings.Contains(err.Error(), "--days: invalid integer") {
		t.Fatalf("first-error contract violated: %v", err)
	}
}

// TestParseFlagsHelpAndMissingValue: --help is a flag; a value flag at the
// end without a value is silently ignored (existing behavior).
func TestParseFlagsHelpAndMissingValue(t *testing.T) {
	f, rest, err := parseFlags([]string{"--help"})
	if err != nil || !f.help {
		t.Fatalf("--help = help %v err %v, want true/nil", f.help, err)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want empty", rest)
	}
	// Trailing value-flag: no panic, no error, flag left defaulted.
	f, _, err = parseFlags([]string{"--root"})
	if err != nil {
		t.Fatalf("trailing --root: %v", err)
	}
	if f.root != "" {
		t.Errorf("root = %q, want empty (no value given)", f.root)
	}
}
