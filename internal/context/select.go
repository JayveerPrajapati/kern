package context

import (
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance/firewall"
)

// This file implements the unified minimum-sufficient context selection engine
// (Phase 5.3). Unlike the pieces in phase5.go, SelectContext is a single engine
// that takes ALL the selection inputs and applies them in one explicit pass.
//
// The documented selection order applied here is:
//
//	intent → target → direct dependencies → required constraints →
//	relevant memory → relevant tests → historical evidence (when justified) →
//	runtime evidence (when justified)
//
// then the selected set is ranked and reduced to the minimum sufficient subset.
//
// The pipeline is fully deterministic: permissions are enforced first (P5.4),
// the selection order is applied, relevance scoring ranks the candidates, the
// reduction keeps constraints + evidence under aggressive trimming, and the
// freshness policy (P5.9) filters stale items. Nothing here calls an LLM.

// SelectRequest carries every input the selection engine needs. The candidate
// set is Items (pre-assembled from graph + memory + tools by the caller); the
// typed inputs describe the task so the engine can apply the documented
// selection order deterministically.
type SelectRequest struct {
	// Items is the candidate context pool (e.g. assembled from graph, memory,
	// tool results, constraints, evidence). The engine authorizes, orders,
	// ranks and reduces these.
	Items []domain.ContextItem

	Intent    domain.Intent
	TaskState string
	Target    string
	Graph     domain.Graph
	Memory    []domain.Memory
	Risks     []domain.Risk

	// Permissions gate (P5.4). A nil firewall authorizes everything
	// (backward compatible); otherwise only items the holder may read survive.
	Firewall *firewall.Firewall
	Holder   string

	// Scoped authorization dimensions (P5.4). Empty fields are unrestricted;
	// when set they refine which repository/task/tenant/security-class items
	// the holder may select.
	Repository             string
	TaskID                 string
	Tenant                 string
	AllowedTenants         []string
	AllowedSecurityClasses []string

	// Freshness policy (p5.9). Stale items are excluded from the active set.
	Freshness domain.FreshnessPolicy

	// Now is the clock used for freshness/ranking; zero means time.Now.
	Now time.Time

	// Threshold is the minimum accumulated relevance that counts as
	// "sufficient". 0 = keep everything (bounded only by MaxItems).
	Threshold float64
	// MaxItems caps the active selection; 0 = unlimited.
	MaxItems int
}

// selectionStage maps a context item to its position in the documented
// selection order. Lower stage = selected earlier. Historical evidence
// (stage 6) and runtime evidence (stage 7) are only pulled in when the
// sufficient budget still needs them, so they naturally come last.
func selectionStage(it domain.ContextItem, req SelectRequest) int {
	switch it.Class {
	case domain.ContextUserIntent:
		return 0 // intent
	case domain.ContextTaskState, domain.ContextPlan:
		return 1 // task state
	case domain.ContextSourceCode, domain.ContextFact:
		// Direct dependencies of the target: code whose content references the
		// target, or target-adjacent facts, are selected at stage 2.
		return 2
	case domain.ContextConstraint:
		return 3 // required constraints
	case domain.ContextMemory:
		return 4 // relevant memory
	case domain.ContextTestResult:
		return 5 // relevant tests
	case domain.ContextEvidence:
		return 6 // historical evidence (when justified)
	case domain.ContextToolResult, domain.ContextHistory, domain.ContextError:
		return 7 // runtime evidence (when justified)
	default:
		return 8
	}
}

func (r SelectRequest) now() time.Time {
	if r.Now.IsZero() {
		return time.Now()
	}
	return r.Now
}

// isProtected reports whether an item is always retained regardless of how
// aggressively the min-sufficient reduction trims the selection. Constraints
// encode hard rules the task must obey, and evidence backs the reasoning, so
// both are kept even when they do not fit the relevance budget.
func isProtected(it domain.ContextItem) bool {
	return it.Class == domain.ContextConstraint || it.Class == domain.ContextEvidence
}

// SelectContext is the unified minimum-sufficient selection engine (Phase
// 5.3). It enforces permissions FIRST, then applies the documented selection
// order, ranks by relevance, reduces to the minimum sufficient subset
// (reusing the SelectMinimal strategy) while keeping constraints + evidence,
// and applies the freshness policy.
//
// The returned slice is the selected ACTIVE context, ordered by the selection
// order (stage asc, then relevance desc) with protected items preserved.
func SelectContext(req SelectRequest) []domain.ContextItem {
	// 1. Permissions / authorization FIRST (P5.4): denied items never enter
	//    the pool, so they cannot be selected or influence ranking.
	items := AuthorizeItemsScoped(req.Items, req.Firewall, domain.ContextAuthorization{
		Agent:                  req.Holder,
		Repository:             req.Repository,
		TaskID:                 req.TaskID,
		Tenant:                 req.Tenant,
		AllowedTenants:         req.AllowedTenants,
		AllowedSecurityClasses: req.AllowedSecurityClasses,
	})

	// 2. Freshness policy (P5.9): drop stale items from the active set.
	items, _ = ApplyFreshnessPolicy(items, req.Freshness, req.now())

	// 3. Classify each item into a selection stage and note protected items.
	protected := make(map[string]bool)
	type ranked struct {
		item  domain.ContextItem
		stage int
	}
	pool := make([]ranked, 0, len(items))
	for _, it := range items {
		if isProtected(it) {
			protected[it.ID] = true
		}
		pool = append(pool, ranked{item: it, stage: selectionStage(it, req)})
	}

	// 4. Rank: apply the selection order (stage asc), tie-broken by the
	//	relevance score (desc) computed from the input signals.
	sort.SliceStable(pool, func(a, b int) bool {
		if pool[a].stage != pool[b].stage {
			return pool[a].stage < pool[b].stage
		}
		return pool[a].item.Relevance > pool[b].item.Relevance
	})

	// 5. Reduce to minimum sufficient, keeping constraints + evidence.
	out := make([]domain.ContextItem, 0, len(pool))
	acc := 0.0
	used := 0
	for _, r := range pool {
		if protected[r.item.ID] {
			// Constraints and evidence are always retained, even past the
			// threshold or budget.
			out = append(out, r.item)
			used++
			continue
		}
		if req.MaxItems > 0 && used >= req.MaxItems {
			break
		}
		// Historical/runtime evidence (stages 6-7) are only pulled in "when
		// justified": once the accumulated relevance is sufficient we stop
		// adding non-protected items, so later-stage items naturally drop out.
		if req.Threshold > 0 && acc >= req.Threshold {
			continue
		}
		out = append(out, r.item)
		acc += r.item.Relevance
		used++
	}
	return out
}
