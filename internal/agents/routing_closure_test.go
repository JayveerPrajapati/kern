package agents

import (
	"reflect"
	"testing"
)

// TestRoutingContextKindDelegates guards that RoutingContext.Kind delegates to
// ClassifyTask so the routing context remains backward compatible with the
// existing classification entry point.
func TestRoutingContextKindDelegates(t *testing.T) {
	incident := RoutingContext{Intent: "investigate incident"}
	if got, want := incident.Kind(), TaskKindIncident; got != want {
		t.Errorf("RoutingContext{Intent:%q}.Kind() = %v, want %v", incident.Intent, got, want)
	}

	modernize := RoutingContext{TaskType: "modernize"}
	if got, want := modernize.Kind(), TaskKindModernization; got != want {
		t.Errorf("RoutingContext{TaskType:%q}.Kind() = %v, want %v", modernize.TaskType, got, want)
	}
}

// TestRoutingContextRankHistoricalSuccess verifies that historical success is
// ranked score-descending and that a policy boost can lift a lower-success role
// above a higher-success one.
func TestRoutingContextRankHistoricalSuccess(t *testing.T) {
	ctx := RoutingContext{
		Policy: "high-risk",
		HistoricalSuccess: map[string]float64{
			"coder":    0.9,
			"security": 0.5,
		},
	}
	got := ctx.RankRoles([]string{"coder", "reviewer", "security"})

	// "security" is implied by the high-risk policy, so it must outrank the
	// higher-history "coder".
	if got[0] != "security" {
		t.Fatalf("RankRoles[0] = %q, want %q (policy boost should lift security above coder); got %v", got[0], "security", got)
	}

	// Deterministic: same input twice must yield the same order.
	if again := ctx.RankRoles([]string{"coder", "reviewer", "security"}); !reflect.DeepEqual(got, again) {
		t.Errorf("RankRoles not deterministic\n first: %v\nsecond: %v", got, again)
	}
}

// TestRoutingContextRankRepositoryBoost verifies that a role whose name appears
// in the repository is boosted above otherwise-tied candidates.
func TestRoutingContextRankRepositoryBoost(t *testing.T) {
	ctx := RoutingContext{Repository: "sre-runbook"}
	got := ctx.RankRoles([]string{"coder", "sre"})
	if got[0] != "sre" {
		t.Fatalf("RankRoles[0] = %q, want sre (repository %q boosts sre); got %v", got[0], ctx.Repository, got)
	}

	// Control: without a matching repository, coder sorts alphabetically first
	// (both score 0).
	plain := RoutingContext{}
	if got := plain.RankRoles([]string{"coder", "sre"}); got[0] != "coder" {
		t.Fatalf("RankRoles[0] without repo = %q, want coder (alphabetical tie-break); got %v", got[0], got)
	}
}

// TestRoutingContextRouteForReturnsRoles verifies RouteFor returns a non-empty,
// duplicate-free role list that is the pipeline role set for the classified
// kind — both for a code change and an incident.
func TestRoutingContextRouteForReturnsRoles(t *testing.T) {
	// Code-change context: set must equal the code pipeline role set.
	code := &RoutingContext{Intent: "add a retry to the http client"}
	got := code.RouteFor()
	if len(got) == 0 {
		t.Fatal("RouteFor on a code context returned an empty role list")
	}
	wantRoles := []Role{RolePlanner, RoleArchitect, RoleCoder, RoleReviewer, RoleSecurity, RoleTester}
	if !sameSet(got, wantRoles) {
		t.Errorf("RouteFor(code) roles = %v, want the code pipeline set %v", got, wantRoles)
	}
	if !allUnique(got) {
		t.Errorf("RouteFor(code) contains duplicates: %v", got)
	}

	// Incident context: must include all incident pipeline roles.
	incident := &RoutingContext{Intent: "correlate alerts for the payment outage"}
	gotInc := incident.RouteFor()
	incidentRoles := []string{string(RolePlanner), string(RoleCoder), string(RoleSecurity), string(RoleTester), string(RoleSRE)}
	for _, role := range incidentRoles {
		if !containsRole(gotInc, role) {
			t.Errorf("RouteFor(incident) is missing pipeline role %q; got %v", role, gotInc)
		}
	}
	if len(gotInc) == 0 {
		t.Error("RouteFor(incident) returned an empty role list")
	}
}

// TestRoutingContextDeterministic guards against map-iteration nondeterminism:
// the same routing context must always yield identical RouteFor output.
func TestRoutingContextDeterministic(t *testing.T) {
	ctx := &RoutingContext{
		Intent:     "modernize the checkout flow",
		Repository: "payments-service",
		Policy:     "production",
		HistoricalSuccess: map[string]float64{
			"architect": 0.8,
			"planner":   0.6,
		},
	}
	first := ctx.RouteFor()
	for i := 0; i < 20; i++ {
		if again := ctx.RouteFor(); !reflect.DeepEqual(first, again) {
			t.Fatalf("RouteFor nondeterministic (run %d):\n first: %v\n again: %v", i, first, again)
		}
	}
}

// sameSet reports whether got contains exactly the roles in want, ignoring order.
func sameSet(got []string, want []Role) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[string(w)] {
			return false
		}
	}
	return true
}

// allUnique reports whether all elements of roles are distinct.
func allUnique(roles []string) bool {
	seen := map[string]bool{}
	for _, r := range roles {
		if seen[r] {
			return false
		}
		seen[r] = true
	}
	return true
}

// containsRole reports whether roles contains role.
func containsRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}
