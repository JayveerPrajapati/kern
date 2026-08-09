package terse

import (
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
