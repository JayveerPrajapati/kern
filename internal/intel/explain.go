package intel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Explanation summarizes how a symbol, subsystem, or workflow works end-to-end.
type Explanation struct {
	Subject       string   `json:"subject"`
	Kind          string   `json:"kind"` // "symbol" | "subsystem" | "workflow"
	Definition    string   `json:"definition,omitempty"`
	File          string   `json:"file,omitempty"`
	Line          int      `json:"line,omitempty"`
	Subsystem     string   `json:"subsystem,omitempty"`
	DirectCallers []string `json:"direct_callers,omitempty"`
	Callees       []string `json:"callees,omitempty"`
	ExecutionFlow []string `json:"execution_flow,omitempty"`
	Summary       string   `json:"summary"`
}

// Explain produces an end-to-end architectural and call-flow explanation of subject.
func Explain(ix *index.Index, subject string) (*Explanation, error) {
	if subject == "" {
		return nil, fmt.Errorf("subject is required")
	}

	exp := &Explanation{Subject: subject}

	// 1. Try resolving subject as a symbol
	var targetSym *index.Symbol
	for _, sym := range ix.Symbols {
		if sym.Name == subject || sym.FullName() == subject {
			targetSym = &sym
			break
		}
	}
	if targetSym == nil {
		matches := ix.Search(subject, 1)
		if len(matches) > 0 {
			targetSym = &matches[0]
		}
	}

	if targetSym != nil {
		exp.Kind = "symbol"
		exp.Definition = fmt.Sprintf("%s %s", targetSym.Kind, targetSym.FullName())
		exp.File = targetSym.File
		exp.Line = targetSym.Line
		exp.DirectCallers = ix.CallersOf(targetSym.FullName())
		exp.Callees = ix.CallSites(targetSym.FullName())

		// Trace short execution flow downstream (up to 3 hops)
		exp.ExecutionFlow = traceFlow(ix, targetSym.FullName(), 3)

		// Determine subsystem from directory
		exp.Subsystem = filepath.Dir(targetSym.File)
		exp.Summary = fmt.Sprintf("Symbol %s is a %s declared in %s:%d (subsystem %s). It is invoked by %d direct callers and delegates work to %d callees.",
			targetSym.FullName(), targetSym.Kind, targetSym.File, targetSym.Line, exp.Subsystem, len(exp.DirectCallers), len(exp.Callees))
		return exp, nil
	}

	// 2. Check if subject matches a subsystem directory
	var subsystemSymbols []index.Symbol
	for _, sym := range ix.Symbols {
		if strings.Contains(sym.File, subject) {
			subsystemSymbols = append(subsystemSymbols, sym)
		}
	}

	if len(subsystemSymbols) > 0 {
		exp.Kind = "subsystem"
		exp.Subsystem = subject
		var entryPoints []string
		for _, s := range subsystemSymbols {
			if s.Entry || isEntryPoint(s.Name) || len(ix.CallersOf(s.FullName())) == 0 {
				entryPoints = append(entryPoints, s.FullName())
			}
		}
		sort.Strings(entryPoints)
		if len(entryPoints) > 10 {
			entryPoints = entryPoints[:10]
		}
		exp.Callees = entryPoints
		exp.Summary = fmt.Sprintf("Subsystem '%s' contains %d indexed symbols. Primary entry points: %s.",
			subject, len(subsystemSymbols), strings.Join(entryPoints, ", "))
		return exp, nil
	}

	return nil, fmt.Errorf("could not resolve symbol or subsystem %q", subject)
}

func traceFlow(ix *index.Index, start string, maxDepth int) []string {
	var flow []string
	visited := map[string]bool{start: true}
	curr := start

	for d := 0; d < maxDepth; d++ {
		callees := ix.CallSites(curr)
		var next string
		for _, c := range callees {
			if !visited[c] {
				next = c
				visited[c] = true
				break
			}
		}
		if next == "" {
			break
		}
		flow = append(flow, next)
		curr = next
	}
	return flow
}

// Render formats the Explanation into a readable narrative.
func (e *Explanation) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ARCHITECTURE NARRATIVE: %s [%s]\n", e.Subject, strings.ToUpper(e.Kind))
	fmt.Fprintf(&b, "========================================================\n")
	fmt.Fprintf(&b, "%s\n\n", e.Summary)

	if e.Definition != "" {
		fmt.Fprintf(&b, "Declaration: %s (%s:%d)\n", e.Definition, e.File, e.Line)
	}

	if len(e.DirectCallers) > 0 {
		fmt.Fprintf(&b, "\nWho calls this (%d):\n", len(e.DirectCallers))
		for i, c := range e.DirectCallers {
			if i >= 10 {
				fmt.Fprintf(&b, "  … and %d more callers\n", len(e.DirectCallers)-10)
				break
			}
			fmt.Fprintf(&b, "  ← %s\n", c)
		}
	}

	if len(e.Callees) > 0 {
		label := "What this calls"
		if e.Kind == "subsystem" {
			label = "Key Entry Points"
		}
		fmt.Fprintf(&b, "\n%s (%d):\n", label, len(e.Callees))
		for i, c := range e.Callees {
			if i >= 10 {
				fmt.Fprintf(&b, "  … and %d more\n", len(e.Callees)-10)
				break
			}
			fmt.Fprintf(&b, "  → %s\n", c)
		}
	}

	if len(e.ExecutionFlow) > 0 {
		fmt.Fprintf(&b, "\nDownstream Execution Chain:\n")
		fmt.Fprintf(&b, "  %s → %s\n", e.Subject, strings.Join(e.ExecutionFlow, " → "))
	}

	return strings.TrimSpace(b.String())
}
