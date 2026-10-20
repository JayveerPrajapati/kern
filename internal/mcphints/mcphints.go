// Package mcphints implements the tool-adoption engine (B6, anti-stall
// campaign Phase 4): in-band nudges inside tool responses that remind the
// agent of high-value tools it never calls. The model is already reading the
// response — that is where the reminder belongs. Purely advisory: never a
// denial, never alters a payload beyond one appended hint line. Deterministic,
// stdlib-only, no LLM in the loop.
//
// Two hint families: (tool, outcome) table hints (a successful kern_explore
// reminds that kern_why/kern_impact exist) and the usage-gap hint (a session
// that crosses GapThreshold read calls without touching any top-ranked
// underused tool gets one nudge naming the most underused one). Every hint
// fires at most once per session, so the footer can never nag.
package mcphints

import (
	"os"
	"strconv"
	"sync"
)

// Outcome classifies a tool response for hint selection.
type Outcome int

const (
	// OutcomeSuccess is a full-payload successful response.
	OutcomeSuccess Outcome = iota
	// OutcomeNoMatch is a successful response whose output signals "nothing
	// found" (e.g. kern_search's "no symbols matched:" text).
	OutcomeNoMatch
	// OutcomeFailure is a failed call (runErr != nil). Hints never ride error
	// payloads, so the mcp wire-in never emits for this outcome; the table
	// entry exists for completeness and unit-test coverage.
	OutcomeFailure
)

// GapThreshold is the session read-call count at/above which the usage-gap
// nudge may fire (10 read-phase calls with no underused-tool usage).
const GapThreshold = 10

// hintTable maps (tool, outcome) to the single hint line to append. Each
// entry fires at most once per session. The tool set follows the Step-0
// usage evidence and the campaign's known-underused set.
var hintTable = map[string]map[Outcome]string{
	"kern_explore": {OutcomeSuccess: "next: kern_why names who depends on this; kern_impact before editing"},
	"kern_plan":    {OutcomeSuccess: "next: recall prior lessons (kern_memory recall) before implementing"},
	"kern_verify":  {OutcomeFailure: "next: kern_heal can auto-repair failing files in a snapshot"},
	"kern_search":  {OutcomeNoMatch: "try kern_meta (NL router) or kern_buddy for orientation"},
}

// gapHint is one usage-gap nudge: the underused tool and the one-line why.
type gapHint struct {
	tool string
	line string
}

// gapOrder ranks the underused tools by the real per-tool usage stats (Step
// 0, `kern stats --by-tool` over the last 7 days: kern_semcache 2 calls,
// kern_why 8, kern_plan 12, kern_memory 28, kern_impact 31 — against
// 200-1500 for the read tools). The nudge names the FIRST ranked tool the
// session has never used.
var gapOrder = []gapHint{
	{"kern_semcache", "kern_semcache: similar queries may already be cached — check before re-asking"},
	{"kern_why", "kern_why: ask who depends on what you keep reading — it names the blast radius"},
	{"kern_plan", "kern_plan: emit a plan — it names the first step instead of more reads"},
	{"kern_memory", "kern_memory recall: cross-session lessons may already hold this answer"},
	{"kern_impact", "kern_impact: before editing, know what breaks"},
}

// Hints is the mutex-guarded per-session hint engine. The zero value is not
// ready to use — construct with New.
type Hints struct {
	mu    sync.Mutex
	fired map[string]map[string]bool // sessionKey -> set of fired hint IDs
	used  map[string]map[string]bool // sessionKey -> set of tools called
	reads map[string]int             // sessionKey -> read-phase call count
}

// New returns an empty hint engine.
func New() *Hints {
	return &Hints{
		fired: map[string]map[string]bool{},
		used:  map[string]map[string]bool{},
		reads: map[string]int{},
	}
}

// Enabled reports whether hints are active. KERN_HINTS=0 disables them; any
// other value (including unset) enables them — the same opt-out discipline as
// the D1 cache, served-ledger and loop-detector knobs.
func (h *Hints) Enabled() bool {
	return os.Getenv("KERN_HINTS") != "0"
}

// Select returns the (tool, outcome) table-hint line for a session — or ""
// when disabled, the hint already fired this session, or the table has no
// entry. Each (tool, outcome) hint fires at most once per session.
func (h *Hints) Select(sessionKey, tool string, outcome Outcome) string {
	if !h.Enabled() {
		return ""
	}
	line, ok := hintTable[tool][outcome]
	if !ok {
		return ""
	}
	id := tool + "\x00" + strconv.Itoa(int(outcome))
	h.mu.Lock()
	defer h.mu.Unlock()
	sess := h.fired[sessionKey]
	if sess == nil {
		sess = map[string]bool{}
		h.fired[sessionKey] = sess
	}
	if sess[id] {
		return ""
	}
	sess[id] = true
	return line
}

// Gap returns the usage-gap hint for a session — or "" when disabled, the
// session's read-call count is below GapThreshold, every top-ranked
// underused tool was already used, or the chosen tool's gap hint already
// fired this session. readCalls counts the session's read-phase tool calls;
// usedTools is the set of tool names the session has called.
func (h *Hints) Gap(sessionKey string, readCalls int, usedTools map[string]bool) string {
	if !h.Enabled() || readCalls < GapThreshold {
		return ""
	}
	for _, g := range gapOrder {
		if usedTools[g.tool] {
			continue
		}
		id := "gap:" + g.tool
		h.mu.Lock()
		sess := h.fired[sessionKey]
		if sess == nil {
			sess = map[string]bool{}
			h.fired[sessionKey] = sess
		}
		if sess[id] {
			h.mu.Unlock()
			return ""
		}
		sess[id] = true
		h.mu.Unlock()
		return g.line
	}
	return ""
}

// Track records one tool call for a session: the tool joins the session's
// used set, and a read-phase call increments the session's read counter. The
// usage-gap nudge consumes both.
func (h *Hints) Track(sessionKey, tool string, read bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.used[sessionKey] == nil {
		h.used[sessionKey] = map[string]bool{}
	}
	h.used[sessionKey][tool] = true
	if read {
		h.reads[sessionKey]++
	}
}

// Reads returns the session's tracked read-phase call count.
func (h *Hints) Reads(sessionKey string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads[sessionKey]
}

// Used returns the session's tracked used-tool set.
func (h *Hints) Used(sessionKey string) map[string]bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]bool{}
	for t := range h.used[sessionKey] {
		out[t] = true
	}
	return out
}
