package edges

import (
	"sort"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Edge kinds .
const (
	KindChangedBy    = "changed_by"    // file ← commit (git history)
	KindAffects      = "affects"       // symbol → symbol (impact/blast radius)
	KindCaused       = "caused"        // symbol/commit → incident (root cause)
	KindFixedBy      = "fixed_by"      // incident → commit/PR (fix)
	KindViolates     = "violates"      // file/symbol → rule (architecture)
	KindDocumentedBy = "documented_by" // symbol ← doc file (documentation)
	KindRelatedTo    = "related_to"    // symbol ↔ symbol (semantic relatedness)
	KindOwns         = "owns"          // team → file/symbol (ownership)
)

// ChangedByEdges builds edges from git commit history. Each file changed
// by a commit gets a changed_by edge pointing to the commit node.
// commitFiles maps commit SHA → list of changed file paths.
func ChangedByEdges(commitFiles map[string][]string) []domain.Edge {
	var edges []domain.Edge
	for commit, files := range commitFiles {
		commitID := "commit:" + commit
		for _, file := range files {
			edges = append(edges, domain.Edge{
				From: "file:" + file,
				To:   commitID,
				Kind: KindChangedBy,
			})
		}
	}
	return edges
}

// AffectsEdges builds edges from impact analysis. Each affected symbol
// gets an affects edge from the root symbol.
// rootSymbol is the symbol being changed; affected are the symbols
// transitively impacted.
func AffectsEdges(rootSymbol string, affected []string) []domain.Edge {
	var edges []domain.Edge
	for _, sym := range affected {
		edges = append(edges, domain.Edge{
			From: rootSymbol,
			To:   sym,
			Kind: KindAffects,
		})
	}
	return edges
}

// CausedEdges builds edges from incident root-cause analysis. Each
// hypothesis links a cause (symbol or commit) to an incident.
// incidents maps incident ID → cause node ID (symbol or commit).
func CausedEdges(incidents map[string]string) []domain.Edge {
	var edges []domain.Edge
	for incID, causeID := range incidents {
		edges = append(edges, domain.Edge{
			From: causeID,
			To:   "incident:" + incID,
			Kind: KindCaused,
		})
	}
	return edges
}

// FixedByEdges builds edges linking incidents to their fix commits/PRs.
// fixes maps incident ID → fix commit SHA or PR ID.
func FixedByEdges(fixes map[string]string) []domain.Edge {
	// Sort incident IDs for deterministic output (map iteration is unordered).
	ids := make([]string, 0, len(fixes))
	for incID := range fixes {
		ids = append(ids, incID)
	}
	sort.Strings(ids)

	var edges []domain.Edge
	for _, incID := range ids {
		fixID := fixes[incID]
		target := fixID
		if len(fixID) == 40 {
			target = "commit:" + fixID // SHA → commit node
		} else {
			target = "pr:" + fixID // PR number → PR node
		}
		edges = append(edges, domain.Edge{
			From: "incident:" + incID,
			To:   target,
			Kind: KindFixedBy,
		})
	}
	return edges
}

// ViolatesEdges builds edges from architecture rule violations. Each
// violating file/symbol gets a violates edge pointing to the rule.
// violations maps file/symbol ID → rule ID.
func ViolatesEdges(violations map[string]string) []domain.Edge {
	var edges []domain.Edge
	for source, rule := range violations {
		edges = append(edges, domain.Edge{
			From: source,
			To:   "rule:" + rule,
			Kind: KindViolates,
		})
	}
	return edges
}

// DocumentedByEdges builds edges linking symbols to documentation files
// that mention them. docLinks maps symbol ID → doc file path.
func DocumentedByEdges(docLinks map[string]string) []domain.Edge {
	var edges []domain.Edge
	for sym, docFile := range docLinks {
		edges = append(edges, domain.Edge{
			From: "file:" + docFile,
			To:   sym,
			Kind: KindDocumentedBy,
		})
	}
	return edges
}

// OwnsEdges builds edges from team ownership. ownership maps file/symbol
// ID → team ID.
func OwnsEdges(ownership map[string]string) []domain.Edge {
	var edges []domain.Edge
	for target, team := range ownership {
		edges = append(edges, domain.Edge{
			From: team, // e.g. "@backend-team"
			To:   target,
			Kind: KindOwns,
		})
	}
	return edges
}

// RelatedToEdges builds bidirectional related_to edges between symbols
// that are semantically related (e.g. symbols that frequently co-occur
// in commits or are in the same community).
// pairs is a list of [symbolA, symbolB] pairs.
func RelatedToEdges(pairs [][2]string) []domain.Edge {
	var edges []domain.Edge
	for _, p := range pairs {
		edges = append(edges, domain.Edge{From: p[0], To: p[1], Kind: KindRelatedTo})
		edges = append(edges, domain.Edge{From: p[1], To: p[0], Kind: KindRelatedTo})
	}
	return edges
}
