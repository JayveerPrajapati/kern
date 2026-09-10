package index

import (
	"testing"
)

// TestConfidenceString verifies the level rendering, including the zero
// value which must never masquerade as HIGH.
func TestConfidenceString(t *testing.T) {
	if got := ConfidenceHigh.String(); got != "HIGH" {
		t.Errorf("ConfidenceHigh.String() = %q, want HIGH", got)
	}
	if got := ConfidenceMedium.String(); got != "MEDIUM" {
		t.Errorf("ConfidenceMedium.String() = %q, want MEDIUM", got)
	}
	if got := ConfidenceLow.String(); got != "LOW" {
		t.Errorf("ConfidenceLow.String() = %q, want LOW", got)
	}
	if got := Confidence("").String(); got != "LOW" {
		t.Errorf("zero Confidence.String() = %q, want LOW", got)
	}
}

// TestParseConfidence verifies persisted-value parsing: known levels round-trip
// and empty/garbage values fall back to MEDIUM.
func TestParseConfidence(t *testing.T) {
	cases := map[string]Confidence{
		"HIGH":   ConfidenceHigh,
		"MEDIUM": ConfidenceMedium,
		"LOW":    ConfidenceLow,
		"":       ConfidenceMedium,
		"??":     ConfidenceMedium,
	}
	for in, want := range cases {
		if got := parseConfidence(in); got != want {
			t.Errorf("parseConfidence(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCallEdgeTargets verifies the projection helper used to keep string-based
// call consumers working alongside the confident edge representation.
func TestCallEdgeTargets(t *testing.T) {
	edges := []CallEdge{
		{Target: "b", Confidence: ConfidenceHigh},
		{Target: "a", Confidence: ConfidenceLow},
	}
	got := CallEdgeTargets(edges)
	want := []string{"b", "a"}
	if len(got) != len(want) {
		t.Fatalf("CallEdgeTargets length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("CallEdgeTargets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if CallEdgeTargets(nil) != nil {
		t.Error("CallEdgeTargets(nil) must be nil")
	}
}

// TestGoSymbolConfidence: explicit declarations score HIGH.
func TestGoSymbolConfidence(t *testing.T) {
	src := `package main

import "strings"

type User struct{ Name string }

type Greeter interface{ Greet() string }

type Alias = string

const Max = 10

var Count = 1

func New() *User { return &User{} }

func (u *User) Login() bool { return u.Name != "" }

func greet(s string) string { return strings.ToUpper(s) }
`
	syms, _, _, _, err := extract("main.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Confidence{
		"User":       ConfidenceHigh, // struct
		"Greeter":    ConfidenceHigh, // interface
		"Alias":      ConfidenceHigh, // type alias
		"Max":        ConfidenceHigh, // const
		"Count":      ConfidenceHigh, // var
		"New":        ConfidenceHigh, // func
		"User.Login": ConfidenceHigh, // method
		"greet":      ConfidenceHigh, // func
	}
	got := map[string]Confidence{}
	for _, s := range syms {
		got[s.FullName()] = s.Confidence
	}
	for name, wantConf := range want {
		if c, ok := got[name]; !ok || c != wantConf {
			t.Errorf("symbol %s confidence = %q (present=%v), want %q", name, c, ok, wantConf)
		}
	}
}

// TestGoEntrySymbolConfidence: standalone entry points are regex/pattern
// heuristics, so they score LOW.
func TestGoEntrySymbolConfidence(t *testing.T) {
	src := `package main

import "net/http"

func main() {
	http.HandleFunc("/health", notDeclaredHandler)
}
`
	syms, _, _, _, err := extract("main.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range syms {
		if s.Kind == "entry" {
			found = true
			if s.Confidence != ConfidenceLow {
				t.Errorf("entry symbol %s confidence = %q, want LOW", s.FullName(), s.Confidence)
			}
		}
	}
	if !found {
		t.Error("expected a standalone entry symbol for the unresolved route handler")
	}
}

// TestGoCallConfidence: direct syntactic calls are HIGH; callees resolved
// through receiver-var/constructor type inference are MEDIUM; package-level
// initializer calls are HIGH.
func TestGoCallConfidence(t *testing.T) {
	src := `package main

import "strings"

type Store struct{}

func NewStore() *Store { return &Store{} }

func (s *Store) Get() string { return "x" }

var db = NewStore()

func run() {
	s := NewStore()
	_ = s.Get()
	_ = strings.ToUpper("a")
}
`
	_, calls, _, _, err := extract("main.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	assertEdgeConf := func(owner, target string, want Confidence) {
		t.Helper()
		for _, ce := range calls[owner] {
			if ce.Target == target {
				if ce.Confidence != want {
					t.Errorf("call %s->%s confidence = %q, want %q", owner, target, ce.Confidence, want)
				}
				return
			}
		}
		t.Errorf("call %s->%s not found in %v", owner, target, CallEdgeTargets(calls[owner]))
	}
	// Direct call to a package function: HIGH.
	assertEdgeConf("run", "strings.ToUpper", ConfidenceHigh)
	// Direct constructor call: HIGH.
	assertEdgeConf("run", "NewStore", ConfidenceHigh)
	// Receiver-var method call resolved via local types ("s.Get" -> "Store.Get"):
	// MEDIUM.
	assertEdgeConf("run", "Store.Get", ConfidenceMedium)
	// Package-level initializer call recorded under the declared var: HIGH.
	assertEdgeConf("db", "NewStore", ConfidenceHigh)
}

// TestGoImportConfidence: Go import statements are direct AST facts: HIGH.
func TestGoImportConfidence(t *testing.T) {
	src := `package main

import (
	"fmt"
	"strings"
)
`
	_, _, _, pkg, err := extract("main.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if pkg == nil || len(pkg.Imports) != 2 {
		t.Fatalf("pkg.Imports = %v, want 2 imports", pkg)
	}
	byPath := map[string]Confidence{}
	for _, ie := range pkg.Imports {
		byPath[ie.Path] = ie.Confidence
	}
	for _, p := range []string{"fmt", "strings"} {
		if byPath[p] != ConfidenceHigh {
			t.Errorf("import %s confidence = %q, want HIGH", p, byPath[p])
		}
	}
}

// TestForeignCallAndImportConfidence: the regex path marks calls MEDIUM (name
// heuristics) and imports HIGH (anchored single-line statements).
func TestForeignCallAndImportConfidence(t *testing.T) {
	src := `package com.example;

import java.util.List;
import com.example.service.UserService;

public class App {
    public void run(UserService svc) {
        svc.load();
        List.of(1, 2);
    }
}
`
	syms, calls, _, pkg, err := extractForeign("App.java", []byte(src), "java")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Confidence != ConfidenceHigh {
			t.Errorf("symbol %s confidence = %q, want HIGH", s.FullName(), s.Confidence)
		}
	}
	for owner, edges := range calls {
		for _, ce := range edges {
			if ce.Confidence != ConfidenceMedium {
				t.Errorf("call %s->%s confidence = %q, want MEDIUM (regex tier)", owner, ce.Target, ce.Confidence)
			}
		}
	}
	if pkg == nil || len(pkg.Imports) != 2 {
		t.Fatalf("pkg.Imports = %v, want 2 imports", pkg)
	}
	for _, ie := range pkg.Imports {
		if ie.Confidence != ConfidenceHigh {
			t.Errorf("import %s confidence = %q, want HIGH", ie.Path, ie.Confidence)
		}
	}
}

// TestDispatchEdgesLowConfidence: virtual edges added through the inheritance
// graph are dynamic dispatch — LOW. Go has no implicit "implements" edges, so
// the index is hand-built the same way communities_test.go exercises dispatch.
func TestDispatchEdgesLowConfidence(t *testing.T) {
	ix := &Index{
		Symbols: []Symbol{
			{Kind: "interface", Name: "NotificationService", File: "notify.java", Line: 1},
			{Kind: "class", Name: "EmailServiceImpl", File: "notify.java", Line: 5},
			{Kind: "method", Name: "send", Receiver: "EmailServiceImpl", File: "notify.java", Line: 6},
			{Kind: "method", Name: "handleRequest", Receiver: "Controller", File: "notify.java", Line: 10},
		},
		Calls: map[string][]CallEdge{
			"Controller.handleRequest": {CallEdge{Target: "NotificationService.send", Confidence: ConfidenceMedium}},
		},
		Inherits: map[string][]string{
			"EmailServiceImpl": {"implements:NotificationService"},
		},
		Callers:     map[string][]string{},
		InheritedBy: map[string][]string{},
	}
	ix.computeCallers()
	ix.addDispatchEdges()

	for _, ce := range ix.Calls["Controller.handleRequest"] {
		if ce.Target == "EmailServiceImpl.send" {
			if ce.Confidence != ConfidenceLow {
				t.Errorf("dispatch edge confidence = %q, want LOW", ce.Confidence)
			}
			return
		}
	}
	t.Errorf("dispatch edge Controller.handleRequest->EmailServiceImpl.send not found; calls = %v", ix.Calls["Controller.handleRequest"])
}

// TestBuildCarriesConfidence: a full Build propagates confidence into the
// index for symbols, call edges and imports.
func TestBuildCarriesConfidence(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Symbols carry HIGH confidence.
	for _, s := range ix.Symbols {
		if s.Confidence != ConfidenceHigh {
			t.Errorf("symbol %s confidence = %q, want HIGH", s.FullName(), s.Confidence)
		}
	}
	// Direct call edges carry HIGH.
	if !edgeHasConfidence(ix, "main", "greet", ConfidenceHigh) {
		t.Errorf("main->greet missing HIGH edge; calls = %v", ix.Calls["main"])
	}
	// The receiver-var call greet(u.Name) inside Login resolves to a direct
	// call on a declared receiver — still HIGH (no local-type rewrite needed
	// here since the receiver is the method's own).
	if !edgeHasConfidence(ix, "User.Login", "greet", ConfidenceHigh) {
		t.Errorf("User.Login->greet missing HIGH edge; calls = %v", ix.Calls["User.Login"])
	}
	// Imports recorded per file carry HIGH.
	if imp := ix.ImportsByFile["main.go"]; len(imp) != 1 || imp[0].Path != "strings" || imp[0].Confidence != ConfidenceHigh {
		t.Errorf("main.go imports = %v, want HIGH strings", imp)
	}
}

func edgeHasConfidence(ix *Index, owner, target string, want Confidence) bool {
	for _, ce := range ix.Calls[owner] {
		if ce.Target == target && ce.Confidence == want {
			return true
		}
	}
	return false
}
