package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// Policy-source labels, matching the values recorded on authz.AuthorizedScope
// (the P1.2 contract exposes them on the provenance authorizing rule).
const (
	policySourceTaskScope     = "task-scope"
	policySourcePermissive    = "permissive-default"
	policySourceDefaultScoped = "default-scoped"
)

// The built-in default agent identity, the KERN_MCP_PERMISSIVE escape hatch,
// and the default agent registration live in internal/governance
// (DefaultAgentID, PermissiveMode, EnsureDefaultAgent) — shared with the CLI
// guard so both surfaces govern identically.

// governor carries the authorized scope for one governed retrieval call. It
// is nil (raw mode) only when KERN_MCP_PERMISSIVE=1 opts out of default
// governance; otherwise every call — with or without an explicit agent_id —
// runs authorization and returns a non-nil governor.
type governor struct {
	policySource string                        // "task-scope" | "default-scoped" | "permissive-default"
	proof        governance.AuthorizationProof // always populated (auditable)
	allowed      map[string]bool               // qualified names the agent may read
}

// newGovernor runs authorization for a retrieval call. With an explicit
// agent_id it behaves as before (agent + optional task scope). Without one,
// the call is governed by the default agent and a cwd-scoped default scope —
// unless KERN_MCP_PERMISSIVE=1 explicitly restores raw mode (nil governor).
// The firewall is built per call, mirroring kern_authorize_context (the MCP
// server holds no global firewall state). On denial the governor is still
// returned alongside the error so the denial is auditable (its proof carries
// the fingerprint, decided-at and deny policy).
func (s *Server) newGovernor(ctx context.Context, args map[string]any, ix *index.Index) (*governor, error) {
	agentID := argString(args, "agent_id")
	wasDefault := false
	if agentID == "" {
		if governance.PermissiveMode() {
			return nil, nil // explicit opt-out: raw mode, byte-for-byte legacy behavior
		}
		agentID = governance.DefaultAgentID
		wasDefault = true
	}
	task := argString(args, "task")
	fw := governance.NewFirewall()
	if agent, aerr := governance.GetAgent(agentID); aerr == nil {
		fw = fw.WithAgents(agent)
	}
	sc := taskScopeFromArgs(args, task)
	source := policySourceDefaultScoped
	if !wasDefault && sc != nil {
		source = policySourceTaskScope
	}
	req := governance.Request{
		Task:    task,
		AgentID: agentID,
		Scope:   sc,
		Root:    ix.Root,
	}
	resp, aerr := governance.AuthorizeContext(req, ix, fw)
	g := &governor{
		policySource: source,
		proof:        resp.Proof,
		allowed:      map[string]bool{},
	}
	for _, r := range resp.Scope.Symbols {
		g.allowed[r.Qualified] = true
	}
	if aerr != nil {
		return g, aerr
	}
	if !resp.Proof.Decision.Allowed {
		return g, fmt.Errorf("authz: agent %q not authorized to read context", agentID)
	}
	return g, nil
}

// taskScopeFromArgs builds a TaskScope from the optional "scope" argument,
// mirroring the kern_authorize_context tool's shape: an object with optional
// paths/denied_paths/services/envs/artifacts string arrays. It returns nil
// when no scope argument is present, so callers fall back to the permissive
// default.
func taskScopeFromArgs(args map[string]any, taskID string) *domain.TaskScope {
	raw, ok := args["scope"]
	if !ok || raw == nil {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	sc := &domain.TaskScope{TaskID: taskID}
	for _, key := range []string{"paths", "denied_paths", "services", "envs", "artifacts"} {
		v, ok := m[key].([]any)
		if !ok {
			continue
		}
		switch key {
		case "paths":
			for _, p := range v {
				sc.Paths = append(sc.Paths, fmt.Sprintf("%v", p))
			}
		case "denied_paths":
			for _, p := range v {
				sc.DeniedPaths = append(sc.DeniedPaths, fmt.Sprintf("%v", p))
			}
		case "services":
			for _, p := range v {
				sc.Services = append(sc.Services, fmt.Sprintf("%v", p))
			}
		case "envs":
			for _, p := range v {
				sc.Envs = append(sc.Envs, fmt.Sprintf("%v", p))
			}
		case "artifacts":
			for _, p := range v {
				sc.Artifacts = append(sc.Artifacts, fmt.Sprintf("%v", p))
			}
		}
	}
	return sc
}

// nameAllowed reports whether a displayed name is within the authorized
// scope. An exact match on the qualified set wins; otherwise the name is
// resolved to its definition. Unresolvable names (foreign/external targets)
// are kept — they are external dependencies, not a leak — mirroring the authz
// edge filter's keep-unresolved-callees rule.
func (g *governor) nameAllowed(ix *index.Index, name string) bool {
	if g.allowed[name] {
		return true
	}
	if d, ok := ix.ResolveName(name); ok {
		return g.allowed[d.FullName()]
	}
	return true
}

// filterQualified drops names that resolve to a definition outside the
// authorized scope. keepUnresolved retains unresolvable (foreign/external)
// names: callers and blast radius are always local (keepUnresolved=false),
// callees may be external (keepUnresolved=true).
func (g *governor) filterQualified(ix *index.Index, names []string, keepUnresolved bool) []string {
	var out []string
	for _, n := range names {
		if g.allowed[n] {
			out = append(out, n)
			continue
		}
		if d, ok := ix.ResolveName(n); ok {
			if g.allowed[d.FullName()] {
				out = append(out, n)
			}
			continue
		}
		if keepUnresolved {
			out = append(out, n)
		}
	}
	return out
}

// filterList keeps only the allowed names in a comma-separated list (dispatch
// implementations, community members).
func (g *governor) filterList(ix *index.Index, csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		name := strings.TrimSpace(part)
		if name == "" || name == "…" {
			continue
		}
		if g.nameAllowed(ix, name) {
			out = append(out, name)
		}
	}
	return out
}

// filterGraphText removes every line of a rendered graph context that names a
// symbol outside the authorized scope, recomputing the section counts so the
// rendered output never hints at filtered symbols. Foreign/unresolvable names
// are kept. The output is a subset of the input, so the caller's token budget
// still holds. It runs on the rendered text (GraphCtx returns a string, not a
// report), but the filtering stays in the handler layer — the intel package
// is untouched.
func (g *governor) filterGraphText(ix *index.Index, text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	var pending []string // adjacency rows of the current callers/callees section
	flush := func() {
		if len(pending) == 0 {
			return
		}
		// The section header was emitted before its rows; rewrite its count
		// in place from the surviving rows so no filtered count leaks.
		last := out[len(out)-1]
		if i := strings.Index(last, " ("); i >= 0 {
			out[len(out)-1] = last[:i] + fmt.Sprintf(" (%d):", len(pending))
		}
		out = append(out, pending...)
		pending = nil
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "callers ("), strings.HasPrefix(line, "callees ("):
			flush()
			out = append(out, line)
		case strings.HasPrefix(line, "  ") && strings.Contains(line, " ["):
			// adjacency row "  Name [CONF] — file:line"
			name := strings.TrimSpace(line[:strings.Index(line, " [")])
			if g.nameAllowed(ix, name) {
				pending = append(pending, line)
			}
		case strings.HasPrefix(line, "    "):
			// nested dispatch under a callee row
			if i := strings.Index(line, ": "); i >= 0 {
				if names := g.filterList(ix, line[i+2:]); len(names) > 0 {
					pending = append(pending, line[:i+2]+strings.Join(names, ", "))
				}
			}
		case strings.HasPrefix(line, "  "):
			// root dispatch line "  dispatch (INFERRED): a, b"
			if i := strings.Index(line, ": "); i >= 0 {
				if names := g.filterList(ix, line[i+2:]); len(names) > 0 {
					out = append(out, line[:i+2]+strings.Join(names, ", "))
				}
			} else {
				out = append(out, line)
			}
		case strings.HasPrefix(line, "community ("):
			// community membership list; recompute the member count from the
			// surviving (allowed) members shown.
			if i := strings.Index(line, ": "); i >= 0 {
				if names := g.filterList(ix, line[i+2:]); len(names) > 0 {
					out = append(out, fmt.Sprintf("community (%d members): %s", len(names), strings.Join(names, ", ")))
				}
			} else {
				out = append(out, line)
			}
		default:
			flush()
			out = append(out, line)
		}
	}
	flush()
	return strings.Join(out, "\n")
}

// filterContextFooter filters the caller/callee name footer that ix.Context
// appends to a source slice, dropping names outside the authorized scope. The
// source lines themselves and the token-savings summary pass through
// unchanged (the root symbol's verbatim definition is authorized context).
func (g *governor) filterContextFooter(ix *index.Index, src string) string {
	if src == "" {
		return ""
	}
	lines := strings.Split(src, "\n")
	var out []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "callers: "), strings.HasPrefix(line, "calls: "):
			prefix := "callers: "
			if strings.HasPrefix(line, "calls: ") {
				prefix = "calls: "
			}
			if names := g.filterList(ix, line[len(prefix):]); len(names) > 0 {
				out = append(out, prefix+strings.Join(names, ", "))
			}
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// graphSymbolsFromText extracts the symbol names visible in a rendered graph
// context, resolving each to file:line where a definition exists. It is the
// provenance counterpart of the graph text: the symbols list equals exactly
// what the response returned.
func graphSymbolsFromText(ix *index.Index, text string) []SymbolProvenance {
	var names []string
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "graph "), strings.HasPrefix(t, "callers ("), strings.HasPrefix(t, "callees ("):
			// headers name no symbol
		case strings.Contains(t, " ["):
			// adjacency row "Name [CONF] — file:line"
			names = append(names, t[:strings.Index(t, " [")])
		case strings.Contains(t, ": "):
			// dispatch / community list
			for _, part := range strings.Split(t[strings.Index(t, ": ")+2:], ",") {
				if n := strings.TrimSpace(part); n != "" && n != "…" {
					names = append(names, n)
				}
			}
		}
	}
	return symbolProvenances(ix, names)
}

// simpleName strips the package qualifier from a name, mirroring intel's
// display convention ("pkg.Func" → "Func").
func simpleName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// simpleNames is the handler-layer counterpart of intel's cleanNames: strip
// package qualifiers, dedupe, sort. Kept here so filtering never touches the
// intel package.
func simpleNames(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, n := range in {
		s := simpleName(n)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
