package index

import (
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
		Calls: map[string][]string{
			"processData":   {"validateInput", "formatOutput", "Date", "List.of"},
			"validateInput": {"of"},
			"formatOutput":  {"processData"},
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
		Calls: map[string][]string{
			"Controller.handleRequest": {"NotificationService.send"},
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
	for _, c := range callees {
		if c == "EmailServiceImpl.send" {
			hasEmail = true
		}
		if c == "SMSServiceImpl.send" {
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
