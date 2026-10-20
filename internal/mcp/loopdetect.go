package mcp

import (
	"context"
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	"github.com/JayveerPrajapati/kern/internal/semcache"
)

// loopPrimaryArg maps each etag-eligible read tool to its primary subject
// argument — the text the query is ABOUT, which semcache.Normalize folds into
// the comparison. Eligible tools without a subject argument are skipped.
var loopPrimaryArg = map[string]string{
	"kern_compact_file": "path",
	"kern_context":      "symbol",
	"kern_explore":      "symbol",
	"kern_graph":        "symbol",
	"kern_search":       "query",
	"kern_why":          "symbol",
}

// appendLoopMarker appends the advisory loop-suspect marker line after the
// payload text with a separating newline (an empty payload yields the marker
// alone). An empty marker returns the text unchanged.
func appendLoopMarker(text, marker string) string {
	if marker == "" {
		return text
	}
	if text == "" {
		return marker
	}
	return text + "\n" + marker
}

// loopObserve feeds this read call's query into the session loop detector
// and returns the advisory marker line when a near-duplicate was flagged
// ("" otherwise). It uses the same resolved tool set (etag.Eligible, the
// ROUTED call for kern_meta) and session keying as the served ledger. Never
// denies or alters a call — advisory only. A nil detector (hand-rolled test
// servers) is a no-op.
func (s *Server) loopObserve(ctx context.Context, name string, args map[string]any) string {
	if s.loopdetect == nil {
		return ""
	}
	rname, rargs := resolveCacheCall(ctx, name, args)
	if !etag.Eligible(rname) {
		return ""
	}
	argName := loopPrimaryArg[rname]
	if argName == "" {
		return ""
	}
	subject := argString(rargs, argName)
	if subject == "" {
		return ""
	}
	suspect, found := s.loopdetect.Observe(sessionKeyFor(args), semcache.Normalize(rname, subject))
	if !found {
		return ""
	}
	return fmt.Sprintf("[kern loop-suspect] resembles call #%d (sim %.2f) — you may already have this; re-use etag or emit a plan (kern_plan)", suspect.CallSeq, suspect.Sim)
}
