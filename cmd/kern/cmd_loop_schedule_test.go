package main

import (
	"strings"
	"testing"
	"time"
)

// TestNextScheduledRun pins the pure helper behind kern loop --schedule: it
// parses the cron expression and returns the deterministic next fire time
// strictly after now. This is the unit-level extraction from runLoop so the
// schedule wiring is tested without sleeping or running a real loop.
func TestNextScheduledRun(t *testing.T) {
	now := time.Date(2024, 1, 1, 12, 3, 0, 0, time.UTC)
	next, err := nextScheduledRun("*/5 * * * *", now)
	if err != nil {
		t.Fatalf("nextScheduledRun: %v", err)
	}
	if want := time.Date(2024, 1, 1, 12, 5, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("nextScheduledRun: got %v, want %v", next, want)
	}
	// Invalid cron → error (the CLI surfaces it as a usage error, exit 2).
	if _, err := nextScheduledRun("not-a-cron", now); err == nil {
		t.Fatal("nextScheduledRun: expected error for invalid cron, got nil")
	}
	if _, err := nextScheduledRun("", now); err == nil {
		t.Fatal("nextScheduledRun: expected error for empty cron, got nil")
	}
}

// TestRunLoopScheduleFlagWiring pins that parseFlags accepts --schedule in
// both the space and inline forms (the flag-acceptance path; the loop itself
// is not run here).
func TestRunLoopScheduleFlagWiring(t *testing.T) {
	f, rest, err := parseFlags([]string{"--schedule", "*/5 * * * *", "intent"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.schedule != "*/5 * * * *" {
		t.Fatalf("--schedule: got %q, want %q", f.schedule, "*/5 * * * *")
	}
	if len(rest) != 1 || rest[0] != "intent" {
		t.Fatalf("positional args: got %v, want [intent]", rest)
	}
	f, _, err = parseFlags([]string{"--schedule=0 0 * * *"})
	if err != nil {
		t.Fatalf("parseFlags inline: %v", err)
	}
	if f.schedule != "0 0 * * *" {
		t.Fatalf("--schedule= inline: got %q, want %q", f.schedule, "0 0 * * *")
	}
	// Default is "" (one-shot, unchanged).
	f, _, err = parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags(nil): %v", err)
	}
	if f.schedule != "" {
		t.Fatalf("default --schedule: got %q, want empty (one-shot)", f.schedule)
	}
}

// TestRunLoopScheduleInvalidExprExits2 pins that kern loop --schedule with an
// invalid cron expression is a usage error: exit 2 with a clear message.
func TestRunLoopScheduleInvalidExprExits2(t *testing.T) {
	errOut, code := captureStderrExit(t, func() {
		runLoop("loop", []string{"--schedule", "not-a-cron", "x"})
	})
	if code != 2 {
		t.Fatalf("expected exit 2 for an invalid cron expression, got %d", code)
	}
	if !strings.Contains(errOut, "--schedule") || !strings.Contains(errOut, "expected 5 fields") {
		t.Fatalf("expected a clear usage error naming --schedule and the cron problem, got: %q", errOut)
	}
}
