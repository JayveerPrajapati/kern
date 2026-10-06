package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
)

// gateSourceRead gates the source-serving context-family tools
// (kern_compact_file, kern_fit_context) through the same per-call governor
// as the governed retrieval family (deep-dive C6, 2026-10-03): without it
// these tools served verbatim source outside the authorized scope while
// kern_context/kern_explore/kern_retrieve filtered. A requested file is
// readable when the governor allows at least one symbol defined in it, or
// when it defines no indexed symbols (non-code files were never governed);
// a named symbol is checked directly against the allowed set. Raw mode
// (KERN_MCP_PERMISSIVE=1, nil governor) passes through unchanged.
//
// The gate covers the index-of-record (the "root" arg / process cwd, exactly
// like the governed graph family); paths outside that project belong to
// another root and are bounded by the KERN_MCP_ROOTS confinement gate.
func (s *Server) gateSourceRead(ctx context.Context, args map[string]any) error {
	var paths []string
	if p := mcpargs.ArgString(args, "path"); p != "" {
		paths = append(paths, p)
	}
	if files := mcpargs.ArgString(args, "files"); files != "" {
		paths = append(paths, strings.Split(files, ",")...)
	}
	var symbols []string
	if syms := mcpargs.ArgString(args, "symbols"); syms != "" {
		symbols = strings.Split(syms, ",")
	}
	if len(paths) == 0 && len(symbols) == 0 {
		return nil
	}
	// The gate loads the index only to authorize the read. compact_file and
	// fit_context are not index-backed tools: restore the per-call scope's
	// index after the gate so their responses stay byte-identical to the
	// ungated shape (no index banner, no conditional-fetch etag drift — the
	// etag hashes the raw pre-sandbox handler text).
	scope, _ := ctx.Value(indexScopeKey{}).(*indexScope)
	var prevIX *index.Index
	if scope != nil {
		prevIX = scope.ix
		defer func() { scope.ix = prevIX }()
	}
	root := resolveRoot(argString(args, "root"))
	var ix *index.Index
	// Calls WITHOUT explicit governance context (no agent_id, no scope)
	// never build an index just to be gated — the default scope admits the
	// whole project root, so peeking the watcher/session cache is
	// sufficient and the gate is a no-op on a never-indexed root
	// (behavior-identical to the pre-gate fast path; the KERN_MCP_ROOTS
	// confinement gate remains the operative boundary there). Calls WITH
	// governance context authorize fully, building the index if needed — the
	// caller asked for scoped reads.
	if mcpargs.ArgString(args, "agent_id") == "" && gov.TaskScopeFromArgs(args, "") == nil {
		ix = s.peekIndex(root)
		if ix == nil {
			return nil
		}
	} else {
		loaded, lerr := s.loadIndex(ctx, argString(args, "root"))
		if lerr != nil {
			return lerr
		}
		ix = loaded
	}
	g, gerr := s.newGovernor(ctx, args, ix)
	if gerr != nil {
		return gerr
	}
	if g == nil {
		return nil
	}
	for _, p := range paths {
		rel, ok := gateRelPath(ix.Root, p)
		if !ok {
			continue
		}
		syms := ix.SymbolsByFile[rel]
		if len(syms) == 0 {
			continue // non-code / unindexed: never governed
		}
		allowed := false
		for _, sym := range syms {
			if g.Allowed[sym.FullName()] {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("authz: %s is outside the authorized read scope (see kern_authorize_context)", p)
		}
	}
	for _, name := range symbols {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !g.NameAllowed(ix, name) {
			return fmt.Errorf("authz: symbol %q is outside the authorized read scope (see kern_authorize_context)", name)
		}
	}
	return nil
}

// peekIndex returns the root's already-available index (watcher-published
// or session-cached) without ever triggering a build, or nil when none
// exists yet. Mirrors the D1 cache's cacheIndexIdentity peek discipline:
// an index-free tool must stay index-free.
func (s *Server) peekIndex(root string) *index.Index {
	if ix, ok := s.watchedIndex(root); ok {
		return ix
	}
	if sess, ok := s.sessCache.Peek(root); ok {
		if ix, ok := sess.CachedIndex(); ok && ix != nil {
			return ix
		}
	}
	return nil
}

// gateRelPath normalizes a requested path to the index's root-relative slash
// form (the SymbolsByFile key). It reports false for empty or
// root-escaping paths — those never belong to this index's project.
func gateRelPath(root, p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", false
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return "", false
		}
		return filepath.ToSlash(rel), true
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return filepath.ToSlash(clean), true
}
