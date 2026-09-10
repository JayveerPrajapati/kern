package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommunityLabelsSmallRepo(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	labels := ix.CommunityLabels()
	if len(labels) == 0 {
		t.Fatalf("expected non-empty labels for a small repo, got none")
	}
}

func TestCommunityLabelsGatedLargeRepo(t *testing.T) {
	ix := &Index{}
	for i := 0; i < MaxCommunitySymbols+1; i++ {
		ix.Symbols = append(ix.Symbols, Symbol{
			Kind: "func", Name: "f", File: "x.go", Line: i + 1,
		})
	}
	if labels := ix.CommunityLabels(); len(labels) != 0 {
		t.Fatalf("expected empty labels above the symbol gate, got %d", len(labels))
	}
}

// K4 regression: external/stdlib callees (e.g. "Date", "List.of") must not
// appear as community nodes even when a bare-name fallback could resolve them
// to a project symbol. Only project-local symbols should participate in
// community clustering.
func TestCommunityLabelsExcludesExternalCallees(t *testing.T) {
	ix := &Index{
		Symbols: []Symbol{
			{Kind: "func", Name: "processData", File: "main.go", Line: 1},
			{Kind: "func", Name: "validateInput", File: "main.go", Line: 10},
			{Kind: "func", Name: "formatOutput", File: "main.go", Line: 20},
			// A project symbol whose bare name "of" could be a fallback target
			// for an external call like "List.of" — the old resolveName fallback
			// would have let "List.of" resolve to this and become a node.
			{Kind: "func", Name: "of", File: "factory.go", Line: 1},
		},
		Calls: map[string][]CallEdge{
			"processData":   {CallEdge{Target: "validateInput", Confidence: ConfidenceHigh}, CallEdge{Target: "formatOutput", Confidence: ConfidenceHigh}, CallEdge{Target: "Date", Confidence: ConfidenceHigh}, CallEdge{Target: "List.of", Confidence: ConfidenceHigh}},
			"validateInput": {CallEdge{Target: "of", Confidence: ConfidenceHigh}},
			"formatOutput":  {CallEdge{Target: "processData", Confidence: ConfidenceHigh}},
		},
	}
	labels := ix.CommunityLabels()
	// External callees must NOT be community nodes.
	for ext := range map[string]bool{"Date": true, "List.of": true} {
		if _, ok := labels[ext]; ok {
			t.Errorf("external callee %q must not be a community node, but got label %q", ext, labels[ext])
		}
	}
	// Project-local symbols SHOULD be community nodes.
	for _, local := range []string{"processData", "validateInput", "formatOutput", "of"} {
		if _, ok := labels[local]; !ok {
			t.Errorf("project symbol %q should be a community node, but it's missing", local)
		}
	}
}

// Spring DI dispatch regression: when a caller invokes a method on an interface
// type, virtual edges must be added to all concrete implementations of that
// method. This is what makes walk/path/dead/communities work for DI frameworks.
func TestAddDispatchEdgesResolvesInterfaceCalls(t *testing.T) {
	ix := &Index{
		Symbols: []Symbol{
			{Kind: "interface", Name: "NotificationService", File: "Iface.java", Line: 1},
			{Kind: "method", Name: "send", Receiver: "NotificationService", File: "Iface.java", Line: 2},
			{Kind: "class", Name: "EmailServiceImpl", File: "EmailImpl.java", Line: 1},
			{Kind: "method", Name: "send", Receiver: "EmailServiceImpl", File: "EmailImpl.java", Line: 5},
			{Kind: "class", Name: "SMSServiceImpl", File: "SMSImpl.java", Line: 1},
			{Kind: "method", Name: "send", Receiver: "SMSServiceImpl", File: "SMSImpl.java", Line: 5},
			{Kind: "class", Name: "Controller", File: "Controller.java", Line: 1},
			{Kind: "method", Name: "handleRequest", Receiver: "Controller", File: "Controller.java", Line: 5},
		},
		Calls: map[string][]CallEdge{
			"Controller.handleRequest": {CallEdge{Target: "NotificationService.send", Confidence: ConfidenceHigh}},
		},
		Inherits: map[string][]string{
			"EmailServiceImpl": {"implements:NotificationService"},
			"SMSServiceImpl":   {"implements:NotificationService"},
		},
		Callers:     map[string][]string{},
		InheritedBy: map[string][]string{},
	}
	// computeCallers populates InheritedBy from Inherits, and Callers from Calls.
	ix.computeCallers()
	ix.addDispatchEdges()

	// The caller should now have virtual edges to both implementations.
	callees := ix.Calls["Controller.handleRequest"]
	hasEmail := false
	hasSMS := false
	for _, ce := range callees {
		if ce.Target == "EmailServiceImpl.send" {
			hasEmail = true
		}
		if ce.Target == "SMSServiceImpl.send" {
			hasSMS = true
		}
	}
	if !hasEmail {
		t.Errorf("expected virtual edge to EmailServiceImpl.send, got callees: %v", callees)
	}
	if !hasSMS {
		t.Errorf("expected virtual edge to SMSServiceImpl.send, got callees: %v", callees)
	}

	// Reverse: both implementations should now have the controller as a caller.
	if !containsStr(ix.Callers["EmailServiceImpl.send"], "Controller.handleRequest") {
		t.Errorf("EmailServiceImpl.send should have Controller.handleRequest as a caller")
	}
	if !containsStr(ix.Callers["SMSServiceImpl.send"], "Controller.handleRequest") {
		t.Errorf("SMSServiceImpl.send should have Controller.handleRequest as a caller")
	}
}

// TestCallersIncludingAliases pins the constructor-inferred-receiver case:
// `s := New(); s.M()` is recorded as "New.M" (the constructor name stands in
// for the receiver type), so the canonical Callers map misses the caller and
// only the simple-name alias has it. The merged accessor must find it — the
// deletion/dead-code safety net.
func TestCallersIncludingAliases(t *testing.T) {
	// An UNDEFINED constructor (no symbol, nowhere declared) cannot be
	// rewritten by the merge pass: the edge stays qualified on the variable
	// name ("mystery.M") and only the alias layer has it — the net
	// CallersIncludingAliases must still find the caller.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.go"), []byte(`package s

type S struct{}

func (s *S) M() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "use.go"), []byte(`package s

func Use() {
	x := mystery()
	x.M()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The canonical map must NOT see the caller under the method's full name
	// (unresolvable receiver), while the merged view must.
	if got := ix.Callers["S.M"]; len(got) != 0 {
		t.Fatalf("canonical Callers[S.M] = %v, want empty (unresolvable receiver)", got)
	}
	got := ix.CallersIncludingAliases("S.M")
	if len(got) != 1 || got[0] != "Use" {
		t.Fatalf("CallersIncludingAliases(S.M) = %v, want [Use]", got)
	}
	// A symbol with no aliases behaves identically to the canonical lookup.
	if got := ix.CallersIncludingAliases("Nonexistent"); len(got) != 0 {
		t.Fatalf("CallersIncludingAliases(Nonexistent) = %v, want empty", got)
	}
}

// TestConstructorReturnTypeInferred pins the source fix: with the constructor
// in the SAME file as the call, `x := New(...)` infers the real receiver type
// and the edge lands in the canonical Callers map under the method's full
// name — no alias needed.
func TestConstructorReturnTypeInferred(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte(`package lib

type S struct{}

func New() *S { return &S{} }

func (s *S) M() {}

func Use() {
	x := New()
	x.M()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Callers["S.M"]
	if len(got) != 1 || got[0] != "Use" {
		t.Fatalf("canonical Callers[S.M] = %v, want [Use] (same-file constructor resolved to the return type)", got)
	}
}

// TestMultiValueReceiverVarResolved pins the final lens class: a receiver
// variable from a MULTI-VALUE method-constructor assign
// ("gov, err := s.newGov(...)") must resolve through the merge-time rewrite
// to the real receiver type ("Gov.Filter"), so canonical Callers sees the
// caller even when the constructor is declared in a different file.
func TestMultiValueReceiverVarResolved(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ctor.go"), []byte(`package lib

type Gov struct{}

type Server struct{}

func (s *Server) newGov() (*Gov, error) { return &Gov{}, nil }

func (g *Gov) Filter() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "use.go"), []byte(`package lib

func Use(s *Server) {
	g, err := s.newGov()
	_ = err
	g.Filter()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Callers["Gov.Filter"]
	if len(got) != 1 || got[0] != "Use" {
		t.Fatalf("canonical Callers[Gov.Filter] = %v, want [Use] (multi-value receiver var resolved via constructor return type)", got)
	}
}
