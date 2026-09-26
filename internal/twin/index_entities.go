// Index-driven entity derivation. The twin extractors are regex heuristics
// that only fire on the framework patterns they know, so on a real repo they
// mostly surface test fixtures. The index itself carries richer, verified
// framework entry-point metadata (Symbol.Entry / Route / Framework,
// populated by internal/index/entries.go AST detection) — real production
// routes like mux.HandleFunc("/mcp", srv.handleHTTP). This derivation
// surfaces that metadata as api entity nodes linked to the real handler
// symbols, and as package-scoped service entities, so symbol-connected
// entity lookups work on the repo's own index rather than only on
// extractor-shaped fixtures.

package twin

import (
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/twin/ids"
)

// indexEntities augments a merged graph with entities derived from the
// index's framework entry-point metadata:
//
//   - one api entity per entry-point symbol with a route (the real endpoint
//     the handler serves), linked to the symbol via "implements" and to its
//     file via "defined_in" — the same edge kinds the api extractor emits;
//   - one service entity per package that hosts entry points (or defines a
//     broker-like type such as eventbus.Bus), linked to its endpoints via
//     "serves" and to the entry symbols via "implements".
//
// Everything is derived from the index itself, so it is deterministic and
// needs no extra I/O. Duplicate api entities (same framework+method+path
// registered in several files) collapse by ID.
func indexEntities(ix *index.Index, g *intel.Graph) {
	if ix == nil || g == nil {
		return
	}
	// file -> package path, mirroring FromIndex's node-ID scoping so the
	// "implements" edges point at the exact symbol node IDs in the graph.
	pkgByFile := map[string]string{}
	for path, pkg := range ix.Pkgs {
		for _, f := range pkg.Files {
			pkgByFile[f] = path
		}
	}
	nodeID := func(s index.Symbol) string {
		p := pkgByFile[s.File]
		if p == "" {
			p = filepath.Dir(s.File)
		}
		if p == "" || p == "." {
			return s.FullName()
		}
		return p + "." + s.FullName()
	}

	seen := map[string]bool{}
	pkgAPIs := map[string][]string{}    // package -> api entity IDs it serves
	pkgEntries := map[string][]string{} // package -> entry symbol node IDs

	addNode := func(n domain.Node) {
		if seen[n.ID] {
			return
		}
		seen[n.ID] = true
		g.Nodes = append(g.Nodes, n)
	}
	addEdge := func(from, to, kind string) {
		for _, e := range g.Edges {
			if e.From == from && e.To == to && e.Kind == kind {
				return
			}
		}
		g.Edges = append(g.Edges, domain.Edge{From: from, To: to, Kind: kind})
	}

	// Pass 1: api entities from entry-point symbols that carry a route.
	for _, s := range ix.Symbols {
		if !s.Entry && s.Route == "" && s.Framework == "" {
			continue
		}
		if s.Route == "" {
			// A framework handler without a route (e.g. Server.handleHealth
			// referenced from a closure) is not itself an endpoint; its
			// package service covers it.
			continue
		}
		fw := s.Framework
		if fw == "" {
			fw = "http"
		}
		// The index records route + framework, not an HTTP method; GET is
		// the default convention the api extractor uses for frameworks
		// that do not encode the method (flask/spring).
		method := "GET"
		path := s.Route
		apiID := "api:index:" + fw + ":" + method + ":" + ids.Escape(path)
		if seen[apiID] {
			continue
		}
		symID := nodeID(s)
		addNode(domain.Node{
			ID:    apiID,
			Kind:  "api",
			Label: method + " " + path,
			API: &domain.API{
				ID:        apiID,
				Name:      method + " " + path,
				Method:    method,
				Path:      path,
				Symbol:    s.FullName(),
				Framework: fw,
				File:      s.File,
				Line:      s.Line,
			},
		})
		addEdge(apiID, symID, "implements")
		if s.File != "" {
			addEdge(apiID, "file:"+s.File, "defined_in")
		}
		if pkg := pkgByFile[s.File]; pkg != "" && pkg != "." {
			pkgAPIs[pkg] = append(pkgAPIs[pkg], apiID)
			pkgEntries[pkg] = append(pkgEntries[pkg], symID)
		}
	}

	// Pass 2: broker-like structs (in-process buses, queues) that carry no
	// HTTP surface still represent a service — e.g. eventbus.Bus, whose
	// methods (Bus.enqueueDeadLetter) must resolve to the bus service.
	brokerPkgs := map[string]string{} // package -> struct node ID
	for _, s := range ix.Symbols {
		if s.Kind != "struct" {
			continue
		}
		if !isBrokerTypeName(s.Name) {
			continue
		}
		pkg := pkgByFile[s.File]
		if pkg == "" || pkg == "." {
			continue
		}
		brokerPkgs[pkg] = nodeID(s)
	}

	// Pass 3: one service entity per package with endpoints or a broker
	// type. The service is the package as a deployable unit — the same
	// package-as-service model WhatServicesAffected uses for modules.
	packages := map[string]bool{}
	for pkg := range pkgAPIs {
		packages[pkg] = true
	}
	for pkg := range brokerPkgs {
		packages[pkg] = true
	}
	for pkg := range packages {
		svcID := "service:pkg:" + ids.Escape(pkg)
		addNode(domain.Node{
			ID:    svcID,
			Kind:  "service",
			Label: pkg,
			Service: &domain.Service{
				ID:   svcID,
				Name: pkg,
				Path: pkg,
				Type: "package",
			},
		})
		for _, apiID := range pkgAPIs[pkg] {
			addEdge(svcID, apiID, "serves")
		}
		for _, symID := range pkgEntries[pkg] {
			addEdge(svcID, symID, "implements")
		}
		if structID, ok := brokerPkgs[pkg]; ok {
			addEdge(svcID, structID, "implements")
		}
	}
}

// isBrokerTypeName reports whether a struct name denotes an in-process
// message broker/queue (eventbus.Bus, MemoryBus, JobQueue, ...). Such types
// are services even though they expose no HTTP surface.
func isBrokerTypeName(name string) bool {
	switch {
	case name == "Bus":
		return true
	case strings.HasSuffix(name, "Bus"):
		return true
	case strings.HasSuffix(name, "Broker"):
		return true
	case strings.HasSuffix(name, "Queue"):
		return true
	}
	return false
}
