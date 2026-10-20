package intel

import "github.com/JayveerPrajapati/kern/internal/index"

func callerDefinitions(ix *index.Index, caller string, target index.Symbol) ([]index.Symbol, bool) {
	sourced := false
	for _, edge := range ix.Calls[caller] {
		if edge.SourceFile != "" || edge.Synth != "" {
			sourced = true
			break
		}
	}
	if !sourced {
		if symbol, ok := findCallerDef(ix, caller, target); ok {
			return []index.Symbol{symbol}, false
		}
		return nil, true
	}
	callers, ambiguous := ix.CallerDeclarations(caller, target)
	for _, edge := range ix.Calls[caller] {
		if edge.Dynamic {
			continue
		}
		if edge.SourceFile != "" || edge.Synth != "" || simpleName(edge.Target) != target.Name {
			continue
		}
		if definition, found := findCallerDef(ix, caller, target); found {
			callers = append(callers, definition)
		} else {
			ambiguous = true
		}
	}
	return callers, ambiguous
}
