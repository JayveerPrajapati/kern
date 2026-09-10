// Package host implements the silent host-adapter pipeline: it injects a
// compact context block derived from a domain.ContextPacket into a host
// agent's instruction file (CLAUDE.md, AGENTS.md, .cursor rules, GitHub
// copilot instructions), so the LLM has the context before it needs to ask.
// Everything is deterministic file I/O — no LLM, no network.
package host

import (
	"fmt"
	"strings"
	"sync"

	kernbudget "github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Adapter silently injects a compact context block derived from a
// domain.ContextPacket into a host agent's instruction file, so the LLM has
// the context before it needs to ask. Everything is deterministic file I/O.
type Adapter interface {
	Name() string                                                              // e.g. "claude"
	FilePath(root string) string                                               // instruction file it manages
	Detect(root string) bool                                                   // FilePath exists
	Inject(root string, pkt *domain.ContextPacket, budget int) (string, error) // writes block; returns the block text
	Extract(root string) (string, error)                                       // current injected block ("" = none)
	Uninstall(root string) error                                               // removes only its own block
}

// blockMarkers returns the HTML comment markers delimiting an adapter's
// injected block. HTML comments are safe in Markdown instruction files.
func blockMarkers(name string) (start, end string) {
	return fmt.Sprintf("<!-- kern-host:%s:start -->", name),
		fmt.Sprintf("<!-- kern-host:%s:end -->", name)
}

// RenderSummary renders the compact context block for injection:
//
//	# kern context (<tasktype/task first line, capped 80 runes>)
//	- fact: <first 3 facts, statement capped 120 runes each>
//	- risk: <first 2 risks, capped 120 runes>
//	- validate: <first 3 RequiredValidation entries, capped 80 runes>
//	- tokens: <pkt.TokenCount>
//
// The rendered block is then fitted with budget.Fit (budget <= 0 → 1200).
// A nil packet renders an empty block.
func RenderSummary(pkt *domain.ContextPacket, budget int) string {
	if pkt == nil {
		return ""
	}
	capTokens := 1200
	if budget > 0 {
		capTokens = budget
	}
	var b strings.Builder
	title := firstLine(pkt.Task, 80)
	if title == "" {
		title = "context"
	}
	fmt.Fprintf(&b, "# kern context (%s)\n", title)
	for i, c := range pkt.Facts {
		if i >= 3 {
			break
		}
		fmt.Fprintf(&b, "- fact: %s\n", capRunes(c.Statement, 120))
	}
	for i, r := range pkt.Risks {
		if i >= 2 {
			break
		}
		fmt.Fprintf(&b, "- risk: %s\n", capRunes(string(r.Level), 120))
	}
	for i, v := range pkt.RequiredValidation {
		if i >= 3 {
			break
		}
		fmt.Fprintf(&b, "- validate: %s\n", capRunes(v, 80))
	}
	fmt.Fprintf(&b, "- tokens: %d\n", pkt.TokenCount)
	return kernbudget.Fit(b.String(), capTokens)
}

// Registry holds the ordered set of host adapters. Reads are safe for
// concurrent use.
type Registry struct {
	mu       sync.RWMutex
	adapters []Adapter
}

// NewRegistry registers all five built-in adapters in order: opencode,
// claude, cursor, copilot, codex.
func NewRegistry() *Registry {
	r := &Registry{}
	for _, a := range []Adapter{
		NewOpenCodeAdapter(),
		NewClaudeAdapter(),
		NewCursorAdapter(),
		NewCopilotAdapter(),
		NewCodexAdapter(),
	} {
		r.Register(a)
	}
	return r
}

// Adapters returns a copy of the registered adapters in registry order.
func (r *Registry) Adapters() []Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Adapter(nil), r.adapters...)
}

// Select returns the adapters whose instruction file exists, in registry
// order.
func (r *Registry) Select(root string) []Adapter {
	var out []Adapter
	for _, a := range r.Adapters() {
		if a.Detect(root) {
			out = append(out, a)
		}
	}
	return out
}

// Register appends an adapter to the registry.
func (r *Registry) Register(a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters = append(r.adapters, a)
}

// firstLine returns the first line of s, capped at n runes.
func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return capRunes(s, n)
}

// capRunes truncates s to at most n runes.
func capRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
