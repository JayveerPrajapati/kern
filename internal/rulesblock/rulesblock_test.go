package rulesblock_test

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/rulesblock"
)

// markerOpen mirrors the exact begin marker the setup writers emit.
const markerOpen = "<!-- kern:global-rules begin (managed by `kern setup --global-rules`; keep personal prefs outside the markers) -->"

func TestExciseKernBlock(t *testing.T) {
	if got, err := rulesblock.ExciseKernBlock("no kern here"); err != nil || got != "no kern here" {
		t.Fatalf("no-op expected, got %q err %v", got, err)
	}
	in := "# kern usage rules\n\nold\n\n# next header\n\nbody\n"
	want := "# next header\n\nbody\n"
	if got, err := rulesblock.ExciseKernBlock(in); err != nil || got != want {
		t.Fatalf("ExciseKernBlock mismatch:\ngot:\n%q\nwant:\n%q\nerr: %v", got, want, err)
	}
	// Kern at EOF: remove through end.
	if got, err := rulesblock.ExciseKernBlock("# kern usage rules\n\nold"); err != nil || got != "" {
		t.Fatalf("expected empty, got %q err %v", got, err)
	}
}

// TestExciseKernBlockMultipleBlocks pins the F15 fix: repeated `kern setup
// --global` runs used to accumulate one "# kern usage rules" block per run
// because a single-pass removal left earlier blocks in place. Every block
// must be removed, and the surrounding content preserved.
func TestExciseKernBlockMultipleBlocks(t *testing.T) {
	in := "# kern usage rules\n\noldest\n\n# user notes\n\nkeep me\n\n# kern usage rules\n\nmiddle\n\n# more user\n\nstill here\n\n# kern usage rules\n\nnewest\n"
	got, err := rulesblock.ExciseKernBlock(in)
	if err != nil {
		t.Fatalf("ExciseKernBlock error: %v", err)
	}
	if strings.Contains(got, "# kern usage rules") {
		t.Fatalf("kern block still present after multi-block removal:\n%s", got)
	}
	for _, want := range []string{"# user notes", "keep me", "# more user", "still here"} {
		if !strings.Contains(got, want) {
			t.Fatalf("user content %q lost during multi-block removal:\n%s", want, got)
		}
	}
}

// TestExciseKernBlockPreservesTrailingUserContentNoH1 pins the F1 follow-up:
// user content after the block WITHOUT an H1 of its own used to be swallowed
// up to EOF (and a setup re-run then silently lost it while reporting
// "already current"). The excise must anchor the block's end on the kern
// rules asset's own tail line and preserve everything after it.
func TestExciseKernBlockPreservesTrailingUserContentNoH1(t *testing.T) {
	// Realistic block shape: heading, version stamp, asset body ending with
	// the global-omit close marker — then user content with no H1.
	block := "# kern usage rules\n<!-- kern-version: dev --> for agents — READ FIRST\n`kern` is a local context engine.\n## The kern_meta tool\nbody\n<!-- kern:global-omit:end -->\n"
	in := block + "\nmy personal notes\n- remember milk\n"
	got, err := rulesblock.ExciseKernBlock(in)
	if err != nil {
		t.Fatalf("ExciseKernBlock error: %v", err)
	}
	if got != "my personal notes\n- remember milk\n" {
		t.Fatalf("trailing user content not preserved exactly:\ngot:\n%q", got)
	}
	if strings.Contains(got, "kern usage rules") || strings.Contains(got, "global-omit") {
		t.Fatalf("kern block still present:\n%s", got)
	}
	// The same shape with an intervening H1 keeps the old boundary semantics.
	inH1 := block + "\n# My Notes\n- remember milk\n"
	gotH1, err := rulesblock.ExciseKernBlock(inH1)
	if err != nil {
		t.Fatalf("ExciseKernBlock error: %v", err)
	}
	if gotH1 != "# My Notes\n- remember milk\n" {
		t.Fatalf("H1-bounded content not preserved:\ngot:\n%q", gotH1)
	}
}

// TestExciseKernBlockCRLF pins CRLF tolerance: Windows-authored rule
// files carry \r\n line endings, and the asset-tail anchors must still
// match so trailing user content is preserved.
func TestExciseKernBlockCRLF(t *testing.T) {
	block := "# kern usage rules\r\n<!-- kern-version: dev --> for agents — READ FIRST\r\nbody\r\n<!-- kern:global-omit:end -->\r\n"
	in := block + "\r\nmy notes\r\n- item\r\n"
	got, err := rulesblock.ExciseKernBlock(in)
	if err != nil {
		t.Fatalf("ExciseKernBlock error: %v", err)
	}
	if !strings.Contains(got, "my notes") || !strings.Contains(got, "- item") {
		t.Fatalf("CRLF trailing user content lost:\ngot:\n%q", got)
	}
	if strings.Contains(got, "kern usage rules") {
		t.Fatalf("kern block still present:\n%s", got)
	}
	// Block-only CRLF file excises to empty.
	gotOnly, err := rulesblock.ExciseKernBlock(block)
	if err != nil {
		t.Fatalf("ExciseKernBlock error: %v", err)
	}
	if strings.TrimSpace(gotOnly) != "" {
		t.Fatalf("block-only CRLF file not excised to empty:\ngot:\n%q", gotOnly)
	}
}

// TestExciseKernBlockMarkerFormat pins the marker-format excise: the
// "<!-- kern:global-rules begin ... -->" block is removed with everything
// outside it preserved, and an unbalanced marker (open without close) is a
// loud error instead of a half-excision.
func TestExciseKernBlockMarkerFormat(t *testing.T) {
	marked := markerOpen + "\n<!-- kern-version: dev -->\ncondensed rules\n<!-- kern:global-rules end -->\n"
	in := "# user header\n\n" + marked + "user footer without H1\n"
	got, err := rulesblock.ExciseKernBlock(in)
	if err != nil {
		t.Fatalf("ExciseKernBlock error: %v", err)
	}
	for _, want := range []string{"# user header", "user footer without H1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("user content %q lost:\n%s", want, got)
		}
	}
	if strings.Contains(got, "global-rules begin") || strings.Contains(got, "condensed rules") {
		t.Fatalf("marker block still present:\n%s", got)
	}
	// Unbalanced marker: open without close -> error.
	broken := "# user header\n\n" + markerOpen + "\nno close marker\n"
	if _, err := rulesblock.ExciseKernBlock(broken); err == nil {
		t.Fatalf("unbalanced marker block: expected an error, got nil")
	} else if !strings.Contains(err.Error(), "unbalanced") {
		t.Fatalf("unbalanced marker error should say so, got: %v", err)
	}
}

// TestIsKernBlockTailLine pins the asset-tail predicate: both the current
// global-omit close marker and the older pure-Go build note are anchors,
// and a trailing CR is tolerated.
func TestIsKernBlockTailLine(t *testing.T) {
	for _, line := range []string{"<!-- kern:global-omit:end -->", "<!-- kern:global-omit:end -->\r", "(`KERN_MCP_FULL=1` only).", "(`KERN_MCP_FULL=1` only).\r"} {
		if !rulesblock.IsKernBlockTailLine(line) {
			t.Errorf("IsKernBlockTailLine(%q) = false, want true", line)
		}
	}
	for _, line := range []string{"## The kern_meta tool", "# kern usage rules", "body"} {
		if rulesblock.IsKernBlockTailLine(line) {
			t.Errorf("IsKernBlockTailLine(%q) = true, want false", line)
		}
	}
}
