package intel

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// setServerVersionIndex builds the SetServerVersion shape (L4 caller
// resolution): a target with a unique simple name called by (a) a
// same-package bare caller, and (b) qualified cross-package callers. One
// qualified caller is named "main", a bare name shared by two packages, so
// the graph cannot resolve the caller node — WhoCallsPrecise drops it via
// nodesForIDs while the raw-edge collector must keep it verbatim.
func setServerVersionIndex() *index.Index {
	return &index.Index{
		Root: "/sv",
		Symbols: []index.Symbol{
			sym("func", "SetServerVersion", "internal/mcp/server_paths.go", 1),
			sym("func", "currentServerVersion", "internal/mcp/server_paths.go", 1),
			sym("func", "main", "cmd/kern/main.go", 1),
			sym("func", "main", "cmd/kern-server/main.go", 1),
			sym("func", "bootstrap", "cmd/kern-server/main.go", 1),
		},
		Calls: map[string][]index.CallEdge{
			"currentServerVersion": {{Target: "SetServerVersion", Confidence: index.ConfidenceHigh}},
			"main":                 {{Target: "mcp.SetServerVersion", Confidence: index.ConfidenceHigh}},
			"bootstrap":            {{Target: "mcp.SetServerVersion", Confidence: index.ConfidenceHigh}},
		},
		Callers: map[string][]string{
			"SetServerVersion":     {"currentServerVersion", "main", "bootstrap"},
			"mcp.SetServerVersion": {"main", "bootstrap"},
		},
		Pkgs: map[string]*index.Pkg{
			"internal/mcp":    {Name: "mcp", Path: "internal/mcp", Imports: []index.ImportEdge{}, Files: []string{"internal/mcp/server_paths.go"}, Lang: "go"},
			"cmd/kern":        {Name: "kern", Path: "cmd/kern", Imports: []index.ImportEdge{{Path: "example.com/mod/internal/mcp", Confidence: index.ConfidenceHigh}}, Files: []string{"cmd/kern/main.go"}, Lang: "go"},
			"cmd/kern-server": {Name: "kern-server", Path: "cmd/kern-server", Imports: []index.ImportEdge{{Path: "example.com/mod/internal/mcp", Confidence: index.ConfidenceHigh}}, Files: []string{"cmd/kern-server/main.go"}, Lang: "go"},
		},
		PrecisionByLang: map[string]string{"go": "resolved"},
		UpdatedAt:       time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

// TestDirectCallersNamesQualifiedAndBare is the L4 regression: the raw-edge
// caller collector must report ALL of a symbol's callers — the bare
// same-package caller AND the qualified cross-package callers — including a
// qualified caller whose bare name is ambiguous across packages, which the
// adjacency/node-map path (WhoCallsPrecise) silently drops.
func TestDirectCallersNamesQualifiedAndBare(t *testing.T) {
	g := FromIndex(setServerVersionIndex())

	got := g.DirectCallersNames("SetServerVersion", false)
	if len(got) != 3 {
		t.Fatalf("DirectCallersNames(SetServerVersion) = %v; want 3 callers (bare + qualified)", got)
	}
	for _, want := range []string{"currentServerVersion", "main", "bootstrap"} {
		if !containsID(got, want) {
			t.Errorf("DirectCallersNames(SetServerVersion) = %v; missing caller %q", got, want)
		}
	}

	// The regression this fixes: WhoCallsPrecise cannot report the qualified
	// cross-package caller "main" — its bare name is shared by two packages,
	// so the adjacency marks it ambiguous ("?main") and nodesForIDs drops it.
	nodeIDs := names(g.WhoCallsPrecise("SetServerVersion", false))
	if !containsID(nodeIDs, "internal/mcp.currentServerVersion") || !containsID(nodeIDs, "cmd/kern-server.bootstrap") {
		t.Errorf("WhoCallsPrecise(SetServerVersion) = %v; want the bare and unique qualified callers", nodeIDs)
	}
	for _, id := range nodeIDs {
		if id == "cmd/kern.main" || id == "cmd/kern-server.main" {
			t.Errorf("WhoCallsPrecise(SetServerVersion) = %v; ambiguous \"main\" callers must be dropped (the bug DirectCallersNames fixes)", nodeIDs)
		}
	}

	// Strict precision mirrors the adjacency: the two resolved callers are
	// kept (Go is "resolved"), while the unresolvable "main" is unknown
	// rather than guessed.
	strict := g.DirectCallersNames("SetServerVersion", true)
	if len(strict) != 2 || containsID(strict, "main") {
		t.Errorf("strict DirectCallersNames(SetServerVersion) = %v; want the 2 resolved callers, ambiguous one skipped", strict)
	}
}

// foreignCalleeIndex builds the foreign-callee shape: "Save" exists in two
// packages, so a qualified reference only resolves via the caller's imports.
// Run links to db.Save through app's import of example.com/mod/db; Stranger
// calls the foreign "buf.Save", whose qualifier matches no import of app.
func foreignCalleeIndex() *index.Index {
	return &index.Index{
		Root: "/foreign",
		Symbols: []index.Symbol{
			sym("func", "Save", "db/store.go", 1),
			sym("func", "Save", "store/store.go", 1),
			sym("func", "Run", "app/run.go", 1),
			sym("func", "Stranger", "app/other.go", 1),
		},
		Calls: map[string][]index.CallEdge{
			"Run":      {{Target: "db.Save", Confidence: index.ConfidenceHigh}},
			"Stranger": {{Target: "buf.Save", Confidence: index.ConfidenceHigh}},
		},
		Callers: map[string][]string{
			"db.Save":  {"Run"},
			"buf.Save": {"Stranger"},
		},
		Pkgs: map[string]*index.Pkg{
			"db":    {Name: "db", Path: "db", Imports: []index.ImportEdge{{Path: "fmt", Confidence: index.ConfidenceHigh}}, Files: []string{"db/store.go"}, Lang: "go"},
			"store": {Name: "store", Path: "store", Imports: []index.ImportEdge{}, Files: []string{"store/store.go"}, Lang: "go"},
			"app":   {Name: "app", Path: "app", Imports: []index.ImportEdge{{Path: "example.com/mod/db", Confidence: index.ConfidenceHigh}, {Path: "example.com/mod/store", Confidence: index.ConfidenceHigh}}, Files: []string{"app/run.go", "app/other.go"}, Lang: "go"},
		},
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

// TestDirectCallersNamesForeignCalleeNotAttributed mirrors
// TestResolveEdgeEndpointForeignStaysUnlinked at the collector level: an
// unresolvable callee endpoint (qualifier matching no caller import) does
// NOT call the target, so its caller must not be attributed.
func TestDirectCallersNamesForeignCalleeNotAttributed(t *testing.T) {
	g := FromIndex(foreignCalleeIndex())

	got := g.DirectCallersNames("db.Save", false)
	if len(got) != 1 || !containsID(got, "Run") {
		t.Fatalf("DirectCallersNames(db.Save) = %v; want [Run] (the import-linked caller)", got)
	}
	if containsID(got, "Stranger") {
		t.Errorf("DirectCallersNames(db.Save) = %v; foreign callee buf.Save must not attribute Stranger", got)
	}
}

// ambiguousCallerIndex builds the shape this fix targets: "Save" exists in
// two packages and "New" in two packages, so BOTH endpoints of the recorded
// edge "New" -> "a.Save" are ambiguous bare names and ResolveEdgeEndpoint
// drops it (its caller pre-condition fails before the import-qualified
// callee resolution can run). The callee endpoint uses the import qualifier
// ("a.Save") while node IDs are package-path-qualified ("pkg/a.Save"), so
// resolveNodeID cannot match it directly either. Dumper calls the foreign
// "fmt.Println"; Stranger the foreign "buf.Save".
func ambiguousCallerIndex() *index.Index {
	return &index.Index{
		Root: "/amb",
		Symbols: []index.Symbol{
			sym("func", "Save", "pkg/a/store.go", 1),
			sym("func", "Save", "pkg/b/store.go", 1),
			sym("func", "New", "pkg/a/factory.go", 1),
			sym("func", "New", "pkg/c/factory.go", 1),
			sym("func", "Dumper", "pkg/c/dump.go", 1),
			sym("func", "Stranger", "pkg/c/other.go", 1),
		},
		Calls: map[string][]index.CallEdge{
			"New":      {{Target: "a.Save", Confidence: index.ConfidenceHigh}},
			"Dumper":   {{Target: "fmt.Println", Confidence: index.ConfidenceHigh}},
			"Stranger": {{Target: "buf.Save", Confidence: index.ConfidenceHigh}},
		},
		Callers: map[string][]string{
			"a.Save":      {"New"},
			"pkg/a.Save":  {"New"},
			"b.Save":      {"New"},
			"fmt.Println": {"Dumper"},
			"buf.Save":    {"Stranger"},
		},
		Pkgs: map[string]*index.Pkg{
			"pkg/a": {Name: "a", Path: "pkg/a", Imports: []index.ImportEdge{}, Files: []string{"pkg/a/store.go", "pkg/a/factory.go"}, Lang: "go"},
			"pkg/b": {Name: "b", Path: "pkg/b", Imports: []index.ImportEdge{}, Files: []string{"pkg/b/store.go"}, Lang: "go"},
			"pkg/c": {Name: "c", Path: "pkg/c", Imports: []index.ImportEdge{{Path: "example.com/mod/pkg/a", Confidence: index.ConfidenceHigh}}, Files: []string{"pkg/c/factory.go", "pkg/c/dump.go", "pkg/c/other.go"}, Lang: "go"},
		},
		UpdatedAt: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
	}
}

// TestDirectCallersNamesAmbiguousCallerQualifiedCallee is the regression for
// the caller pre-condition: an edge whose CALLEE endpoint resolves to the
// target must NOT be dropped just because the CALLER endpoint is an
// ambiguous bare name. "New" (two packages) -> "a.Save" (two packages): the
// callee IS pkg/a.Save (its qualifier names the target's package), so the
// caller must be reported — resolved package-aware to pkg/a's New.
func TestDirectCallersNamesAmbiguousCallerQualifiedCallee(t *testing.T) {
	g := FromIndex(ambiguousCallerIndex())
	got := g.DirectCallersNames("pkg/a.Save", false)
	if len(got) != 1 || !containsID(got, "New") {
		t.Fatalf("DirectCallersNames(pkg/a.Save) = %v; want [New] (ambiguous caller of an ambiguous callee)", got)
	}
	// Foreign qualified callees are still not attributed: neither fmt.Println
	// nor buf.Save calls pkg/a.Save.
	if containsID(got, "Dumper") {
		t.Errorf("DirectCallersNames(pkg/a.Save) = %v; foreign callee fmt.Println must not attribute Dumper", got)
	}
	if containsID(got, "Stranger") {
		t.Errorf("DirectCallersNames(pkg/a.Save) = %v; foreign callee buf.Save must not attribute Stranger", got)
	}
	// The callee-endpoint fix must not have widened the target: the other
	// same-named Save reports only its own caller.
	gotB := g.DirectCallersNames("pkg/b.Save", false)
	if len(gotB) != 0 {
		t.Errorf("DirectCallersNames(pkg/b.Save) = %v; want none (nobody calls it)", gotB)
	}
}

// authorizeContextIndex mirrors the repo scenario (deep-dive 2026-10-04):
// two same-named AuthorizeContext funcs (the governance core and its MCP
// wrapper), cross-package callers whose bare names are ambiguous (New,
// Generate) or identical to the target's (the wrapper), and an MCP wrapper
// test calling the wrapper bare. Node IDs are package-path-qualified
// ("internal/governance.AuthorizeContext") while the recorded callee
// endpoints use the import qualifier ("governance.AuthorizeContext"), so
// resolveNodeID cannot match them directly and the caller pre-condition of
// ResolveEdgeEndpoint fails for every ambiguous caller.
func authorizeContextIndex() *index.Index {
	return &index.Index{
		Root: "/kern",
		Symbols: []index.Symbol{
			sym("func", "AuthorizeContext", "internal/governance/authorize.go", 43),
			sym("func", "AuthorizeContext", "internal/mcp/governance/governance.go", 233),
			sym("func", "New", "internal/app/platform.go", 96),
			sym("func", "Generate", "internal/evidence/bundle.go", 153),
			sym("func", "guardAuthzVerdict", "cmd/kern/cmd_context.go", 775),
			sym("func", "runAuthorizeContext", "cmd/kern/cmd_authorize_context.go", 12),
			sym("func", "TestAuthorizeContextDenial", "internal/mcp/governance/governance_test.go", 216),
		},
		Calls: map[string][]index.CallEdge{
			"New":                        {{Target: "governance.AuthorizeContext", Confidence: index.ConfidenceHigh}},
			"Generate":                   {{Target: "governance.AuthorizeContext", Confidence: index.ConfidenceHigh}},
			"AuthorizeContext":           {{Target: "governance.AuthorizeContext", Confidence: index.ConfidenceHigh}},
			"guardAuthzVerdict":          {{Target: "governance.AuthorizeContext", Confidence: index.ConfidenceHigh}},
			"runAuthorizeContext":        {{Target: "governance.AuthorizeContext", Confidence: index.ConfidenceHigh}},
			"TestAuthorizeContextDenial": {{Target: "AuthorizeContext", Confidence: index.ConfidenceHigh}},
		},
		Callers: map[string][]string{
			"governance.AuthorizeContext": {"New", "Generate", "AuthorizeContext", "guardAuthzVerdict", "runAuthorizeContext"},
			"AuthorizeContext":            {"TestAuthorizeContextDenial"},
		},
		Pkgs: map[string]*index.Pkg{
			"internal/governance":     {Name: "governance", Path: "internal/governance", Imports: []index.ImportEdge{}, Files: []string{"internal/governance/authorize.go"}, Lang: "go"},
			"internal/mcp/governance": {Name: "governance", Path: "internal/mcp/governance", Imports: []index.ImportEdge{{Path: "example.com/kern/internal/governance", Confidence: index.ConfidenceHigh}}, Files: []string{"internal/mcp/governance/governance.go", "internal/mcp/governance/governance_test.go"}, Lang: "go"},
			"internal/app":            {Name: "app", Path: "internal/app", Imports: []index.ImportEdge{{Path: "example.com/kern/internal/governance", Confidence: index.ConfidenceHigh}}, Files: []string{"internal/app/platform.go"}, Lang: "go"},
			"internal/evidence":       {Name: "evidence", Path: "internal/evidence", Imports: []index.ImportEdge{{Path: "example.com/kern/internal/governance", Confidence: index.ConfidenceHigh}}, Files: []string{"internal/evidence/bundle.go"}, Lang: "go"},
			"cmd/kern":                {Name: "kern", Path: "cmd/kern", Imports: []index.ImportEdge{{Path: "example.com/kern/internal/governance", Confidence: index.ConfidenceHigh}}, Files: []string{"cmd/kern/cmd_context.go", "cmd/kern/cmd_authorize_context.go"}, Lang: "go"},
		},
		UpdatedAt: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
	}
}

// TestDirectCallersNamesAuthorizeContextScenario is the exact repo shape:
// the governance core's DirectCallersNames must report all five non-test
// callers — New, Generate, the MCP wrapper (bare name identical to the
// target's), runAuthorizeContext and guardAuthzVerdict — none of which
// ResolveEdgeEndpoint's caller pre-condition could previously keep.
func TestDirectCallersNamesAuthorizeContextScenario(t *testing.T) {
	g := FromIndex(authorizeContextIndex())
	got := g.DirectCallersNames("internal/governance.AuthorizeContext", false)
	for _, want := range []string{"New", "Generate", "AuthorizeContext", "runAuthorizeContext", "guardAuthzVerdict"} {
		if !containsID(got, want) {
			t.Errorf("DirectCallersNames(internal/governance.AuthorizeContext) = %v; missing caller %q", got, want)
		}
	}
	if len(got) != 5 {
		t.Errorf("DirectCallersNames(internal/governance.AuthorizeContext) = %v; want exactly the 5 non-test callers", got)
	}
	// The wrapper test calls the wrapper, not the core: it must not leak
	// into the core's caller list (the same-name merge this guards against).
	if containsID(got, "TestAuthorizeContextDenial") {
		t.Errorf("DirectCallersNames(internal/governance.AuthorizeContext) = %v; wrapper-only caller TestAuthorizeContextDenial must not be attributed to the core", got)
	}
}

// TestBlastRadiusPackageAware is the Defect B regression: the blast radius
// BFS must walk the same package-aware edges explore's direct-caller list
// uses (ix.CallersFor + findCallerDef) instead of the raw bare-name
// ix.Callers map. The wrapper "AuthorizeContext" is a real caller of the
// core but collapses into the name-keyed visited root, so its own callers
// (TestAuthorizeContextDenial) never enter the core's radius, and the
// "Generate" entry resolves to evidence's symbol (bundle.go), not the
// first-match name→file mapping.
func TestBlastRadiusPackageAware(t *testing.T) {
	ix := authorizeContextIndex()
	radius, radiusSyms, _, _ := blastRadiusWalk(ix, []string{"AuthorizeContext"}, false)
	for _, want := range []string{"AuthorizeContext", "New", "Generate", "runAuthorizeContext", "guardAuthzVerdict"} {
		if !containsID(radius, want) {
			t.Errorf("BlastRadius(AuthorizeContext) = %v; missing %q", radius, want)
		}
	}
	if containsID(radius, "TestAuthorizeContextDenial") {
		t.Errorf("BlastRadius(AuthorizeContext) = %v; wrapper-only TestAuthorizeContextDenial must not be in the core's radius", radius)
	}
	// The resolved symbol behind the "Generate" name is evidence's
	// (bundle.go), never the first same-named symbol in the index.
	for i, s := range radius {
		if s == "Generate" {
			if radiusSyms[i].File != "internal/evidence/bundle.go" {
				t.Errorf("BlastRadius(AuthorizeContext) Generate symbol = %s; want internal/evidence/bundle.go (same-name merge leaked a different Generate)", radiusSyms[i].File)
			}
		}
	}
}

// TestBlastRadiusParallelSymbols is the F1 regression (live campaign
// 2026-10-04): a radius member that resolves to no indexed symbol — the
// Python/JS heuristic-caller case, where blast-radius leaves are raw
// endpoints findCallerDef cannot resolve — must keep its outSyms slot as a
// zero-value Symbol instead of shifting every later entry. The pre-fix
// filtered append broke the positional out[i]↔outSyms[i] contract, which
// both misattributed BlastFiles and made ExploreBudgeted's depth cap panic
// ("index out of range") on every Python-class and JS-function symbol
// explored via the CLI; Go graphs never trip it because every endpoint
// resolves.
func TestBlastRadiusParallelSymbols(t *testing.T) {
	ix := &index.Index{
		Root: "/proj",
		Symbols: []index.Symbol{
			sym("func", "run_backtest", "app/backtest.py", 10),
		},
		Calls: map[string][]index.CallEdge{},
		Callers: map[string][]string{
			"run_backtest": {"ZzUnresolvedHelper"},
		},
		Pkgs: map[string]*index.Pkg{
			"app": {Name: "app", Path: "app", Imports: []index.ImportEdge{}, Files: []string{"app/backtest.py"}, Lang: "python"},
		},
		UpdatedAt: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
	}
	out, outSyms, _, _ := blastRadiusWalk(ix, []string{"run_backtest"}, false)
	if len(out) != len(outSyms) {
		t.Fatalf("blastRadiusWalk parallel contract broken: len(out)=%d len(outSyms)=%d (an unresolved member must keep its zero-value slot, not shift the slice)", len(out), len(outSyms))
	}
	for i, s := range out {
		if s == "run_backtest" && outSyms[i].File != "app/backtest.py" {
			t.Errorf("blastRadiusWalk: out[%d]=%q paired with %q; want app/backtest.py", i, s, outSyms[i].File)
		}
		if s == "ZzUnresolvedHelper" && outSyms[i].File != "" {
			t.Errorf("blastRadiusWalk: unresolved member %q paired with %q; want zero-value Symbol (no file)", s, outSyms[i].File)
		}
	}
	// The crash path itself: ExploreBudgeted's depth cap indexes
	// radiusSyms[i] positionally — with the pre-fix filtered slice this
	// panicked instead of returning a report.
	if _, err := ExploreBudgeted(ix, "run_backtest", 2, 30, "", 0); err != nil {
		t.Errorf("ExploreBudgeted(run_backtest) error = %v; want nil (pre-fix this panicked with index out of range)", err)
	}
}
