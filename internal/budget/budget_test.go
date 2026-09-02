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

func TestFitCodePreservesGoSignatures(t *testing.T) {
	// Three long functions plus a type: far over budget in raw form.
	var sb strings.Builder
	sb.WriteString("package main\n\nimport \"fmt\"\n\n")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		sb.WriteString("// " + name + " does heavy work.\n")
		sb.WriteString("func " + name + "(input int) int {\n")
		for i := 0; i < 12; i++ {
			sb.WriteString("\taccumulator := accumulator + input*" + string(rune('a'+i)) + " + " + string(rune('1'+i%9)) + "\n")
		}
		sb.WriteString("\treturn accumulator\n}\n\n")
	}
	sb.WriteString("type Config struct {\n\tHost string\n\tPort int\n}\n")
	text := sb.String()
	if n := tokenize.Count(text); n <= 150 {
		t.Fatalf("test input too small to exercise the budget: %d tokens", n)
	}
	out := FitCode(text, 150)
	if n := tokenize.Count(out); n > 150 {
		t.Fatalf("exceeded budget: %d tokens\n%s", n, out)
	}
	if got := strings.Count(out, "func "); got < 2 {
		t.Fatalf("expected at least 2 preserved func signatures, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "body elided") {
		t.Fatalf("expected elided-body marker in output:\n%s", out)
	}
	if strings.Contains(out, "accumulator := accumulator") {
		t.Fatalf("function bodies must be elided:\n%s", out)
	}
}

func TestFitCodeSmallTextPassthrough(t *testing.T) {
	text := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	if out := FitCode(text, 1000); out != text {
		t.Fatalf("text under budget must pass through byte-identical, got:\n%s", out)
	}
}

func TestFitCodeNonCodeMatchesFit(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString("ERROR worker-3 failed to connect to 127.0.0.1:11434 with timeout after retry (attempt " + strconv.Itoa(i) + ")\n")
	}
	sb.WriteString("WARN disk usage at 92% on /var/lib\n")
	text := sb.String()
	fit := Fit(text, 100)
	if out := FitCode(text, 100); out != fit {
		t.Fatalf("non-code text must follow the plain Fit path:\nFitCode:\n%s\n\nFit:\n%s", out, fit)
	}
}

func TestFitCodeHardBudget(t *testing.T) {
	var sb strings.Builder
	words := []string{"alpha", "beta", "gamma", "delta", "epsilOn", "zeta", "theta", "lambda"}
	for i := 0; i < 800; i++ {
		sb.WriteString("line " + strconv.Itoa(i) + " " + words[i%len(words)] + " payload " + words[(i*3)%len(words)] + "\n")
	}
	text := sb.String()
	out := FitCode(text, 100)
	if n := tokenize.Count(out); n > 100 {
		t.Fatalf("exceeded budget: %d tokens\n%s", n, out)
	}
}

func TestFitCodeDeterministic(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("package main\n\nfunc alpha(x int) int {\n")
	for i := 0; i < 30; i++ {
		sb.WriteString("\tv := v + x + " + strconv.Itoa(i) + "\n")
	}
	sb.WriteString("\treturn v\n}\n")
	text := sb.String()
	first := FitCode(text, 120)
	second := FitCode(text, 120)
	if first != second {
		t.Fatalf("FitCode must be deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
