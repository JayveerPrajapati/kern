package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCalleeNameChainedAndAsserted pins the extractor's callee-name
// coverage for expression shapes that previously produced bare ".Method"
// targets: chained calls (json.NewEncoder(w).Encode(x)), type assertions
// (v.(*T).M()), pointer derefs ((*x).M()) and unary expressions. A bare
// ".Encode" target can never resolve; the qualified forms at least name the
// real function chain.
func TestCalleeNameChainedAndAsserted(t *testing.T) {
	src := `package p
import "encoding/json"
func f(w interface{}) {
	json.NewEncoder(w).Encode("x")
	v, _ := w.(interface{ M() })
	v.M()
	(*w2p).M()
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if ce, ok := n.(*ast.CallExpr); ok {
				got = append(got, calleeName(ce.Fun))
			}
			return true
		})
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"json.NewEncoder", "json.NewEncoder.Encode"} {
		if !strings.Contains(joined, want) {
			t.Errorf("callee names %q: missing %q (bare-dot regression)", got, want)
		}
	}
	for _, bad := range got {
		if strings.HasPrefix(bad, ".") {
			t.Errorf("bare-dot callee produced: %q (full set %q)", bad, got)
		}
	}
	if !strings.Contains(joined, "M") {
		t.Errorf("type-asserted call v.M() not recorded: %q", got)
	}
}

// TestMeasureCallResolutionOnRealFixture builds a small two-package Go
// project with a mix of resolvable and external call targets and pins the
// honest resolution counters: same-package funcs and receiver-var methods
// resolve; stdlib and chained external calls stay unresolved; no bare-dot
// targets are recorded.
func TestMeasureCallResolutionOnRealFixture(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api", "service.go"), []byte(`package api
type Service struct{}
func (s *Service) Do() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
import (
	"encoding/json"
	"fmt"
	"example.com/app/api"
)
func helper() {}
func main() {
	helper()
	var s api.Service
	s.Do()
	fmt.Println("external")
	json.NewEncoder(nil).Encode("chained")
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Chained call must record the qualified target, never ".Encode".
	for _, callees := range ix.Calls {
		for _, ce := range callees {
			if strings.HasPrefix(ce.Target, ".") {
				t.Fatalf("bare-dot callee leaked into index: %q", ce.Target)
			}
		}
	}
	// helper and Service.Do resolve (receiver var + same package);
	// fmt.Println, json.NewEncoder and the chained json.NewEncoder.Encode
	// are external and correctly stay unresolved.
	stats := ix.CallResolution
	if stats.Total != 5 {
		t.Fatalf("CallResolution.Total = %d, want 5 (callees: %v)", stats.Total, collectTargets(ix))
	}
	if stats.Unresolved != 3 {
		t.Fatalf("CallResolution.Unresolved = %d, want 3 (external stdlib targets)", stats.Unresolved)
	}
	if got := ix.Callers["Service.Do"]; len(got) != 1 || got[0] != "main" {
		t.Fatalf("Callers[Service.Do] = %v, want [main]", got)
	}
}

func collectTargets(ix *Index) []string {
	var out []string
	for _, callees := range ix.Calls {
		for _, ce := range callees {
			out = append(out, ce.Target)
		}
	}
	return out
}
