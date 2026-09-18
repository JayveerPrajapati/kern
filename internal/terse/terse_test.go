package terse

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompressStripsFiller(t *testing.T) {
	in := "Sure! Great question.\n\nI think the bug is here.\n\nNote that the error is in parseLine.\n\nfunc main() {}\n\nHope this helps!"
	out, dropped := Compress(in)
	if dropped < 3 {
		t.Fatalf("expected >=3 filler lines dropped, got %d", dropped)
	}
	if !contains(out, "func main() {}") {
		t.Fatalf("code block must survive: %q", out)
	}
	if contains(out, "Great question") || contains(out, "Hope this helps") {
		t.Fatalf("filler survived: %q", out)
	}
	if contains(out, "I think the bug is here") {
		t.Fatalf("hedge survived: %q", out)
	}
}

func TestCompressKeepsTechnicalPayload(t *testing.T) {
	in := "The function returns err: nil.\n\nJust to be clear, use ctx.\n\ncmd := exec.Command(\"go\", \"build\")\n\nLet me know if that works."
	out, _ := Compress(in)
	if !contains(out, "cmd := exec.Command") {
		t.Fatalf("code line dropped: %q", out)
	}
	if !contains(out, "err: nil") {
		t.Fatalf("error line dropped: %q", out)
	}
}

func TestCompressPreservesFences(t *testing.T) {
	in := "Here's the fix:\n\n```go\n// keep me\n```\n\nThanks!"
	out, _ := Compress(in)
	if !contains(out, "```go") || !contains(out, "// keep me") {
		t.Fatalf("fence content lost: %q", out)
	}
	if contains(out, "Thanks") {
		t.Fatalf("trailing thank-you survived: %q", out)
	}
}

func TestCompressNoOpOnCleanOutput(t *testing.T) {
	in := "var x = 42\n\nreturn x + 1\n"
	out, dropped := Compress(in)
	if out != "var x = 42\n\nreturn x + 1" {
		t.Fatalf("clean output changed: %q != %q", out, "var x = 42\n\nreturn x + 1")
	}
	if dropped != 0 {
		t.Fatalf("unexpected drops: %d", dropped)
	}
}

func TestCompressCollapsesBlankRuns(t *testing.T) {
	in := "a\n\n\n\n\nb"
	out, _ := Compress(in)
	if out != "a\n\nb" {
		t.Fatalf("blank runs not collapsed: %q", out)
	}
}

func TestCompressStripsFillerFromShortPayloadLine(t *testing.T) {
	// A short single-line response that carries technical payload must still
	// have its filler stripped (regression: payload lines were appended
	// verbatim, so "Sure! ... server.go. Hope that helps!" survived intact).
	in := "Sure! I'd be happy to help you with that. The function dispatch is defined in server.go. Hope that helps!"
	out, dropped := Compress(in)
	want := "The function dispatch is defined in server.go."
	if strings.TrimSpace(out) != want {
		t.Fatalf("compressed = %q, want %q", out, want)
	}
	if contains(out, "Sure!") || contains(out, "happy to help") || contains(out, "Hope that helps") {
		t.Fatalf("filler survived: %q", out)
	}
	if dropped != 0 {
		t.Fatalf("expected 0 dropped lines, got %d", dropped)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestStripPromptFluffRemovesConversationalFiller(t *testing.T) {
	in := "Hi there!\n\nI hope you're well.\n\nI'm trying to debug billing-worker at internal/worker/billing.go.\n\nPlease take a look at the code.\n\nThanks so much in advance!"
	out, dropped := StripPromptFluff(in)
	if dropped < 3 {
		t.Fatalf("expected >=3 fluff lines dropped, got %d", dropped)
	}
	if contains(out, "Hi there") || contains(out, "Thanks so much") {
		t.Fatalf("fluff survived: %q", out)
	}
	if !contains(out, "billing.go") {
		t.Fatalf("payload line dropped: %q", out)
	}
}

func TestStripPromptFluffKeepsTechnicalLineWithHedge(t *testing.T) {
	// "note that" can prefix a real instruction; the payload guard plus the
	// absence of generic hedge prefixes must keep this line.
	in := "Note that the parser fails on unicode input.\n\nThe fix is in tokenize.go."
	out, _ := StripPromptFluff(in)
	if !contains(out, "parser fails on unicode input") {
		t.Fatalf("real instruction dropped: %q", out)
	}
	if !contains(out, "tokenize.go") {
		t.Fatalf("payload line dropped: %q", out)
	}
}

func TestStripPromptFluffPreservesFence(t *testing.T) {
	in := "Please help me.\n\n```go\nfunc main() {}\n```\n\nThanks!"
	out, _ := StripPromptFluff(in)
	if !contains(out, "```go") || !contains(out, "func main()") {
		t.Fatalf("fence content lost: %q", out)
	}
}

func TestCompressPreservesTrailingWhitespaceFreeText(t *testing.T) {
	in := "hello world   \n  indented line  \n"
	out, _ := Compress(in)
	if out != "hello world\n  indented line" {
		t.Fatalf("compressed = %q, want %q", out, "hello world\n  indented line")
	}
}

func TestStripPromptFluffKeepsRequestAfterPrefix(t *testing.T) {
	// A filler-only line that is the ENTIRE prompt must not vanish: the
	// empty-result guard falls back to the original text.
	in := "So basically, I just wanted to say thanks for asking this question. Let me help you with that."
	out, _ := StripPromptFluff(in)
	if strings.TrimSpace(out) != in {
		t.Fatalf("real request was stripped: %q", out)
	}
}

func TestStripPromptFluffNeverReturnsEmpty(t *testing.T) {
	cases := []string{
		"So basically, I just wanted to say thanks for asking this question. Let me help you with that.",
		"please just simply provide the final answer thank you very much",
		"Hi, I hope you can help me with my question.",
	}
	for _, in := range cases {
		out, _ := StripPromptFluff(in)
		if strings.TrimSpace(out) == "" {
			t.Errorf("prompt stripped to empty: %q", in)
		}
	}
}

func TestCompressConversationalFiller(t *testing.T) {
	// K-16: polite filler phrases must be stripped
	in := "Certainly! Great question.\nBasically, the system works by caching queries.\nIn summary: everything is fine."
	out, dropped := Compress(in)
	if dropped < 2 {
		t.Errorf("expected >=2 dropped lines, got %d", dropped)
	}
	want := "The system works by caching queries."
	if strings.TrimSpace(out) != want {
		t.Errorf("Compress = %q, want %q", out, want)
	}

	// Single-line pure filler
	single := "Absolutely! I would be happy to help you with that."
	outSingle, droppedSingle := Compress(single)
	if droppedSingle != 1 || strings.TrimSpace(outSingle) != "" {
		t.Errorf("Compress single pure filler = %q (dropped %d), want empty (dropped 1)", outSingle, droppedSingle)
	}
}

func TestTersifyStripsBlankAndCommentLines(t *testing.T) {
	in := "// header comment\n\n\nfunc main() {\n\n    // inner comment\n    fmt.Println(\"hi\")\n}\n\n// trailing\n"
	out, st := Tersify(in, 0)
	if st.DroppedComment < 3 {
		t.Fatalf("expected >=3 comment lines dropped, got %d", st.DroppedComment)
	}
	if st.DroppedBlank < 3 {
		t.Fatalf("expected >=3 blank lines dropped, got %d", st.DroppedBlank)
	}
	if !contains(out, "func main() {") || !contains(out, `fmt.Println("hi")`) {
		t.Fatalf("code lines must survive: %q", out)
	}
	if contains(out, "header comment") || contains(out, "inner comment") {
		t.Fatalf("comment lines survived: %q", out)
	}
	if st.AfterTokens >= st.BeforeTokens {
		t.Fatalf("expected token savings on a comment-heavy file, %d -> %d", st.BeforeTokens, st.AfterTokens)
	}
}

func TestTersifyMarkdownHeadingsSurvive(t *testing.T) {
	in := "# Section One\n\n# todo: fix this later\n\n## Getting Started\n"
	out, st := Tersify(in, 0)
	if !contains(out, "# Section One") || !contains(out, "## Getting Started") {
		t.Fatalf("markdown headings must survive: %q", out)
	}
	if st.DroppedComment < 1 {
		t.Fatalf("expected the todo comment dropped, got %d", st.DroppedComment)
	}
}

func TestTersifyMaxBudgetKeepsHead(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("line of technical content number ")
		b.WriteString(itoaT(i))
		b.WriteString(" with some words\n")
	}
	in := b.String()
	out, st := Tersify(in, 40)
	if st.DroppedBudget <= 0 {
		t.Fatalf("expected budget drops, got %d", st.DroppedBudget)
	}
	if st.AfterTokens > 60 {
		t.Fatalf("output exceeds budget: %d tokens", st.AfterTokens)
	}
	if !strings.HasPrefix(out, "line of technical content number 0") {
		t.Fatalf("head not kept: %q", out)
	}
	if st.BeforeTokens <= st.AfterTokens {
		t.Fatalf("expected savings, %d -> %d", st.BeforeTokens, st.AfterTokens)
	}
}

func TestTersifyPreservesFences(t *testing.T) {
	in := "Sure!\n\n```go\n// comment inside fence\n\n   func x() {}\n```\n\nThanks!"
	out, st := Tersify(in, 0)
	if !contains(out, "```go") || !contains(out, "// comment inside fence") || !contains(out, "func x() {}") {
		t.Fatalf("fence content lost: %q", out)
	}
	if contains(out, "Sure") || contains(out, "Thanks") {
		t.Fatalf("filler survived: %q", out)
	}
	if st.DroppedFiller < 2 {
		t.Fatalf("expected 2 filler drops, got %d", st.DroppedFiller)
	}
}

func TestTersifyCollapsesRepeatedWhitespace(t *testing.T) {
	in := "  the   answer   is 42  "
	out, _ := Tersify(in, 0)
	if !contains(out, "the answer is 42") {
		t.Fatalf("whitespace not collapsed: %q", out)
	}
}

func itoaT(n int) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.Repeat(" ", 0), " ", "")) + fmt.Sprintf("%d", n)
}
