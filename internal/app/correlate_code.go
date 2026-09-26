// Incident → twin → code correlation engine (Feature Batch D): extends the
// existing alert→runtime-evidence correlation (runtime.Correlator) with the
// code dimension — the affected service is resolved through the twin-merged
// knowledge graph to the implicated source files and symbols. Deterministic,
// no LLM, no network. Lives in internal/app because it needs twin + incident
// + runtime together (app's ARCHITECTURE.md allowed deps already include all
// three); internal/incident's dep list does not include twin.
package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/incident"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// Correlation is the incident→twin→code correlation report for an alert:
// the affected service, the existing runtime evidence, the twin-resolved
// implicated source files and symbols, a deterministic confidence, and any
// auto-attached heal playbook.
type Correlation struct {
	Alert             domain.Alert
	AffectedService   string
	RuntimeEvidence   []string // formatted runtime evidence (errors/deployments/chain)
	ImplicatedFiles   []string // root-relative source files implicated by the service
	ImplicatedSymbols []string // symbols defined in (or referenced by) the implicated code
	Confidence        string   // "high" (exact service→code mapping) or "low" (heuristic)
	PlaybookSignature string   // auto-attached heal playbook signature (empty = none)
	PlaybookSteps     []string // auto-attached playbook steps
}

// CorrelateIncident resolves an alert to the affected service and derives
// the implicated code (twin graph entities → source files → symbols),
// extending the runtime correlation path. It is the engine entry point the
// CLI `kern incident --correlate` and MCP kern_incident correlate=true share.
// An unknown service yields a graceful empty correlation with confidence
// "low", never an error.
func CorrelateIncident(root string, alert domain.Alert) (Correlation, error) {
	p, err := New(root)
	if err != nil {
		return Correlation{}, err
	}
	return p.CorrelateCode(alert)
}

// CorrelateCode runs the incident→twin→code correlation on the platform's
// twin-merged graph. The runtime dimension reuses the shared correlator so
// every lane reasons over the same source/window as Correlate/Investigate.
func (p *Platform) CorrelateCode(alert domain.Alert) (Correlation, error) {
	src := p.RuntimeSource()
	if src == nil {
		src = runtime.NewStore()
	}
	shared := runtime.NewSharedCorrelator(src, incident.DefaultLookback)
	corr := shared.Correlate(alert)
	chain := shared.CorrelateChain(alert)

	out := Correlation{
		Alert:           alert,
		AffectedService: corr.AffectedService,
	}
	out.RuntimeEvidence = runtimeEvidenceLines(corr, src.Name())
	if corr.AffectedService != "" {
		files, syms, conf := p.serviceToCode(corr.AffectedService, chain)
		out.ImplicatedFiles = files
		out.ImplicatedSymbols = syms
		out.Confidence = conf
	} else {
		out.Confidence = "low"
	}

	// Auto-attach the heal playbook matching the incident's signature (the
	// same lookup the incident engine's IngestAlert performs, so the
	// correlation report and the incident report agree).
	inc := domain.Incident{Alert: alert, AffectedService: corr.AffectedService, Title: alert.Message}
	if pb, ok := incident.FindPlaybook(p.Root(), inc); ok {
		out.PlaybookSignature = pb.Signature
		out.PlaybookSteps = pb.Steps
	}
	return out, nil
}

// serviceToCode maps an affected service name to the implicated source files
// and symbols through the twin-merged graph, deterministically:
//
//  1. Exact mapping (confidence "high"): the service name treated as a
//     root-relative directory — every graph "file" node under <service>/ is
//     implicated. This is the exact service→code mapping.
//  2. Heuristic mapping (confidence "low"): twin entity nodes (service, api,
//     error, deployment — from the twin extractors) owned by the service
//     whose edges reach "file" nodes.
//
// Unknown services (no directory, no twin entities) return empty sets with
// confidence "low" and never error. Results are deduplicated and sorted.
// The runtime chain's already-resolved symbol links are folded in so the
// report never drops evidence the runtime correlation found.
func (p *Platform) serviceToCode(svc string, chain runtime.CorrelationChain) (files, syms []string, confidence string) {
	svc = strings.Trim(filepath.ToSlash(svc), "/")
	if svc == "" {
		return nil, nil, "low"
	}
	prefix := svc + "/"

	fileSet := map[string]bool{}
	var exact, heuristic []string
	addFile := func(list []string, fp string) []string {
		if !fileSet[fp] {
			fileSet[fp] = true
			list = append(list, fp)
		}
		return list
	}

	// 1) Exact directory mapping from graph file nodes.
	for _, n := range p.Graph().Nodes {
		if n.Kind != "file" || n.File == nil {
			continue
		}
		fp := filepath.ToSlash(n.File.Path)
		if fp == svc || strings.HasPrefix(fp, prefix) {
			exact = addFile(exact, fp)
		}
	}

	// 2) Twin entity mapping: service-owned nodes (service/api/deployment)
	// and error nodes whose edges reach file nodes ("defined_in"/"caused_by").
	twinFiles := map[string]bool{}
	for _, n := range p.Graph().Nodes {
		owned := false
		switch {
		case n.Kind == "service" && n.Service != nil && n.Service.Name == svc:
			owned = true
		case n.Kind == "api" && n.API != nil && n.API.Service == svc:
			owned = true
		case n.Kind == "deployment" && strings.Contains(n.Label, svc):
			owned = true
		case n.Kind == "error":
			owned = true // error nodes link to files via caused_by edges below
		}
		if !owned {
			continue
		}
		for _, e := range p.Graph().Edges {
			if e.From == n.ID && strings.HasPrefix(e.To, "file:") {
				if fp := strings.TrimPrefix(e.To, "file:"); fp != "" {
					twinFiles[fp] = true
				}
			}
		}
	}
	for fp := range twinFiles {
		heuristic = addFile(heuristic, fp)
	}

	// 3) Implicated symbols: graph symbol nodes defined in an implicated
	// file, plus the deterministic symbol references the runtime correlation
	// chain already resolved.
	symSet := map[string]bool{}
	for _, n := range p.Graph().Nodes {
		if n.Kind != "symbol" || n.Symbol == nil {
			continue
		}
		if fileSet[filepath.ToSlash(n.Symbol.File)] {
			id := n.ID
			if id == "" {
				id = n.Symbol.Qualified
			}
			if id != "" {
				symSet[id] = true
			}
		}
	}
	for _, l := range chain.Links {
		if l.Stage == "symbol" {
			symSet[l.ID] = true
		}
	}

	if len(exact) > 0 {
		confidence = "high"
	} else {
		confidence = "low"
	}
	merged := append(exact, heuristic...)
	sort.Strings(merged)
	files = slices.Compact(merged)

	syms = make([]string, 0, len(symSet))
	for s := range symSet {
		syms = append(syms, s)
	}
	sort.Strings(syms)
	return files, syms, confidence
}

// runtimeEvidenceLines renders the runtime evidence as deterministic lines:
// the existing runtime-evidence summary plus the error events in the window.
func runtimeEvidenceLines(corr runtime.Correlation, source string) []string {
	var out []string
	var depl []string
	for _, d := range corr.Deployments {
		depl = append(depl, d.Version+"@"+shortSHA8(d.CommitSHA))
	}
	line := fmt.Sprintf("%d errors, %d logs, %d metrics, %d spans; deployments %v",
		len(corr.ErrorEvents), len(corr.LogEvents), len(corr.MetricEvents), len(corr.TraceSpans), depl)
	if source != "" {
		line = source + ": " + line
	}
	out = append(out, line)
	for _, e := range corr.ErrorEvents {
		out = append(out, "error "+e.ID+": "+e.Message)
	}
	return out
}

func shortSHA8(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}
