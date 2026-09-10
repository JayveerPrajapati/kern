package host

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Warning is a non-fatal adapter failure: the adapter is skipped but the rest
// of the pipeline continues.
type Warning struct {
	Adapter string `json:"adapter"`
	Message string `json:"message"`
}

// Pipeline drives the adapter lifecycle (inject / dry-run / check / remove)
// across the registry.
type Pipeline struct {
	Reg *Registry
}

// NewPipeline builds a pipeline over the given registry.
func NewPipeline(reg *Registry) *Pipeline {
	return &Pipeline{Reg: reg}
}

// InjectAll injects into every detected adapter (Select(root)). Errors become
// Warnings (adapter skipped — graceful fallback); successful injections are
// NOT warnings. Returns warnings (nil slice when all clean).
func (p *Pipeline) InjectAll(root string, pkt *domain.ContextPacket, budget int) []Warning {
	var warns []Warning
	for _, a := range p.Reg.Select(root) {
		if _, err := a.Inject(root, pkt, budget); err != nil {
			warns = append(warns, Warning{Adapter: a.Name(), Message: err.Error()})
		}
	}
	return warns
}

// DryRun renders the plan WITHOUT writing: for each adapter (whether or not
// detected), one line "<name>: <filepath> [detected|not detected]", plus,
// when pkt != nil, the rendered block under each adapter line (indented).
func (p *Pipeline) DryRun(root string, pkt *domain.ContextPacket, budget int) string {
	var b strings.Builder
	for _, a := range p.Reg.Adapters() {
		det := "not detected"
		if a.Detect(root) {
			det = "detected"
		}
		fmt.Fprintf(&b, "%s: %s [%s]\n", a.Name(), a.FilePath(root), det)
		if pkt != nil {
			for _, line := range strings.Split(RenderSummary(pkt, budget), "\n") {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
	}
	return b.String()
}

// Check reports the current injection state without writing:
// "<name>: <filepath> [detected|missing] [injected:<N bytes>|clean]".
func (p *Pipeline) Check(root string) string {
	var b strings.Builder
	for _, a := range p.Reg.Adapters() {
		state := "missing"
		if a.Detect(root) {
			state = "detected"
		}
		block := ""
		if a.Detect(root) {
			block, _ = a.Extract(root)
		}
		inj := "clean"
		if block != "" {
			inj = fmt.Sprintf("injected:%d", len(block))
		}
		fmt.Fprintf(&b, "%s: %s [%s] [%s]\n", a.Name(), a.FilePath(root), state, inj)
	}
	return b.String()
}

// UninstallAll removes blocks from every detected adapter. Errors become
// Warnings; adapters without a block are no-ops.
func (p *Pipeline) UninstallAll(root string) []Warning {
	var warns []Warning
	for _, a := range p.Reg.Select(root) {
		if err := a.Uninstall(root); err != nil {
			warns = append(warns, Warning{Adapter: a.Name(), Message: err.Error()})
		}
	}
	return warns
}
