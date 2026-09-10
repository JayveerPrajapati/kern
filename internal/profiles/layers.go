// Package profiles layers: the three-layer evidence model (raw → rendered →
// formatted) used to compress and shape evidence deterministically.
package profiles

import (
	"github.com/JayveerPrajapati/kern/internal/terse"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// RawEvidence is evidence before any shaping.
type RawEvidence struct {
	Content string
	Source  string
}

// RenderedEvidence is the compressed middle layer.
type RenderedEvidence struct {
	Content        string
	Compressed     bool // whether terse.Compress reduced the token count
	OriginalTokens int  // tokenize.Count before compression
	RenderedTokens int  // tokenize.Count after compression
}

// FormattedEvidence is profile-shaped evidence.
type FormattedEvidence struct {
	Content string
	Profile OutputProfile
}

// Render compresses raw evidence into the rendered layer using the
// deterministic internal/terse.Compress(text) (string, int) — the LLM-based
// internal/optimize path is deliberately NOT used (profiles must be
// deterministic). Compressed = rendered tokens < original tokens.
// (terse.Compress's int return is the number of DROPPED LINES, not tokens;
// both token counts are therefore measured with tokenize.Count per the field
// semantics.)
func Render(r RawEvidence) RenderedEvidence {
	original := tokenize.Count(r.Content)
	compressed, _ := terse.Compress(r.Content)
	rendered := tokenize.Count(compressed)
	return RenderedEvidence{
		Content:        compressed,
		Compressed:     rendered < original,
		OriginalTokens: original,
		RenderedTokens: rendered,
	}
}

// Format applies a profile to rendered evidence (shaping only).
func Format(r RenderedEvidence, p OutputProfile) FormattedEvidence {
	return FormattedEvidence{Content: ApplyProfile(p, r.Content), Profile: p}
}

// Pipeline is the three-layer one-shot RawEvidence → FormattedEvidence:
// Render then Format.
func Pipeline(r RawEvidence, p OutputProfile) FormattedEvidence {
	return Format(Render(r), p)
}
