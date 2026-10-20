package mcpcli

import (
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRunContextWatchNoTextExits2 pins the usage contract: a
// context-watch without text (no --text, no positional, no stdin) is a
// usage error (exit 2).
func TestRunContextWatchNoTextExits2(t *testing.T) {
	if code := RunContextWatch(nil); code != 2 {
		t.Fatalf("RunContextWatch(nil) = %d, want 2 (usage error)", code)
	}
}

// TestRunContextWatchReport pins the happy path: text + budget produce the
// report header on stdout and exit 0.
func TestRunContextWatchReport(t *testing.T) {
	out := captureStdout(t, func() {
		if code := RunContextWatch([]string{"--budget", "100", "some sample text"}); code != 0 {
			t.Fatalf("RunContextWatch = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "CONTEXT WATCH REPORT") {
		t.Fatalf("output missing report header: %q", out)
	}
}
