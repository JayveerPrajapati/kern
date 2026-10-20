package mcp

import (
	"context"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	"github.com/JayveerPrajapati/kern/internal/mcphints"
)

// hintsOutcomeFor classifies a full-payload response text for hint selection:
// kern_search's "no symbols matched" output is the no-match outcome; anything
// else successful is the success outcome. Failures never reach here — hints
// ride payloads only (error returns, pointer lines, etag short-circuits and
// slice re-reads never carry one).
func hintsOutcomeFor(rname, text string) mcphints.Outcome {
	if rname == "kern_search" && strings.HasPrefix(text, "no symbols matched:") {
		return mcphints.OutcomeNoMatch
	}
	return mcphints.OutcomeSuccess
}

// hintFor returns the single advisory hint line to append to THIS response
// ("" = none). It observes the call (usage + read count — the gap nudge's
// data), then selects the table hint for the (tool, outcome); when no table
// hint fires and the session has a usage gap, the gap hint. Suppressed
// entirely when the Phase-1 loop marker is already appended to this response
// (footer-bloat control: max one advisory line per response — the marker
// already teaches). A nil engine (hand-rolled test servers) is a no-op.
func (s *Server) hintFor(ctx context.Context, name string, args map[string]any, text, loopMarker string) string {
	if s.hints == nil {
		return ""
	}
	rname, _ := resolveCacheCall(ctx, name, args)
	if !etag.Eligible(rname) {
		return ""
	}
	// Slice re-reads never carry hints (same contract as the loop marker):
	// a windowed view (start_line/end_line) is a partial read, not a full
	// payload, and its byte count is pinned by exact-output tests.
	if argString(args, "start_line") != "" || argString(args, "end_line") != "" {
		return ""
	}
	key := sessionKeyFor(args)
	// Every observed eligible call counts as a read-phase call and as usage.
	s.hints.Track(key, rname, true)
	if loopMarker != "" {
		return "" // the loop marker already teaches this response
	}
	if line := s.hints.Select(key, rname, hintsOutcomeFor(rname, text)); line != "" {
		return line
	}
	return s.hints.Gap(key, s.hints.Reads(key), s.hints.Used(key))
}

// appendHintLine appends the advisory hint line after the payload text with a
// separating newline (an empty payload yields the hint alone). An empty hint
// returns the text unchanged.
func appendHintLine(text, hint string) string {
	if hint == "" {
		return text
	}
	if text == "" {
		return hint
	}
	return text + "\n" + hint
}
