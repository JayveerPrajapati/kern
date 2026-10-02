// Package etag implements the conditional-fetch contract for kern's MCP read
// tools (ADR-0012, phase B1): every eligible response carries an etag — the
// sha256 hex of the raw pre-sandbox response text combined with the tool's
// schema version, the scheme version, and the serve-time view (the call's
// max_output budget, F-2) — and a caller that passes etag=<previous> receives
// a tiny "unchanged" response when the content is identical. This saves
// tokens on repeat reads: the D1 cache already saves recompute, not tokens.
//
// The package owns the hash helpers, the short-circuit response text, the
// per-agent working-set registry (the set of etags an agent has been served),
// and the workingset listing that kern_meta's "my working set" route renders.
// It is a leaf: it imports nothing but the standard library, so any package
// (the mcp root, the CLI) may depend on it without cycle risk.
package etag

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// SchemaVersion is the version of the etag scheme itself. It is combined
// into every hash alongside the tool's catalog schema version and the
// serve-time view, so a change to how etags are computed (not just tool
// response shapes) mints fresh etags for unchanged content — old
// conditional-fetch callers re-fetch the new shape exactly once. v2 (F-2):
// etags are now bound to the serve-time view (max_output budget), which
// invalidates every v1 etag — one re-fetch, by design.
const SchemaVersion = "v2"

// EligibleTools are the read tools that participate in conditional fetch
// (ADR-0012): deterministic, index-backed retrieval whose raw response text
// can be hashed cheaply at serve time. Adding a tool here requires an `etag`
// string property in its catalog InputSchema, an `etag` parse in the leaf
// handlers (mcpargs pattern), and the --etag CLI flag on the mirrored
// command. The etag comparison itself happens in the mcp root around the
// leaf call, so leaves never import this package for it.
var EligibleTools = map[string]bool{
	"kern_compact_file": true,
	"kern_context":      true,
	"kern_explore":      true,
	"kern_retrieve":     true,
}

// Eligible reports whether the named tool participates in conditional fetch.
func Eligible(name string) bool { return EligibleTools[name] }

// HashView computes the etag for a raw pre-sandbox response text served under
// a specific serve-time view: sha256 hex of the text combined with the tool's
// catalog schema version, this scheme's version, and the view identifier.
// The view is the call's effective max_output budget (see mcpserve.CallOutputBudget)
// — the etag is bound to what the caller was actually served,
// so a different serve view is different content and mints a different etag
// even for byte-identical text (F-2). Identical text plus identical versions
// and view always mint the same etag; a bumped schema version mints a fresh
// one. There is deliberately NO per-file hashing — the hash covers the whole
// response, which is what the caller would otherwise re-pay tokens for.
func HashView(text, toolSchemaVersion, view string) string {
	h := sha256.New()
	_, _ = io.WriteString(h, text)
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, toolSchemaVersion)
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, SchemaVersion)
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, view)
	return hex.EncodeToString(h.Sum(nil))
}

// Hash computes the etag for a raw pre-sandbox response text without a
// serve-view component (the view-agnostic form): HashView with an empty view.
// The CLI (cmd/kern/cmd_etag.go) keeps calling this form, whose semantics are
// unchanged by the F-2 view binding.
func Hash(text, toolSchemaVersion string) string {
	return HashView(text, toolSchemaVersion, "")
}

// UnchangedResponseText is the body of the short-circuit response served when
// the caller's etag matches the freshly computed one. The caller recognizes
// the short-circuit from the result fields (etag + unchanged=true) rather
// than from this text, which stays human-readable.
func UnchangedResponseText(e string) string {
	return "unchanged (etag " + e + ")"
}

// StripServeTimeArgs returns a copy of args with the identity-only and
// conditional-fetch arguments removed: etag, no_cache, agent_id, task.
// max_output is deliberately NOT stripped (F-2): the etag is bound to the
// serve-time view, so different serve views are different content and must
// key separate D1 and registry entries. It is the ONE source of truth for
// that exclusion list, shared by the working-set registry key
// (CanonicalArgsKey) and the D1 cache key (cacheKeyFor in the mcp root) so
// the two keys can never drift (NIT-10).
func StripServeTimeArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		switch k {
		case "etag", "no_cache", "agent_id", "task":
			continue
		}
		out[k] = v
	}
	return out
}

// CanonicalArgsKey builds the working-set registry key for one call's
// arguments: canonical JSON (encoding/json sorts map keys, so the form is
// canonical for free) minus the identity-only and conditional-fetch args —
// the F8 set, stripped by StripServeTimeArgs. max_output is part of the key
// (F-2): the etag is view-bound, so each serve view records its own
// working-set entry. Identical asks share one registry entry across
// etag/no_cache/agent_id/task variance.
func CanonicalArgsKey(args map[string]any) string {
	canon, _ := json.Marshal(StripServeTimeArgs(args))
	h := sha256.New()
	_, _ = h.Write(canon)
	return hex.EncodeToString(h.Sum(nil))
}

// Entry is one working-set registry row: the tool that answered, the args
// digest for that ask, the etag the caller was handed, and when.
type Entry struct {
	Tool  string    // eligible tool name (kern_context, ...)
	Args  string    // canonical args digest (CanonicalArgsKey)
	ETag  string    // etag served for that ask
	Stamp time.Time // when the etag was last served
}

// Registry bounds (ADR-0012): a small, strictly bounded working set per
// agent. 256 entries per agent covers a working session of distinct reads;
// 64 agents covers a busy multi-agent host. Both are LRU-pruned.
const (
	// MaxEntriesPerAgent bounds one agent's working set.
	MaxEntriesPerAgent = 256
	// MaxAgents bounds the number of tracked agents.
	MaxAgents = 64
)

// agentlessKey is the registry key for calls that carry no agent_id. Agent
// identity flows per-call today (argString(args,"agent_id")); empty maps
// here so un-attributed reads still populate the working set.
const agentlessKey = "_"

// Registry is a per-agent working set: map[agentID] -> {argsDigest -> Entry},
// LRU-bounded per agent and across agents, mutex-guarded. The zero value is
// not ready to use — construct with NewRegistry (or use Default).
type Registry struct {
	mu         sync.Mutex
	byAgent    map[string]map[string]Entry
	entryOrder map[string][]string // per-agent args digests, MRU first
	agentOrder []string            // agent ids, MRU first
}

// Default is the process-wide working-set registry shared by the mcp root
// and the CLI. It mirrors the D1 cache's package-global LRU pattern.
var Default = NewRegistry()

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		byAgent:    map[string]map[string]Entry{},
		entryOrder: map[string][]string{},
	}
}

// Record stores (or refreshes) one working-set entry for agentID, promoting
// it to MRU and evicting LRU entries when the per-agent or cross-agent
// bounds are exceeded. Concurrent calls are safe; the registry never grows
// beyond MaxAgents * MaxEntriesPerAgent entries.
func (r *Registry) Record(agentID string, e Entry) {
	if agentID == "" {
		agentID = agentlessKey
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entries, ok := r.byAgent[agentID]
	if !ok {
		entries = map[string]Entry{}
		r.byAgent[agentID] = entries
	}
	// Promote the agent on EVERY record (true MRU semantics, matching the
	// "agent ids, MRU first" doc comment): an existing agent that records
	// new entries must refresh its position, so the longest-IDLE agent — not
	// the longest-created one — is evicted at the cross-agent bound (LOW-6).
	r.promoteAgentLocked(agentID)
	for len(r.agentOrder) > MaxAgents {
		tail := r.agentOrder[len(r.agentOrder)-1]
		r.agentOrder = r.agentOrder[:len(r.agentOrder)-1]
		delete(r.byAgent, tail)
		delete(r.entryOrder, tail)
	}
	entries[e.Args] = e
	r.promoteEntryLocked(agentID, e.Args)
	order := r.entryOrder[agentID]
	for len(order) > MaxEntriesPerAgent {
		tail := order[len(order)-1]
		order = order[:len(order)-1]
		delete(r.byAgent[agentID], tail)
	}
	r.entryOrder[agentID] = order
}

// promoteAgentLocked moves an agent id to the MRU front. Caller holds r.mu.
func (r *Registry) promoteAgentLocked(id string) {
	for i, a := range r.agentOrder {
		if a == id {
			r.agentOrder = append(r.agentOrder[:i], r.agentOrder[i+1:]...)
			break
		}
	}
	r.agentOrder = append([]string{id}, r.agentOrder...)
}

// promoteEntryLocked moves an args digest to the MRU front within its agent.
// Caller holds r.mu.
func (r *Registry) promoteEntryLocked(agent, key string) {
	order := r.entryOrder[agent]
	for i, k := range order {
		if k == key {
			order = append(order[:i], order[i+1:]...)
			break
		}
	}
	r.entryOrder[agent] = append([]string{key}, order...)
}

// Entries returns a copy of the agent's working set, most-recently-used
// first, so callers can render or inspect it without holding the lock. A
// fresh copy also makes the return safe to mutate.
func (r *Registry) Entries(agentID string) []Entry {
	if agentID == "" {
		agentID = agentlessKey
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := r.byAgent[agentID]
	out := make([]Entry, 0, len(entries))
	for _, key := range r.entryOrder[agentID] {
		if e, ok := entries[key]; ok {
			out = append(out, e)
		}
	}
	return out
}

// Count returns the number of entries currently tracked for an agent (0 when
// none). Test-visible bound check.
func (r *Registry) Count(agentID string) int {
	return len(r.Entries(agentID))
}

// AgentCount returns the number of agents currently tracked.
func (r *Registry) AgentCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byAgent)
}

// Render renders the working-set listing for an agent: one line per entry
// with tool, args digest, etag and timestamp, newest first. It is the body
// of the kern_meta "my working set" / "workingset" route.
func (r *Registry) Render(agentID string) string {
	entries := r.Entries(agentID)
	if len(entries) == 0 {
		return "[kern] working set: empty (no conditional-fetch reads recorded yet for this agent)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[kern] working set (%d entries):\n", len(entries))
	for _, e := range entries {
		fmt.Fprintf(&b, "%s args=%s etag=%s ts=%s\n", e.Tool, e.Args, e.ETag, e.Stamp.UTC().Format(time.RFC3339))
	}
	return b.String()
}

// RenderWorkingset renders the process-wide working set for a kern_meta
// request. It is the hook the mcp root wires into meta.Handle for the
// workingset route; agentID "" renders the un-attributed bucket.
func RenderWorkingset(agentID string) (string, error) {
	return Default.Render(agentID), nil
}
