package budget

import (
	"strconv"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

func TestFitUnderBudgetUnchanged(t *testing.T) {
	text := "hello world this is short"
	out := Fit(text, 1000)
	if out != text {
		t.Fatalf("expected unchanged, got %q", out)
	}
}

func TestFitDedupsLines(t *testing.T) {
	text := strings.Repeat("error: connection refused\n", 200)
	out := Fit(text, 20)
	if strings.Count(out, "error: connection refused") != 1 {
		t.Fatalf("duplicate line not deduped:\n%s", out)
	}
}

func TestFitRespectsBudget(t *testing.T) {
	line := "ERROR worker-3 failed to connect to 127.0.0.1:11434 with timeout after retry"
	text := strings.Repeat(line+"\n", 500)
	out := Fit(text, 200)
	if n := tokenize.Count(out); n > 200 {
		t.Fatalf("exceeded budget: %d tokens\n%s", n, out)
	}
}

func TestFitKeepsImportantLines(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("INFO progress line number " + strconv.Itoa(i) + " with padding\n")
	}
	sb.WriteString("FATAL unrecoverable panic in main")
	out := Fit(sb.String(), 200)
	if !strings.Contains(out, "panic") {
		t.Fatalf("important line dropped:\n%s", out)
	}
}

func TestFitLosslessOnlyDedups(t *testing.T) {
	text := "a\nb\na\nc\nb\n"
	out := FitLossless(text)
	if out != "a\nb\nc\n" {
		t.Fatalf("lossless dedup wrong: %q", out)
	}
}

func TestFitEmpty(t *testing.T) {
	if Fit("", 10) != "" {
		t.Fatal("empty in, empty out")
	}
}
