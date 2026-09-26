// Policy-signal extraction (Self-Improvement use-cases Tier 3 #7): learns
// from the governance approval log which proposed actions ALWAYS get approved
// vs which are risky, and proposes that learning as typed-claim memories
// (RECOMMENDATION for always-approved actions, INFERENCE for risky ones).
// The guardrail is "learning proposes, policy change approves": the output is
// memory only — nothing here touches the firewall, gates, or policy store.

package learning

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// policySigParts returns the normalized action-signature components of an
// approval: requester, risk level, and sorted policy IDs. Empty components
// normalize to "unknown" / "UNKNOWN" / "none" so the signature key and the
// rendered statements stay readable.
func policySigParts(a domain.Approval) (requester, risk, policies string) {
	requester = strings.TrimSpace(a.Requester)
	if requester == "" {
		requester = "unknown"
	}
	risk = strings.TrimSpace(string(a.RiskLevel))
	if risk == "" {
		risk = "UNKNOWN"
	}
	ps := append([]string(nil), a.PolicyIDs...)
	sort.Strings(ps)
	policies = strings.Join(ps, ",")
	if policies == "" {
		policies = "none"
	}
	return requester, risk, policies
}

// artifactTag reports whether the approval carries an artifact binding
// ("artifact" / "no-artifact"). It is part of the action signature so
// artifact-backed and bare approvals of otherwise identical shape do not
// merge into one signal.
func artifactTag(a domain.Approval) string {
	if strings.TrimSpace(a.ArtifactID) != "" {
		return "artifact"
	}
	return "no-artifact"
}

// approvalSigKey assembles the deterministic signature key for an approval:
// requester + risk level + sorted policy IDs + artifact presence. The key is
// the Pattern scope, so learning.Remember upserts one constraint per action
// signature (repeated decisions refresh it instead of duplicating).
func approvalSigKey(a domain.Approval) string {
	r, risk, policies := policySigParts(a)
	return "approval:" + r + ":" + risk + ":policies=" + policies + ":artifact=" + artifactTag(a)
}

// approvalResolvedAt returns the decision time of an approval, falling back
// to the request time when no decision was recorded.
func approvalResolvedAt(a domain.Approval) time.Time {
	if a.DecidedAt != nil {
		return *a.DecidedAt
	}
	return a.RequestedAt
}

// policyGroup accumulates the decided approvals sharing one action signature.
type policyGroup struct {
	requester string
	risk      string
	policies  string
	artifact  string
	approved  int
	rejected  int
	latest    time.Time
	ids       []string
}

// PolicyPatterns derives policy signals from decided (approved/rejected)
// approvals. Each action signature (requester + risk level + sorted policy
// IDs + artifact presence) is scored independently:
//
//   - always-approved: approved >= threshold AND zero rejections →
//     RECOMMENDATION "pre-approve ..." (Count = approved count);
//   - risky: at least one rejection → INFERENCE "... rejected M times out of
//     T — policy review recommended" (Count = total decided count).
//
// Groups with neither signal (below threshold with no rejections) produce
// nothing, and pending approvals never contribute. Deterministic: PolicyIDs
// are sorted before grouping, signatures are sorted, and the returned
// patterns are ordered by signature key. threshold <= 0 is treated as 1.
func PolicyPatterns(decisions []domain.Approval, threshold int) []Pattern {
	if threshold <= 0 {
		threshold = 1
	}
	groups := map[string]*policyGroup{}
	for _, a := range decisions {
		switch a.Status {
		case "approved", "rejected":
		default:
			// Only decided approvals carry a policy signal; pending (and
			// unknown) statuses contribute nothing.
			continue
		}
		key := approvalSigKey(a)
		g, ok := groups[key]
		if !ok {
			r, risk, policies := policySigParts(a)
			g = &policyGroup{requester: r, risk: risk, policies: policies, artifact: artifactTag(a)}
			groups[key] = g
		}
		if a.Status == "approved" {
			g.approved++
		} else {
			g.rejected++
		}
		if at := approvalResolvedAt(a); at.After(g.latest) {
			g.latest = at
		}
		g.ids = append(g.ids, a.ID)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	patterns := make([]Pattern, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		var p Pattern
		switch {
		case g.approved >= threshold && g.rejected == 0:
			p = preApprovalPattern(key, g)
		case g.rejected >= 1:
			p = riskyPolicyPattern(key, g)
		default:
			continue // below threshold and never rejected: no signal yet
		}
		patterns = append(patterns, p)
	}
	return patterns
}

// preApprovalPattern assembles the RECOMMENDATION pattern for an action
// signature approved at least threshold times and never rejected: propose
// pre-approval (a future human-approved policy change may act on it).
func preApprovalPattern(key string, g *policyGroup) Pattern {
	statement := fmt.Sprintf(
		"pre-approve %s %s action (%s) — approved %d times, never rejected",
		g.requester, g.risk, g.policies, g.approved)
	return policyPattern(key, g, statement, g.approved, domain.ClaimRecommendation)
}

// riskyPolicyPattern assembles the INFERENCE pattern for an action signature
// rejected at least once: recommend policy review, never an automatic change.
func riskyPolicyPattern(key string, g *policyGroup) Pattern {
	total := g.approved + g.rejected
	statement := fmt.Sprintf(
		"%s %s action (%s) rejected %d times out of %d — policy review recommended",
		g.requester, g.risk, g.policies, g.rejected, total)
	return policyPattern(key, g, statement, total, domain.ClaimInference)
}

// policyPattern builds the common Pattern shape: the deterministic signature
// key as scope, the statement as the sample/content source, and provenance
// = the contributing approval IDs (sorted) with the newest decision time.
func policyPattern(key string, g *policyGroup, statement string, count int, ct domain.ClaimType) Pattern {
	ids := append([]string(nil), g.ids...)
	sort.Strings(ids)
	srcs := make([]string, 0, len(ids))
	for _, id := range ids {
		srcs = append(srcs, "approval "+id)
	}
	return Pattern{
		Key:       key,
		Count:     count,
		Scopes:    []string{g.requester + " " + g.risk},
		Sample:    []string{statement},
		Created:   g.latest,
		ClaimType: ct,
		Provenance: ClaimProvenance{
			Sources: srcs,
			Count:   len(ids),
			Latest:  g.latest,
		},
		Statement: statement,
	}
}
