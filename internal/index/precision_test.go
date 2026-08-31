package index

import "testing"

// TestPrecisionByLangPopulated verifies Build records the edge-precision tier
// per language: Go is fully resolved (go/ast cross-file binding resolution),
// Java is "resolved" in the regex build via local-type tracking + callee
// resolution (v.method() -> Type.method() binds cross-file; "ast" under the
// tree-sitter build, whose call edges are still receiver-var heuristics), and
// other foreign languages are "ast" under the tree-sitter build or
// "heuristic" (regex) otherwise.
func TestPrecisionByLangPopulated(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"goapp/main.go": `package main

func main() {
	run()
}

func run() {}
`,
		"web/app.ts": `export function handle(): void {
	helper();
}

function helper(): void {}
`,
		"java/App.java": `package app;

public class App {
	public void run() {
		util();
	}

	public void util() {}
}
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.PrecisionByLang["go"]; got != "resolved" {
		t.Errorf("PrecisionByLang[go] = %q; want resolved", got)
	}
	wantJava := "resolved"
	if treesitterEnabled() {
		wantJava = "ast"
	}
	if got := ix.PrecisionByLang["java"]; got != wantJava {
		t.Errorf("PrecisionByLang[java] = %q; want %s", got, wantJava)
	}
	wantForeign := "ast"
	if !treesitterEnabled() {
		wantForeign = "heuristic"
	}
	if got := ix.PrecisionByLang["typescript"]; got != wantForeign {
		t.Errorf("PrecisionByLang[typescript] = %q; want %s", got, wantForeign)
	}
}
