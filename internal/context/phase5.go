package context

import (
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance/firewall"
)

// This file implements the remaining Phase 5 Context Runtime features:
//
//	P5.3 min-sufficient selector        SelectMinimal
//	P5.4 per-item authorization         AuthorizeItems
//	P5.5 GC completeness (last_used)    GC.lastUsePenalty
//	P5.8 dedup pipeline                 DedupItems
//	P5.9 freshness policy               ApplyFreshnessPolicy
//	P5.10 paging                          PageItems
//	P5.11 leases                          LeaseManager
//	P5.12 replay engine                  ReplayPacket
//
// All are additive and deterministic; nothing here calls an LLM.

// resourceForItem maps a context item to a governance resource name so the
// per-item authorization check (P5.4) can ask the firewall whether the current
// holder may see it. Items of the most sensitive classes are gated on the
// "context" resource; the rest are gated on "source".
func resourceForItem(item domain.ContextItem) string {
	switch item.Class {
	case domain.ContextSourceCode, domain.ContextFact, domain.ContextEvidence:
		return "source"
	default:
		return "context"
	}
}

// AuthorizeItems runs per-item governance checks (P5.4) for a single agent
// holder. It is a thin, backward-compatible wrapper around AuthorizeItemsScoped
// that checks only the agent → resource dimension. A nil firewall authorizes
// everything (backward-compatible).
func AuthorizeItems(items []domain.ContextItem, fw *firewall.Firewall, holder string) []domain.ContextItem {
	return AuthorizeItemsScoped(items, fw, domain.ContextAuthorization{Agent: holder})
}

// containsString reports whether s is present in list.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// AuthorizeItemsScoped runs per-item governance checks (P5.4) across the five
// scoped authorization dimensions: agent (via the firewall), repository, task,
// tenant/team, and security classification. It fails closed — an unauthorized
// dimension excludes the item from the returned set and records a DenyReason.
//
//   - Firewall dimension: if fw is non-nil, the item is denied unless the agent
//     may read resourceForItem(item).
//   - Repository dimension: when auth.Repository is set and the item is scoped
//     to a different repository, the item is denied.
//   - Task dimension: when auth.TaskID is set and the item is scoped to a
//     different task, the item is denied.
//   - Tenant dimension: when auth.Tenant is set and the item is scoped to a
//     different tenant, the item is denied; a non-empty AllowedTenants list also
//     denies an item whose tenant is not in it.
//   - Security-class dimension: a non-empty AllowedSecurityClasses list denies
//     an item whose SecurityClass is not in it.
func AuthorizeItemsScoped(items []domain.ContextItem, fw *firewall.Firewall, auth domain.ContextAuthorization) []domain.ContextItem {
	out := make([]domain.ContextItem, 0, len(items))
	for i := range items {
		item := &items[i]
		// Agent dimension via the governance firewall.
		if fw != nil {
			res := resourceForItem(*item)
			allowed, _, _, err := fw.Check(auth.Agent, res, "read")
			if err != nil || !allowed {
				item.Authorized = false
				item.DenyReason = "denied by governance firewall"
				continue
			}
		}
		// Repository dimension.
		if auth.Repository != "" && item.Repository != "" && item.Repository != auth.Repository {
			item.Authorized = false
			item.DenyReason = "repository scope denied: " + item.Repository
			continue
		}
		// Task dimension.
		if auth.TaskID != "" && item.TaskID != "" && item.TaskID != auth.TaskID {
			item.Authorized = false
			item.DenyReason = "task scope denied"
			continue
		}
		// Tenant dimension (only when the item declares a tenant).
		if item.Tenant != "" {
			if auth.Tenant != "" && item.Tenant != auth.Tenant {
				item.Authorized = false
				item.DenyReason = "tenant denied"
				continue
			}
			if len(auth.AllowedTenants) > 0 && !containsString(auth.AllowedTenants, item.Tenant) {
				item.Authorized = false
				item.DenyReason = "tenant denied"
				continue
			}
		}
		// Security-classification dimension.
		if item.SecurityClass != "" && len(auth.AllowedSecurityClasses) > 0 && !containsString(auth.AllowedSecurityClasses, item.SecurityClass) {
			item.Authorized = false
			item.DenyReason = "security class denied"
			continue
		}
		item.Authorized = true
		out = append(out, *item)
	}
	return out
}

// DedupItems removes duplicate context items (P5.8 dedup pipeline) by content
// digest. The first occurrence of each digest is kept; later duplicates are
// dropped. Items without a digest are always kept (they cannot be proven
// duplicates). The dedup pipeline is independent of GC — it runs before GC so
// the GC scores only unique items.
func DedupItems(items []domain.ContextItem) []domain.ContextItem {
	seen := make(map[string]bool, len(items))
	out := make([]domain.ContextItem, 0, len(items))
	for _, item := range items {
		if item.Digest != "" && seen[item.Digest] {
			continue
		}
		if item.Digest != "" {
			seen[item.Digest] = true
		}
		out = append(out, item)
	}
	return out
}

// CanonicalizeItems implements the Phase 5.8 canonical-fact dedup model: for
// items that share the same content digest, the FIRST occurrence is kept as the
// canonical item and each later duplicate's ID is appended to the canonical
// item's EvidenceRefs (the duplicates become evidence references for the single
// canonical fact). Items without a digest are always kept unchanged.
//
// Output ordering: canonical items (one per digest, in first-seen order)
// followed by the distinct digest-less items in their original relative order.
func CanonicalizeItems(items []domain.ContextItem) []domain.ContextItem {
	canonical := make([]domain.ContextItem, 0, len(items))
	var distinct []domain.ContextItem
	firstIdx := make(map[string]int, len(items)) // digest -> index in canonical
	for _, item := range items {
		if item.Digest == "" {
			// Not deduplicable; preserved unchanged.
			distinct = append(distinct, item)
			continue
		}
		if idx, ok := firstIdx[item.Digest]; ok {
			// Duplicate of an existing canonical fact: record as evidence ref.
			canonical[idx].EvidenceRefs = append(canonical[idx].EvidenceRefs, item.ID)
			continue
		}
		firstIdx[item.Digest] = len(canonical)
		canonical = append(canonical, item)
	}
	return append(canonical, distinct...)
}

// PageItems returns one page of items (P5.10). Pages are 1-indexed; page 0 is
// normalized to page 1. The returned ContextPage carries paging metadata and a
// copy of the requested slice (empty when out of range).
func PageItems(items []domain.ContextItem, page, pageSize int) domain.ContextPage {
	if pageSize <= 0 {
		pageSize = 10
	}
	if page <= 0 {
		page = 1
	}
	total := len(items)
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pageItems := make([]domain.ContextItem, 0, end-start)
	pageItems = append(pageItems, items[start:end]...)
	return domain.ContextPage{
		Items:      pageItems,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		HasNext:    page < totalPages,
	}
}

// LeaseManager implements P5.11 context leases. A lease reserves an item in
// the active set for a bounded duration; expired leases can be swept so the GC
// can evict the item.
type LeaseManager struct {
	leases map[string]domain.ContextLease
	now    func() time.Time
}

// NewLeaseManager returns an empty lease manager. now optionally overrides the
// clock for tests.
func NewLeaseManager(now func() time.Time) *LeaseManager {
	if now == nil {
		now = time.Now
	}
	return &LeaseManager{leases: make(map[string]domain.ContextLease), now: now}
}

// Acquire leases an item for the holder for the given duration. Replaces any
// existing lease on that item.
func (l *LeaseManager) Acquire(itemID, holder string, dur time.Duration) domain.ContextLease {
	lease := domain.ContextLease{ItemID: itemID, Holder: holder, ExpiresAt: l.now().Add(dur)}
	l.leases[itemID] = lease
	return lease
}

// Active reports whether the item has a lease that has not expired.
func (l *LeaseManager) Active(itemID string) bool {
	lease, ok := l.leases[itemID]
	if !ok {
		return false
	}
	return lease.ExpiresAt.After(l.now())
}

// Renew extends an existing lease on itemID (if any) by dur from now. Reports
// whether a lease existed to renew.
func (l *LeaseManager) Renew(itemID string, dur time.Duration) bool {
	lease, ok := l.leases[itemID]
	if !ok {
		return false
	}
	lease.ExpiresAt = l.now().Add(dur)
	l.leases[itemID] = lease
	return true
}

// Release drops the lease on an item. Idempotent.
func (l *LeaseManager) Release(itemID string) {
	delete(l.leases, itemID)
}

// Expired returns the ids of all items whose leases have expired. Used by the
// GC to know which items may be evicted.
func (l *LeaseManager) Expired() []string {
	now := l.now()
	var out []string
	for id, lease := range l.leases {
		if !lease.ExpiresAt.After(now) {
			out = append(out, id)
		}
	}
	return out
}

// Len returns the number of tracked leases.
func (l *LeaseManager) Len() int { return len(l.leases) }

// Replay reconstructs a minimal task context (P5.12) from a persisted replay
// record. It turns the snapshot fields back into facts, files, constraints,
// risks and required validation so a resumed task has a usable starting packet
// without re-running the full retrieval pipeline.
func Replay(r domain.ContextReplay) domain.ContextPacket {
	now := r.Occurred
	if now.IsZero() {
		now = time.Now()
	}
	pkt := domain.ContextPacket{
		Task:        r.Input,
		GeneratedAt: now,
	}
	for _, d := range r.Snapshot.Decisions {
		pkt.Facts = append(pkt.Facts, domain.Claim{
			Type:       domain.ClaimFact,
			Statement:  d,
			Confidence: 1.0,
		})
	}
	for _, c := range r.Snapshot.Constraints {
		pkt.ArchitectureRules = append(pkt.ArchitectureRules, domain.Policy{ID: c})
	}
	for _, f := range r.Snapshot.Files {
		pkt.Files = append(pkt.Files, domain.File{Path: f})
	}
	for _, t := range r.Snapshot.Tests {
		pkt.RequiredValidation = append(pkt.RequiredValidation, t)
	}
	for _, rk := range r.Snapshot.Risks {
		pkt.Risks = append(pkt.Risks, domain.Risk{Mitigation: rk})
	}
	return pkt
}

// minSufficient picks the smallest subset of items whose combined relevance is
// "sufficient" for the task (P5.3). Items are sorted by relevance descending;
// the selector takes items until the accumulated relevance reaches threshold or
// maxItems is reached, whichever comes first. maxItems 0 = unlimited.
func SelectMinimal(items []domain.ContextItem, threshold float64, maxItems int) []domain.ContextItem {
	if threshold <= 0 {
		return items
	}
	sorted := make([]domain.ContextItem, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].Relevance > sorted[b].Relevance })

	acc := 0.0
	out := make([]domain.ContextItem, 0, len(sorted))
	for _, item := range sorted {
		if maxItems > 0 && len(out) >= maxItems {
			break
		}
		out = append(out, item)
		acc += item.Relevance
		if acc >= threshold {
			break
		}
	}
	return out
}

// lastUsePenalty adds a GC completeness signal (P5.5) for items whose LastUsed
// is set but old, and for items never used, so the GC can evict long-unused
// items before freshly used ones. It returns a multiplicative penalty in
// (0, 1]: freshly used items keep 1.0, older items are scaled down.
func lastUsePenalty(item domain.ContextItem, now time.Time) float64 {
	if item.LastUsed.IsZero() {
		return 1.0 // unknown use — do not penalize (avoid surprise evictions)
	}
	age := now.Sub(item.LastUsed)
	switch {
	case age < 1*time.Hour:
		return 1.0
	case age < 24*time.Hour:
		return 0.7
	case age < 7*24*time.Hour:
		return 0.4
	default:
		return 0.2
	}
}

// ApplyFreshness filters and classifies items per the freshness policy (P5.9).
// Items older than the policy MaxAge are dropped from the active set. It
// returns the retained items plus a map of item ID -> freshness classification.
func ApplyFreshnessPolicy(items []domain.ContextItem, policy domain.FreshnessPolicy, now time.Time) ([]domain.ContextItem, map[string]domain.Freshness) {
	kept := make([]domain.ContextItem, 0, len(items))
	classes := make(map[string]domain.Freshness, len(items))
	for _, item := range items {
		if policy.MaxAge > 0 && !item.Freshness.IsZero() {
			fr := policy.ClassifyAge(item.Freshness, now)
			classes[item.ID] = fr
			if fr == domain.FreshnessStale {
				continue // drop stale items from the active set
			}
		} else {
			classes[item.ID] = domain.FreshnessFresh
		}
		kept = append(kept, item)
	}
	return kept, classes
}
