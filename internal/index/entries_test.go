package index

import (
	"testing"
)

func TestGoNetHTTPEntries(t *testing.T) {
	src := `package main

import "net/http"

func main() {
	http.HandleFunc("/", indexHandler)
	mux := http.NewServeMux()
	mux.HandleFunc("/api", apiHandler)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {}
func apiHandler(w http.ResponseWriter, r *http.Request) {}
`
	syms, _, _, _, err := extract("main.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	ih := findSym(syms, "indexHandler")
	if ih == nil || !ih.Entry || ih.Framework != "net-http" || ih.Route != "/" {
		t.Fatalf("expected indexHandler net-http entry, got %+v", ih)
	}
	ah := findSym(syms, "apiHandler")
	if ah == nil || !ah.Entry || ah.Route != "/api" {
		t.Fatalf("expected apiHandler entry /api, got %+v", ah)
	}
}

func TestGoRouterVerbEntries(t *testing.T) {
	src := `package main

import "github.com/gin-gonic/gin"

func main() {
	r := gin.Default()
	r.GET("/users", getUsers)
	r.POST("/users", func(c *gin.Context) {})
}

func getUsers(c *gin.Context) {}
`
	syms, _, _, _, err := extract("main.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	gu := findSym(syms, "getUsers")
	if gu == nil || !gu.Entry || gu.Framework != "http-route" || gu.Route != "/users" {
		t.Fatalf("expected getUsers http-route entry, got %+v", gu)
	}
	var anon bool
	for _, s := range syms {
		if s.Kind == "entry" && s.Name == "POST /users" {
			anon = true
		}
	}
	if !anon {
		t.Fatal("expected standalone entry for anonymous POST /users")
	}
}

func TestResolveEntriesCrossFile(t *testing.T) {
	files := map[string]string{
		"main.go": `package main

import "net/http"

func main() {
	http.HandleFunc("/", rootHandler)
}
`,
		"handlers.go": `package main

import "net/http"

func rootHandler(w http.ResponseWriter, r *http.Request) {}
`,
	}
	ix := buildTestIndex(t, files)
	rh := findSym(ix.Symbols, "rootHandler")
	if rh == nil || !rh.Entry {
		t.Fatalf("expected rootHandler resolved as entry cross-file, got %+v", rh)
	}
	// The route must be copied onto the cross-file-resolved handler: the
	// endpoint list would otherwise carry empty routes for every handler
	// registered from another file.
	if rh.Route != "/" {
		t.Fatalf("expected rootHandler Route / (copied from the entry marker), got %q", rh.Route)
	}
	if rh.Framework != "net-http" {
		t.Fatalf("expected rootHandler Framework net-http, got %q", rh.Framework)
	}
}

func TestRouteSearchMatchesRoutes(t *testing.T) {
	files := map[string]string{
		"app.py": `from flask import Flask
app = Flask(__name__)

@app.route("/admin/users")
def admin_users():
    return "x"
`,
	}
	ix := buildTestIndex(t, files)
	got := ix.Search("entry */admin*", 10)
	if len(got) == 0 {
		t.Fatal("search for route /admin/users returned nothing")
	}
	if got[0].Name != "admin_users" || got[0].Route != "/admin/users" || !got[0].Entry {
		t.Fatalf("unexpected search hit: %+v", got[0])
	}
	// the route itself is matchable with the plain route wildcard too
	byRoute := ix.Search("*admin*", 10)
	if len(byRoute) == 0 || byRoute[0].Route != "/admin/users" {
		t.Fatalf("route-pattern search failed: %+v", byRoute)
	}
}

func buildTestIndex(t *testing.T, files map[string]string) *Index {
	t.Helper()
	root := writeTree(t, files)
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestIgnoreDirsExcludesAgentConfigs(t *testing.T) {
	ix := buildTestIndex(t, map[string]string{
		"main.go":                   "package main\nfunc main() {}\n",
		".cursor/mcp.json":          `{"mcpServers":{"kern":{"type":"stdio"}}}`,
		".gemini/settings.json":     `{"hooks":{}}`,
		".kiro/settings/mcp.json":   `{"mcpServers":{}}`,
		".claude/settings.json":     `{"hooks":{}}`,
		".opencode/plugins/kern.ts": "import { tool } from \"@opencode-ai/plugin\"\n",
		".git/config":               "[core]\n",
	})
	for f := range ix.FileHashes {
		if f == ".cursor/mcp.json" || f == ".gemini/settings.json" || f == ".kiro/settings/mcp.json" ||
			f == ".claude/settings.json" || f == ".opencode/plugins/kern.ts" || f == ".git/config" {
			t.Errorf("agent/tooling config was indexed: %s", f)
		}
	}
	for f := range ix.FileHashes {
		if f == "main.go" {
			return
		}
	}
	t.Errorf("real source was not indexed; got %v", ix.FileHashes)
}
