package index

// synthesizers.go — dynamic-dispatch edge synthesis (CG-P0-1).
//
// Router-table registrations (http.HandleFunc, chi r.Get, gin engine.GET,
// echo e.GET, fiber app.Get) are invisible to the AST call graph: the setup
// function never syntactically calls the handler, so path/impact/probe stop
// at the framework boundary. This synthesizer reconnects that hop by
// emitting CallEdge{Target: handler, Confidence: Medium, Synth: "router:..."}
// from the enclosing top-level function to every handler it registers.
//
// Synthesized edges are MEDIUM (an inference, not a syntactic fact) and
// carry a Synth provenance marker so every renderer can distinguish them
// from AST-extracted edges (typed-claims principle).
//
// v1 scope (Go): net-http Handle/HandleFunc, chi/gin/echo/fiber verb and
// group methods on router-shaped variables. Python (FastAPI/Flask decorators)
// and JS (express/react) families are future per-family additions; the
// extension point is this function's matcher switch.

import (
	"go/ast"
)

// routerVarNames is the allowlist of variable names treated as routers for
// verb-method registrations (chi r.Get, gin engine.GET, echo e.GET, fiber
// app.Get). Deliberately conservative: a wrong guess costs one MEDIUM,
// Synth-marked edge (inspectable), while a false negative on stdlib calls
// like http.Get(url) would pollute the graph with phantom handlers.
var routerVarNames = map[string]bool{
	"r": true, "rt": true, "mux": true, "router": true,
	"engine": true, "e": true, "api": true, "app": true,
	"srv": true, "server": true, "h": true, "handler": true,
	"g": true, "group": true, "routes": true,
}

// chiGroupMethods are registration forms specific enough to accept on any
// router-shaped variable without the verb allowlist confusion (r.Use,
// r.Route, r.Mount, r.Group).
var chiGroupMethods = map[string]bool{
	"Use": true, "Route": true, "Mount": true, "Group": true,
}

// synthesizeDispatchEdges walks one top-level function body for router
// registrations and appends MEDIUM-confidence, Synth-marked edges to
// calls[owner]. Existing AST edges from the same owner to the same target
// win (a real syntactic call supersedes the synthesized hop). Closures are
// skipped (no named target); registrations inside nested closures are not
// attributed — their owner is an anonymous function, so the hop would not
// bind to a symbol (documented v1 scope).
func synthesizeDispatchEdges(owner string, body *ast.BlockStmt, calls map[string][]CallEdge) {
	if body == nil {
		return
	}
	existing := map[string]bool{}
	for _, e := range calls[owner] {
		existing[e.Target] = true
	}
	add := func(synth, target string) {
		if target == "" || target == owner || existing[target] {
			return
		}
		existing[target] = true
		calls[owner] = append(calls[owner], CallEdge{
			Target:     target,
			Confidence: ConfidenceMedium,
			Synth:      synth,
		})
	}
	ast.Inspect(body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		method := fn.Sel.Name
		var synth string
		switch {
		case method == "Handle" || method == "HandleFunc":
			// http.HandleFunc (stdlib mux), mux.HandleFunc (chi/gorilla on a
			// router var), or a field-chain mux (s.mux.HandleFunc). The
			// selector must be a plain ident or ident chain — package
			// selectors like foo.HandleFunc are not registrations.
			switch x := fn.X.(type) {
			case *ast.Ident:
				if x.Name != "http" && !routerVarNames[x.Name] {
					return true
				}
			case *ast.SelectorExpr:
				// s.mux — field chain, accept
			default:
				return true
			}
			synth = "router:net-http"
		case httpVerbs[method]:
			x, ok := fn.X.(*ast.Ident)
			if !ok || !routerVarNames[x.Name] {
				return true
			}
			synth = "router:http-route"
		case chiGroupMethods[method]:
			x, ok := fn.X.(*ast.Ident)
			if !ok || !routerVarNames[x.Name] {
				return true
			}
			synth = "router:chi"
		default:
			return true
		}
		// Registrations carry the handler as the last argument
		// (chi middleware chains: r.Get("/x", mw, h)).
		if len(ce.Args) < 2 {
			return true
		}
		add(synth, dispatchTarget(ce.Args[len(ce.Args)-1]))
		return true
	})
}

// dispatchTarget extracts a named handler from a registration argument:
// an identifier (h), a selector (svc.Handle), or a conversion wrapper
// (http.HandlerFunc(h), gin.HandlerFunc(h)). Closures and other anonymous
// expressions yield "".
func dispatchTarget(arg ast.Expr) string {
	switch t := arg.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if _, ok := t.X.(*ast.Ident); ok {
			return t.Sel.Name
		}
	case *ast.CallExpr:
		if fn, ok := t.Fun.(*ast.SelectorExpr); ok && fn.Sel.Name == "HandlerFunc" && len(t.Args) == 1 {
			if id, ok := t.Args[0].(*ast.Ident); ok {
				return id.Name
			}
		}
	}
	return ""
}
