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

// TestParseFlagsInlineEqualsForms pins the unified parser's --flag=value
// support (the stdlib flag package forms the migrated FlagSets accepted):
// value flags take both --flag value and --flag=value; bool flags take
// --flag, --flag=true and --flag=false. Representative migrated flags are
// used (--pipeline/--template strings, --auto-gap/--sign/--apply bools,
// single-dash -k).
func TestParseFlagsInlineEqualsForms(t *testing.T) {
	// String flag: --flag value AND --flag=value (last wins).
	f, rest, err := parseFlags([]string{"--root", "/a", "--root=/b", "pos"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.root != "/b" {
		t.Errorf("root = %q, want /b (last --root=/b wins)", f.root)
	}
	if len(rest) != 1 || rest[0] != "pos" {
		t.Errorf("rest = %v, want [pos]", rest)
	}
	// Migrated FlagSet string flags, both forms, including single-dash -k.
	f, _, err = parseFlags([]string{"--template", "t1", "--pipeline={\"a\":1}", "-k=5", "--agent", "a1"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.template != "t1" || f.pipeline != `{"a":1}` || f.k != "5" || f.agent != "a1" {
		t.Errorf("migrated strings = template %q pipeline %q k %q agent %q", f.template, f.pipeline, f.k, f.agent)
	}
	// Bool flag with and without =value.
	f, _, err = parseFlags([]string{"--json"})
	if err != nil || !f.json {
		t.Fatalf("--json = %v err %v, want true/nil", f.json, err)
	}
	f, _, err = parseFlags([]string{"--json=true"})
	if err != nil || !f.json {
		t.Fatalf("--json=true = %v err %v, want true/nil", f.json, err)
	}
	f, _, err = parseFlags([]string{"--json=false"})
	if err != nil || f.json {
		t.Fatalf("--json=false = %v err %v, want false/nil", f.json, err)
	}
	// Migrated FlagSet bools, mixed forms.
	f, _, err = parseFlags([]string{"--auto-gap", "--sign=true", "--apply=false"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !f.autoGap || !f.sign || f.apply {
		t.Errorf("bools = autoGap %v sign %v apply %v, want true/true/false", f.autoGap, f.sign, f.apply)
	}
}

// TestParseFlagsTrailingValueLessFlag pins the documented accept-and-default
// semantic (parseFlags contract #1): a value flag at the end of the line
// with no value is accepted and left at its default — never an error, never
// silently swallowed as a positional. This is the single unified semantic
// that replaced the stdlib FlagSets, which each rejected the same input
// ("flag needs an argument: -x") with per-command error paths.
func TestParseFlagsTrailingValueLessFlag(t *testing.T) {
	f, rest, err := parseFlags([]string{"--root", "x", "--out"})
	if err != nil {
		t.Fatalf("trailing --out must be accepted (accept-and-default): %v", err)
	}
	if f.root != "x" {
		t.Errorf("root = %q, want x (value flag before the trailing one)", f.root)
	}
	if f.out != "" {
		t.Errorf("out = %q, want default (empty)", f.out)
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want empty (the trailing flag is not a positional)", rest)
	}
	// Same for a migrated FlagSet int flag (flight gc --keep-tasks).
	f, _, err = parseFlags([]string{"--keep-tasks"})
	if err != nil {
		t.Fatalf("trailing --keep-tasks must be accepted (accept-and-default): %v", err)
	}
	if f.keepTasks != 20 {
		t.Errorf("keepTasks = %d, want default 20", f.keepTasks)
	}
	// And for a repeatable migrated flag (policy-dsl --file).
	f, _, err = parseFlags([]string{"--file"})
	if err != nil {
		t.Fatalf("trailing --file must be accepted (accept-and-default): %v", err)
	}
	if len(f.files) != 0 || f.file != "" {
		t.Errorf("files = %v file %q, want empty defaults", f.files, f.file)
	}
}

// TestParseFlagsMigratedDefaults pins the preserved FlagSet defaults of the
// migrated per-command flags (contract: name, default and help text were
// preserved exactly through the unified parser).
func TestParseFlagsMigratedDefaults(t *testing.T) {
	f, _, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags(nil): %v", err)
	}
	if f.keepTasks != 20 || f.k != "5" || f.halfLife != "7.0" || f.format != "text" ||
		f.chunkSize != "1000" || f.ttl != "300" || f.injectMemory != "true" {
		t.Errorf("migrated defaults = keepTasks %d k %q halfLife %q format %q chunkSize %q ttl %q injectMemory %q",
			f.keepTasks, f.k, f.halfLife, f.format, f.chunkSize, f.ttl, f.injectMemory)
	}
}

// TestRunHealthRootEqualsForm pins the unified parser at the handler level:
// a migrated FlagSet command (health) accepts the --flag=value form.
func TestRunHealthRootEqualsForm(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	out := captureStdout(t, func() { runHealth([]string{"--root=" + root}) })
	m := assertValidJSON(t, out)
	idx, ok := m["index"].(map[string]any)
	if !ok {
		t.Fatalf("health output missing index block: %v", m)
	}
	if idx["built"] != false {
		t.Fatalf("index block = %v, want built=false (empty dir)", idx)
	}
}
