package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// ErrUnauthorized is returned when the agent is not authorized to read
// context. The accompanying Response always carries the auditable proof.
var ErrUnauthorized = errors.New("authz: agent not authorized to read context")

// Policy-source labels recorded on every AuthorizedScope.
const (
	policySourceTaskScope     = "task-scope"
	policySourcePermissive    = "permissive-default"
	policySourceDefaultScoped = "default-scoped"
)

// Denial stages, matching the explain-deny contract on domain.DenyReason.
const (
	stageAuthentication = "authentication"
	stageFirewall       = "firewall"
	stagePath           = "path"
)

// AuthorizeContext computes the context an agent may legally read: the
// symbols and call edges permitted by the agent's identity (via the firewall)
// and the effective task scope. It always returns a Response whose Proof is
// auditable; on denial it returns ErrUnauthorized alongside that proof.
//
// The flow: validate → resolve agent → effective scope → firewall gate
// ("context.read") → path-scoped symbol enumeration → edge filtering → proof.
func AuthorizeContext(req Request, ix *index.Index, fw *Firewall) (Response, error) {
	resp := Response{}
	if req.AgentID == "" {
		return resp, fmt.Errorf("authz: agent_id is required")
	}
	if ix == nil {
		return resp, fmt.Errorf("authz: index is nil")
	}

	// Resolve the agent identity: direct identity wins, otherwise the registry.
	agent := req.AgentIdentity
	if agent == nil {
		var err error
		agent, err = GetAgent(req.AgentID)
		if err != nil {
			// Unknown agent: fail closed with an auditable authentication denial.
			return denyUnknownAgent(req, ix), ErrUnauthorized
		}
	}

	// Effective scope: the request's scope when present, otherwise a scoped
	// default (cwd-confined when Root is set; fail-closed otherwise — see M1).
	scope := effectiveScope(req)
	source := policySourcePermissive
	if req.Scope != nil {
		source = policySourceTaskScope
	} else if req.Root != "" {
		source = policySourceDefaultScoped
	}

	// Fail closed: a request with neither an explicit scope nor a root to
	// confine the default scope to has no scope at all — deny everything.
	if scope == nil {
		risk := domain.Risk{
			Level:      domain.RiskCritical,
			Score:      1.0,
			Factors:    []string{"no task scope and no root"},
			Mitigation: "provide a task scope or a root before authorizing context reads",
			Blocked:    true,
		}
		decision := buildDecision(false, risk, nil, stagePath,
			"no task scope and no root: cannot authorize without a scope",
			req.AgentID, req.Task, "scope.required")
		proof := buildProof(req, ix, agent, decision, source, nil)
		return Response{Proof: proof}, ErrUnauthorized
	}

	// Firewall gate: context.read. The firewall owns authentication and
	// permission enforcement for known agents.
	if fw == nil {
		risk := domain.Risk{
			Level:      domain.RiskCritical,
			Score:      1.0,
			Factors:    []string{"no firewall configured"},
			Mitigation: "construct a firewall before authorizing context reads",
			Blocked:    true,
		}
		decision := buildDecision(false, risk, nil, stageFirewall,
			"no firewall configured", req.AgentID, req.Task, "firewall.availability")
		proof := buildProof(req, ix, agent, decision, source, nil)
		return Response{Proof: proof}, ErrUnauthorized
	}
	allowed, risk, approval, err := fw.Check(req.AgentID, "context", "read")
	if err != nil || !allowed {
		stage := stageFirewall
		policy := "firewall.permission"
		reason := "context.read denied by firewall"
		if err != nil {
			reason = err.Error()
		}
		decision := buildDecision(false, risk, approval, stage, reason, req.AgentID, req.Task, policy)
		proof := buildProof(req, ix, agent, decision, source, nil)
		return Response{Proof: proof}, ErrUnauthorized
	}

	// Enumerate + filter symbols: path-scoped partition, then substring filter
	// applied to the allowed set only.
	allowedRefs, denied := filterSymbols(ix, scope, req.SymbolFilter)
	allowedSet := make(map[string]bool, len(allowedRefs))
	for _, r := range allowedRefs {
		allowedSet[r.Qualified] = true
	}

	// Filter edges: every call edge whose caller is in the allowed set. Edges
	// to unresolved callees are kept — they are external, not a leak.
	edges := filterEdges(ix, allowedSet)

	decision := buildDecision(true, risk, approval, "", "", req.AgentID, req.Task, "")
	proof := buildProof(req, ix, agent, decision, source, allowedRefs)

	return Response{
		Scope: AuthorizedScope{
			Symbols:      allowedRefs,
			Edges:        edges,
			Denied:       denied,
			PolicySource: source,
		},
		Proof: proof,
	}, nil
}

// effectiveScope returns the request's scope when set, otherwise the default
// scope: a cwd-scoped TaskScope confined to the request's root (the project
// the index was built from). The old permissive default (all paths allowed)
// is gone — authz is the spine of the default retrieval path, so a request
// without an explicit scope is governed and cwd-scoped, not raw. When neither
// an explicit scope nor a root is set, it returns nil so the caller can fail
// closed (M1): an empty Paths scope would silently allow every path.
func effectiveScope(req Request) *domain.TaskScope {
	if req.Scope != nil {
		return req.Scope
	}
	if req.Root == "" {
		return nil
	}
	sc := &domain.TaskScope{TaskID: req.Task}
	sc.Paths = []string{req.Root}
	return sc
}

// scopeAllows reports whether the unified scope allows a symbol's file. The
// relative path — the documented convention for scope path entries ("secret/",
// "internal/legacy") — is checked first. When the scope's allowed paths are
// absolute (the default cwd-scoped scope carries the index root, while symbol
// files are stored root-relative), the absolute form of the file is checked as
// a fallback. An explicit denied-path entry is honored on the relative form
// and the absolute fallback can never override it.
func scopeAllows(ix *index.Index, scope *domain.TaskScope, file string) bool {
	if scope.CheckPath(file) {
		return true
	}
	// Rejected on the relative form. If an explicit denied-path entry caused
	// the rejection, keep the denial; the absolute fallback exists only for
	// allowlist misses against absolute root paths.
	if !(domain.TaskBoundary{DeniedPaths: scope.DeniedPaths}).CheckPath(file) {
		return false
	}
	return scope.CheckPath(filepath.Join(ix.Root, file))
}

// filterSymbols partitions the index symbols into the allowed set (passing
// scope.CheckPath and, when set, the substring filter) and the denied set.
// The file→package map is built once up front (single pass, one allocation)
// instead of a per-symbol O(n²) pkgForFile lookup.
func filterSymbols(ix *index.Index, scope *domain.TaskScope, filter string) ([]SymbolRef, []DeniedSymbol) {
	pkgByFile := make(map[string]string, len(ix.Pkgs))
	for _, pkg := range ix.Pkgs {
		for _, f := range pkg.Files {
			pkgByFile[f] = pkg.Path
		}
	}

	var allowed []SymbolRef
	var denied []DeniedSymbol
	for _, s := range ix.Symbols {
		if !scopeAllows(ix, scope, s.File) {
			denied = append(denied, DeniedSymbol{
				Symbol: symbolRef(s, pkgByFile),
				Stage:  stagePath,
				Reason: "path denied by task scope: " + s.File,
			})
			continue
		}
		ref := symbolRef(s, pkgByFile)
		if filter != "" && !strings.Contains(ref.Qualified, filter) && !strings.Contains(ref.Name, filter) {
			continue
		}
		allowed = append(allowed, ref)
	}
	return allowed, denied
}

// symbolRef converts an index symbol to its scope reference, using FullName()
// for the qualified name so methods resolve to "Receiver.Name".
func symbolRef(s index.Symbol, pkgByFile map[string]string) SymbolRef {
	return SymbolRef{
		Name:      s.Name,
		Qualified: s.FullName(),
		Kind:      s.Kind,
		File:      s.File,
		Line:      s.Line,
		Pkg:       pkgByFile[s.File],
	}
}

// filterEdges keeps every call edge whose caller is in the allowed set and
// returns them sorted stably (From, then To) so the edge list — and the
// fingerprint — is deterministic regardless of map iteration order.
func filterEdges(ix *index.Index, allowed map[string]bool) []EdgeRef {
	var edges []EdgeRef
	for from, callees := range ix.Calls {
		if !allowed[from] {
			continue
		}
		for _, to := range callees {
			edges = append(edges, EdgeRef{From: from, To: to})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return edges
}

// buildDecision assembles the gateway result for the authorization, attaching
// an explain-deny object when the decision is not allowed.
func buildDecision(allowed bool, risk domain.Risk, approval *domain.Approval, stage, reason, agentID, taskID, policy string) domain.GatewayResult {
	decision := domain.DecisionAllowed
	if !allowed {
		decision = domain.DecisionDenied
	}
	res := domain.GatewayResult{
		Decision: decision,
		Allowed:  allowed,
		Risk:     risk,
		Approval: approval,
	}
	if !allowed {
		res.Deny = &domain.DenyReason{
			Stage:            stage,
			AgentID:          agentID,
			TaskID:           taskID,
			Resource:         "context",
			Action:           "read",
			Reason:           reason,
			Risk:             risk,
			RequiredApproval: approval,
			Policy:           policy,
		}
	}
	return res
}

// buildProof assembles the auditable proof: decision, agent summary, the
// effective task scope, and a deterministic fingerprint over the stable
// inputs (index identity + decision + allowed symbol set).
func buildProof(req Request, ix *index.Index, agent *AgentIdentity, decision domain.GatewayResult, policySource string, symbols []SymbolRef) AuthorizationProof {
	scope := domain.TaskScope{TaskID: req.Task}
	if sc := effectiveScope(req); sc != nil {
		scope = *sc
	}
	if scope.TaskID == "" {
		scope.TaskID = req.Task
	}
	summary := AgentSummary{ID: req.AgentID}
	if agent != nil {
		summary.Permissions = len(agent.Permissions)
	}
	return AuthorizationProof{
		Decision:       decision,
		Agent:          summary,
		TaskScope:      scope,
		Fingerprint:    fingerprint(ix, decision, policySource, symbols),
		IndexFreshness: freshness(ix),
		IndexVersion:   ix.UpdatedAt.UTC().Format(time.RFC3339),
		DecidedAt:      time.Now().UTC(),
	}
}

// fingerprint is a SHA-256 over the stable inputs of an authorization: the
// index identity, the policy source, the decision, and the sorted set of
// allowed qualified symbol names. Identical inputs always produce identical
// fingerprints; mutating the scope symbols changes it.
func fingerprint(ix *index.Index, decision domain.GatewayResult, policySource string, symbols []SymbolRef) string {
	h := sha256.New()
	fmt.Fprintf(h, "root=%s\n", ix.Root)
	fmt.Fprintf(h, "index_updated=%s\n", ix.UpdatedAt.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(h, "policy_source=%s\n", policySource)
	fmt.Fprintf(h, "allowed=%t\n", decision.Allowed)
	if decision.Deny != nil {
		fmt.Fprintf(h, "deny_stage=%s\n", decision.Deny.Stage)
	}
	names := make([]string, 0, len(symbols))
	for _, s := range symbols {
		names = append(names, s.Qualified)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(h, "symbol=%s\n", n)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// freshness reports whether the index is recent enough to be trusted as-is.
func freshness(ix *index.Index) string {
	if time.Since(ix.UpdatedAt) < 5*time.Minute {
		return "fresh"
	}
	return "stale"
}

// denyUnknownAgent builds the fail-closed response for an agent that does not
// exist in the identity registry: an authentication-stage denial with an
// auditable proof and no scope symbols.
func denyUnknownAgent(req Request, ix *index.Index) Response {
	source := policySourcePermissive
	if req.Scope != nil {
		source = policySourceTaskScope
	} else if req.Root != "" {
		source = policySourceDefaultScoped
	}
	risk := domain.Risk{
		Level:      domain.RiskCritical,
		Score:      1.0,
		Factors:    []string{"unknown agent"},
		Mitigation: "register the agent before use",
		Blocked:    true,
	}
	decision := buildDecision(false, risk, nil, stageAuthentication,
		"unknown agent: "+req.AgentID, req.AgentID, req.Task, "governance.authentication")
	proof := buildProof(req, ix, nil, decision, source, nil)
	return Response{Proof: proof}
}
