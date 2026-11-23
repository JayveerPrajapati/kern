package research

import (
	"context"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	mcpgov "github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
	kernresearch "github.com/JayveerPrajapati/kern/internal/research"
)

// Research implements kern_research: a deterministic, read-only bundle for a
// multi-symbol task. The adapter runs authorization once for the bundle, then
// filters every displayed anchor/edge/path through the same Governor contract
// used by the direct graph tools. It performs no writes, commands, or network.
func Research(ctx context.Context, ix *index.Index, gvc mcpgov.GovContext, args map[string]any) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	task := mcpargs.ArgString(args, "task")
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	maxTokens := 6000
	if v := mcpargs.ArgString(args, "max_tokens"); v != "" {
		n, err := mcpargs.AtoiArg(v, maxTokens)
		if err != nil {
			return "", err
		}
		maxTokens = n
	}

	report := kernresearch.Research(ix, task, maxTokens)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	gov, err := gvc.NewGov()
	if err != nil {
		gvc.StampGov(gov, nil)
		return "", err
	}
	if gov != nil {
		filterResearchReport(ix, gov, report)
	}
	applyResearchConfidence(ix, report, mcpargs.ArgString(args, "min_confidence"))

	text := kernresearch.RenderResearch(report)
	if report.Truncated {
		text = kernresearch.FitResearch(text, maxTokens)
	}
	names := researchSymbolNames(report)
	if gov != nil {
		gvc.StampGov(gov, provenance.SymbolProvenances(ix, names))
	} else {
		gvc.StampRaw(provenance.SymbolProvenances(ix, names))
	}
	return text, nil
}

func filterResearchReport(ix *index.Index, gov *mcpgov.Governor, report *kernresearch.ResearchReport) {
	kept := make([]kernresearch.ResearchAnchor, 0, len(report.Anchors))
	for _, anchor := range report.Anchors {
		if !gov.NameAllowed(ix, anchor.Resolved) {
			continue
		}
		anchor.Callers = filterResearchNames(ix, gov, anchor.Callers)
		anchor.Callees = filterResearchNames(ix, gov, anchor.Callees)
		anchor.Tests = filterResearchNames(ix, gov, anchor.Tests)
		anchor.Impact = filterResearchNames(ix, gov, anchor.Impact)
		anchor.AffectedFiles = filterResearchFiles(ix, gov, anchor.AffectedFiles)
		kept = append(kept, anchor)
	}
	report.Anchors = kept

	paths := make([][]string, 0, len(report.Paths))
	for _, path := range report.Paths {
		allowed := true
		for _, name := range path {
			if !gov.NameAllowed(ix, name) {
				allowed = false
				break
			}
		}
		if allowed {
			paths = append(paths, path)
		}
	}
	report.Paths = paths
}

func filterResearchFiles(ix *index.Index, gov *mcpgov.Governor, files []string) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		for _, sym := range ix.Symbols {
			if sym.File == file && gov.NameAllowed(ix, sym.FullName()) {
				out = append(out, file)
				break
			}
		}
	}
	return out
}

func filterResearchNames(ix *index.Index, gov *mcpgov.Governor, names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if gov.NameAllowed(ix, name) {
			out = append(out, name)
		}
	}
	return out
}

func applyResearchConfidence(ix *index.Index, report *kernresearch.ResearchReport, minimum string) {
	if strings.TrimSpace(minimum) == "" {
		return
	}
	passes := intel.MinConfidenceFilter(minimum)
	for i := range report.Anchors {
		anchor := &report.Anchors[i]
		anchor.Callers = keepResearchEdges(anchor.Callers, func(name string) bool {
			return passes(intel.EdgeConfidenceLabel(ix, name, anchor.Resolved))
		})
		anchor.Callees = keepResearchEdges(anchor.Callees, func(name string) bool {
			return passes(intel.EdgeConfidenceLabel(ix, anchor.Resolved, name))
		})
	}
}

func keepResearchEdges(names []string, keep func(string) bool) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if keep(name) {
			out = append(out, name)
		}
	}
	return out
}

func researchSymbolNames(report *kernresearch.ResearchReport) []string {
	names := make([]string, 0, 16)
	for _, anchor := range report.Anchors {
		names = append(names, anchor.Resolved)
		names = append(names, anchor.Callers...)
		names = append(names, anchor.Callees...)
		names = append(names, anchor.Tests...)
		names = append(names, anchor.Impact...)
	}
	for _, path := range report.Paths {
		names = append(names, path...)
	}
	return names
}
