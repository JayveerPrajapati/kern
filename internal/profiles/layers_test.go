package profiles

import (
	"strings"
	"testing"
)

func TestRenderCompresses(t *testing.T) {
	verbose := "Real analysis line one.\nto summarize\nReal analysis line two.\nobviously\nReal analysis line three."
	r := Render(RawEvidence{Content: verbose, Source: "probe"})
	if !r.Compressed {
		t.Error("filler-heavy content should compress")
	}
	if r.RenderedTokens >= r.OriginalTokens {
		t.Errorf("rendered %d tokens should be < original %d", r.RenderedTokens, r.OriginalTokens)
	}
	if len(r.Content) >= len(verbose) {
		t.Errorf("compressed content should be shorter, got %d >= %d", len(r.Content), len(verbose))
	}
	if !strings.Contains(r.Content, "Real analysis line one.") {
		t.Errorf("payload should survive compression, got %q", r.Content)
	}
}

func TestRenderIncompressible(t *testing.T) {
	terse := "func main() {\n\treturn\n}"
	r := Render(RawEvidence{Content: terse})
	if r.Compressed {
		t.Error("already-terse content should not compress")
	}
	if r.Content != terse {
		t.Errorf("content should be unchanged, got %q", r.Content)
	}
	if r.RenderedTokens != r.OriginalTokens {
		t.Errorf("tokens should be equal, got %d vs %d", r.RenderedTokens, r.OriginalTokens)
	}
}

func TestFormatAppliesProfile(t *testing.T) {
	r := Render(RawEvidence{Content: "real content"})
	f := Format(r, MachineJSONProfile())
	if f.Profile.Name != "machine-json" {
		t.Errorf("Profile = %+v, want machine-json", f.Profile)
	}
	want := ApplyProfile(MachineJSONProfile(), r.Content)
	if f.Content != want {
		t.Errorf("Format content should equal ApplyProfile(rendered), got %q want %q", f.Content, want)
	}
}

func TestPipelineFull(t *testing.T) {
	raw := RawEvidence{Content: "to summarize\nreal payload line\n", Source: "test"}
	f := Pipeline(raw, DebugProfile())
	if f.Profile.Name != "debug" {
		t.Errorf("Profile = %+v, want debug", f.Profile)
	}
	if !strings.HasPrefix(f.Content, "profile: debug ") {
		t.Errorf("pipeline output should carry the debug prefix, got %q", f.Content)
	}
	if !strings.Contains(f.Content, "real payload line") {
		t.Errorf("pipeline should preserve the payload, got %q", f.Content)
	}
	// Compression happened before shaping: the filler line is gone.
	r := Render(raw)
	if !r.Compressed {
		t.Error("filler should have been compressed out in the rendered layer")
	}
	if strings.Contains(f.Content, "to summarize") {
		t.Errorf("filler should be dropped, got %q", f.Content)
	}
}
