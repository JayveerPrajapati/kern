package compress

import (
	"fmt"
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

func TestCompressLogTimeOnlyTimestamps(t *testing.T) {
	log := strings.Join([]string{
		"10:00:00.001 INFO starting service",
		"10:00:01.002 INFO heartbeat ok",
		"10:00:02.003 ERROR failed to connect: dial tcp 10.0.0.1:8080",
		"10:00:03.004 ERROR failed to connect: dial tcp 10.0.0.1:8080",
		"10:00:04.005 INFO done",
	}, "\n")
	got := CompressLog(log, Options{MaxLines: 200})
	if !strings.Contains(got, "ERROR failed to connect") {
		t.Fatalf("expected error line retained, got:\n%s", got)
	}
	if strings.Contains(got, "10:00:00") || strings.Contains(got, "10:00:01") {
		t.Fatalf("expected time-only timestamps stripped, got:\n%s", got)
	}
	if c := strings.Count(got, "ERROR failed to connect"); c != 1 {
		t.Fatalf("expected dedupe of error line, got %d:\n%s", c, got)
	}
	if strings.Contains(got, "heartbeat") {
		t.Fatalf("expected heartbeat chatter removed, got:\n%s", got)
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

func TestCompressLogClustersRepeatedTraces(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 450; i++ {
		fmt.Fprintf(&sb, "2024-01-01 10:00:%02d ERROR ConnTimeout: database unreachable at address 0x%X (goroutine %d)\n", i%60, 0x7fA1B2+i, i)
	}
	got := CompressLog(sb.String(), Options{MaxLines: 1000, Cluster: true})
	if c := strings.Count(got, "ConnTimeout"); c != 1 {
		t.Fatalf("expected exactly one ConnTimeout line, got %d:\n%s", c, got)
	}
	if !strings.Contains(got, "ConnTimeout") {
		t.Fatalf("expected clustered ConnTimeout line retained, got:\n%s", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "(repeated 450x)") {
		t.Fatalf("expected line ending in '(repeated 450x)', got:\n%s", got)
	}
}

func TestCompressLogClusterDisabled(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&sb, "2024-01-01 10:00:%02d ERROR ConnTimeout: database unreachable at address 0x%X (goroutine %d)\n", i, 0x7fA1B2+i, i)
	}
	got := CompressLog(sb.String(), Options{MaxLines: 1000})
	if c := strings.Count(got, "ConnTimeout"); c < 2 {
		t.Fatalf("expected near-duplicate lines to stay distinct without clustering, got %d:\n%s", c, got)
	}
	if strings.Contains(got, "repeated") {
		t.Fatalf("expected no repeat annotation without clustering, got:\n%s", got)
	}
}

func TestCompressLogClusterDeterministic(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&sb, "2024-01-01 10:00:%02d ERROR ConnTimeout: database unreachable at address 0x%X (goroutine %d)\n", i%60, 0x7fA1B2+i, i)
	}
	in := sb.String()
	a := CompressLog(in, Options{MaxLines: 1000, Cluster: true})
	b := CompressLog(in, Options{MaxLines: 1000, Cluster: true})
	if a != b {
		t.Fatalf("expected byte-identical output, got:\n%q\nvs\n%q", a, b)
	}
}

func TestCompressLogClusterFuzzyMerge(t *testing.T) {
	log := "ERROR request failed for user alice\nERROR request failed for user bob\n"
	got := CompressLog(log, Options{MaxLines: 200, Cluster: true})
	if c := strings.Count(got, "ERROR request failed for user"); c != 1 {
		t.Fatalf("expected one merged line, got %d:\n%s", c, got)
	}
	if !strings.Contains(got, "(repeated 2x)") {
		t.Fatalf("expected fuzzy-merged line annotated '(repeated 2x)', got:\n%s", got)
	}
}

func TestCompressLogClusterRespectsMaxLines(t *testing.T) {
	msgs := []string{
		"connection refused", "timeout", "permission denied", "disk full",
		"invalid argument", "broken pipe", "deadline exceeded", "resource busy",
		"not found", "out of memory",
	}
	var sb strings.Builder
	for r := 0; r < 20; r++ {
		for i, m := range msgs {
			fmt.Fprintf(&sb, "2024-01-01 10:00:%02d ERROR %s on attempt %d (goroutine %d)\n", (i+r*10)%60, m, r+1, r*10+i+1)
		}
	}
	got := CompressLog(sb.String(), Options{MaxLines: 5, Cluster: true})
	if n := strings.Count(got, "ERROR "); n > 5 {
		t.Fatalf("expected at most 5 clustered lines, got %d:\n%s", n, got)
	}
	if !strings.Contains(got, "lines omitted") {
		t.Fatalf("expected omission message, got:\n%s", got)
	}
}

func TestCompressLogClusterKeepsUniqueErrors(t *testing.T) {
	log := strings.Join([]string{
		"2024-01-01 10:00:00 ERROR connection refused on port 8080",
		"2024-01-01 10:00:01 WARN disk space low at 91%",
		"2024-01-01 10:00:02 FATAL nil pointer dereference in handler",
	}, "\n")
	got := CompressLog(log, Options{MaxLines: 200, Cluster: true})
	for _, want := range []string{"connection refused", "disk space low", "nil pointer dereference"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected distinct error %q preserved, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "repeated") {
		t.Fatalf("expected no repeat annotation for distinct errors, got:\n%s", got)
	}
	if c := strings.Count(got, "\n") + 1; c != 3 {
		t.Fatalf("expected all 3 distinct lines, got %d:\n%s", c, got)
	}
}

func TestCompressLogFoldStackFrames(t *testing.T) {
	log := strings.Join([]string{
		"2024-01-01 10:00:00 ERROR panic in handler",
		"goroutine 1 [running]:",
		"main.main()",
		"\t/workspace/cmd/billing/main.go:42",
		"net/http.(*conn).serve()",
		"\t/usr/local/go/src/net/http/server.go:2000",
		"net/http.serverHandler.ServeHTTP()",
		"\t/usr/local/go/src/net/http/server.go:2800",
		"runtime.main()",
		"\t/usr/local/go/src/runtime/proc.go:250",
		"runtime.goexit()",
		"\t/usr/local/go/src/runtime/asm_amd64.s:1590",
		"main.init()",
		"\t/workspace/cmd/billing/main.go:10",
	}, "\n")

	got := CompressLog(log, Options{MaxLines: 200})
	if !strings.Contains(got, "external frames folded") {
		t.Fatalf("expected external frames folded placeholder, got:\n%s", got)
	}
	if !strings.Contains(got, "main.main()") || !strings.Contains(got, "main.init()") {
		t.Fatalf("expected top and bottom app frames preserved, got:\n%s", got)
	}
}
