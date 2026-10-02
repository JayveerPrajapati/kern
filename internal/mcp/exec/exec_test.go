package exec

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSandboxEmptyCommand(t *testing.T) {
	ctx := context.Background()
	_, err := Sandbox(ctx, "test", map[string]any{
		"command": "",
	})
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("expected error on empty command, got: %v", err)
	}
}

func TestRunBuildEmptyCommand(t *testing.T) {
	ctx := context.Background()
	_, err := RunBuild(ctx, "test", map[string]any{
		"command": "",
	})
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("expected error on empty command, got: %v", err)
	}
}

func TestExecList(t *testing.T) {
	ctx := context.Background()
	res, err := Exec(ctx, map[string]any{
		"list": "true",
	})
	if err != nil {
		t.Fatalf("Exec list failed: %v", err)
	}
	if !strings.Contains(res, "installed runtimes:") {
		t.Errorf("expected installed runtimes in list output, got: %s", res)
	}
}

// TestMaskedOutputMasksBeforeTruncate is the Stage 1b regression guard: the
// sandbox output is masked in FULL before the 4000-byte display cut, so a
// secret straddling the cut cannot escape the mask regexes. The AWS key
// begins 7 bytes before the cut; a truncate-first pipeline would split it
// (AKIAIOS / FODNN7EXAMPLE) and neither half matches the AKIA[0-9A-Z]{16}
// pattern — the mask must see the complete output to catch it.
func TestMaskedOutputMasksBeforeTruncate(t *testing.T) {
	const secret = "AKIAIOSFODNN7EXAMPLE" // 20 chars; matches pii's AWS pattern
	head := strings.Repeat("x", 4000-8)   // space at the cut-8 mark, secret straddles
	out := maskedOutput(head+" "+secret+"\n", 4000)
	if strings.Contains(out, "AKIA") {
		t.Fatalf("secret straddling the truncation cut escaped the mask: %q", out)
	}
	if !strings.Contains(out, "... (truncated)") {
		t.Fatalf("output must still be truncated at %d bytes, got %d bytes", 4000, len(out))
	}
	// Short output is masked but never truncated.
	short := maskedOutput("before "+secret+" after", 4000)
	if strings.Contains(short, "AKIA") || strings.Contains(short, "... (truncated)") {
		t.Fatalf("short output mishandled: %q", short)
	}
}

// TestExecTimeout pins the KERN_EXEC_TIMEOUT override: valid Go durations are
// honored, while unset, invalid, and non-positive values fall back to the
// 5-minute default (invalid values also emit a warning line on stderr).
func TestExecTimeout(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "unset", env: "", want: 5 * time.Minute},
		{name: "valid seconds", env: "90s", want: 90 * time.Second},
		{name: "valid minutes", env: "10m", want: 10 * time.Minute},
		{name: "valid mixed", env: "1m30s", want: 90 * time.Second},
		{name: "invalid garbage", env: "abc", want: 5 * time.Minute},
		{name: "invalid bare number", env: "90", want: 5 * time.Minute},
		{name: "invalid zero", env: "0s", want: 5 * time.Minute},
		{name: "invalid negative", env: "-5m", want: 5 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KERN_EXEC_TIMEOUT", tc.env)
			if got := execTimeout(); got != tc.want {
				t.Errorf("execTimeout() = %s, want %s", got, tc.want)
			}
		})
	}
}
