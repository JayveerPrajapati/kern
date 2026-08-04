package compress

import (
	"strings"
	"testing"
)

func TestCompressLogKeepsErrors(t *testing.T) {
	log := strings.Join([]string{
		"2024-01-01 10:00:00 INFO starting service",
		"2024-01-01 10:00:01 INFO heartbeat ok",
		"2024-01-01 10:00:02 ERROR failed to connect: dial tcp 10.0.0.1:8080",
		"\tat main.main (main.go:42)",
		"\tat runtime.main (proc.go:250)",
		"2024-01-01 10:00:03 INFO done",
	}, "\n")
	got := CompressLog(log, Options{MaxLines: 200})
	if !strings.Contains(got, "ERROR failed to connect") {
		t.Fatalf("expected error line retained, got:\n%s", got)
	}
	if !strings.Contains(got, "main.go:42") {
		t.Fatalf("expected stack frame retained, got:\n%s", got)
	}
	if strings.Contains(got, "heartbeat") {
		t.Fatalf("expected heartbeat chatter removed, got:\n%s", got)
	}
	if strings.Contains(got, "2024-01-01 10:00:00") {
		t.Fatalf("expected timestamps stripped, got:\n%s", got)
	}
}

func TestCompressLogDedupe(t *testing.T) {
	text := "ERROR boom\nERROR boom\nERROR boom\n"
	got := CompressLog(text, Options{MaxLines: 200})
	if c := strings.Count(got, "ERROR boom"); c != 1 {
		t.Fatalf("expected dedupe to 1, got %d", c)
	}
}

func TestCompressLogMaxLines(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("ERROR line ")
		sb.WriteString(strings.Repeat("x", 10))
		sb.WriteString("\n")
	}
	got := CompressLog(sb.String(), Options{MaxLines: 10})
	if n := strings.Count(got, "ERROR line"); n > 10 {
		t.Fatalf("expected at most 10 lines, got %d", n)
	}
}

func TestCompressPromptCollapsesBlanksAndRuns(t *testing.T) {
	in := "a\n\n\nb\nb\nb\nc\n"
	got := CompressPrompt(in)
	want := "a\n\nb\nc"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
