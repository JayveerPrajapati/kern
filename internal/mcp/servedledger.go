package mcp

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	"github.com/JayveerPrajapati/kern/internal/metaroute"
	"github.com/JayveerPrajapati/kern/internal/servedledger"
)

// ledgerFileRefRe matches kern's own output-path format — `path/to/file.go:NN`
// — in served response text. The outputs are kern-controlled (symbol evidence
// footers, callers/callees, source slices), so a conservative relative-path
// match is deterministic: group 1 is the root-relative file path.
var ledgerFileRefRe = regexp.MustCompile(`([\w./-]+\.[a-z]+):\d+`)

// ledgerFilesCap bounds the file references extracted per entry (a served
// response can reference dozens of paths; 32 covers any plan's affected set).
const ledgerFilesCap = 32

// sessionKeyFor derives the served-ledger session key from a call's args,
// mirroring the precheckTool identity precedent (toolpolicy.go): agent_id +
// "|" + task when agent_id is present, else "default". Same-client
// cross-task pollution is bounded by the per-session FIFO cap.
func sessionKeyFor(args map[string]any) string {
	agentID := argString(args, "agent_id")
	if agentID == "" {
		return "default"
	}
	return agentID + "|" + argString(args, "task")
}

// ledgerContentKey returns the content-identity component of the served-ledger
// key, reusing the D1 cache's freshness machinery: the file fingerprint for
// file-backed tools (kern_compact_file — an edited file rotates the key) and
// the session's index identity for index-backed tools (an edited tree rotates
// the key via the rate-limited staleness probe). A changed tree therefore
// serves FRESH content on the next call — never a stale "already served"
// pointer — exactly like the D1 key's content components, but WITHOUT
// root/schema/version: session semantics, content-aware.
func (s *Server) ledgerContentKey(ctx context.Context, name string, args map[string]any) string {
	root := resolveRoot(argString(args, "root"))
	if fp := fileFingerprint(ctx, name, args, root); fp != "" {
		return "fp:" + fp
	}
	return "ix:" + s.cacheIndexIdentity(root)
}

// ledgerLookup reports whether this (tool, view-defining args) was already
// served in this session, returning the one-line pointer. It mirrors the D1
// cache's resolution: the ROUTED call for kern_meta, the canonical args via
// etag.StripServeTimeArgs + json.Marshal, plus the content identity (see
// ledgerContentKey). Bypasses: no_cache=1 (fresh serve, same as D1), an
// explicit etag (the caller is already speaking the conditional-fetch
// protocol — the D1 path serves the tiny "unchanged" reply, which is exactly
// what the pointer teaches), and embedding-dependent semantic calls (R3
// parity: a semantic result depends on KERN_EMBED_MODEL, so it is never
// deduped as "already served"). Only the etag-eligible read tools participate
// (etag.Eligible — the ledger's tool set). A nil ledger (hand-rolled test
// servers) degrades to a miss.
func (s *Server) ledgerLookup(ctx context.Context, name string, args map[string]any) (string, bool) {
	if s.ledger == nil || !servedledger.Enabled() || noCacheArg(args) || argString(args, "etag") != "" {
		return "", false
	}
	rname, rargs := resolveCacheCall(ctx, name, args)
	if !etag.Eligible(rname) || argBool(rargs, "semantic") || argBool(rargs, metaroute.WorkingsetArg) {
		return "", false
	}
	canon, _ := json.Marshal(etag.StripServeTimeArgs(rargs))
	key := string(canon) + "\x00" + s.ledgerContentKey(ctx, name, args)
	return s.ledger.Lookup(sessionKeyFor(args), rname, key)
}

// ledgerFilesFromText extracts the root-relative file paths referenced by a
// served response's text (kern's own `path:line` output format). Absolute
// paths and ".." escapes are skipped (only project-relative files can be
// "served this session" for the sufficiency proof); results are deduped,
// sorted (deterministic) and capped at ledgerFilesCap.
func ledgerFilesFromText(text string) []string {
	seen := map[string]bool{}
	for _, m := range ledgerFileRefRe.FindAllStringSubmatch(text, -1) {
		p := m[1]
		if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
			continue
		}
		seen[p] = true
		if len(seen) >= ledgerFilesCap {
			break
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ledgerRecord records a full-payload serve into the session ledger with the
// etag minted for it (scope.etag — set for eligible tools on both the fresh
// dispatch and the D1-cache-hit paths; "" is fine) and the file references
// extracted from the served text (B3: the context-sufficiency data source).
// Short-circuits never reach the record sites (they return before), and
// no_cache=1 calls are never recorded (fresh serves bypass the ledger exactly
// like D1).
func (s *Server) ledgerRecord(ctx context.Context, name string, args map[string]any, text string) {
	if s.ledger == nil || !servedledger.Enabled() || noCacheArg(args) {
		return
	}
	rname, rargs := resolveCacheCall(ctx, name, args)
	if !etag.Eligible(rname) || argBool(rargs, "semantic") || argBool(rargs, metaroute.WorkingsetArg) {
		return
	}
	e := ""
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
		e = scope.etag
	}
	canon, _ := json.Marshal(etag.StripServeTimeArgs(rargs))
	key := string(canon) + "\x00" + s.ledgerContentKey(ctx, name, args)
	s.ledger.Record(sessionKeyFor(args), rname, key, e, ledgerFilesFromText(text))
}
