package index

import (
	"testing"
)

func entryIn(t *testing.T, lang, rel, src string) []Symbol {
	t.Helper()
	syms, _, _, _, err := extractForeign(rel, []byte(src), lang)
	if err != nil {
		t.Fatalf("extractForeign: %v", err)
	}
	return syms
}

func entriesOf(syms []Symbol) []Symbol {
	var out []Symbol
	for _, s := range syms {
		if s.Entry {
			out = append(out, s)
		}
	}
	return out
}

func findEntry(syms []Symbol, framework string) *Symbol {
	for i := range syms {
		if syms[i].Entry && syms[i].Framework == framework {
			return &syms[i]
		}
	}
	return nil
}

func TestJavaSpringRestController(t *testing.T) {
	src := `package com.example;

import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api")
public class UserController {

    @GetMapping("/users")
    public String list() { return "hi"; }

    @PostMapping
    public String create() { return "ok"; }
}
`
	syms := entryIn(t, "java", "UserController.java", src)
	list := findSym(syms, "UserController.list")
	if list == nil || !list.Entry || list.Framework != "spring-mvc" || list.Route != "/api/users" {
		t.Fatalf("expected UserController.list entry route /api/users, got %+v", list)
	}
	create := findSym(syms, "UserController.create")
	if create == nil || !create.Entry || create.Route != "/api" {
		t.Fatalf("expected UserController.create route /api, got %+v", create)
	}
}

func TestJavaSpringBootApplication(t *testing.T) {
	src := `@SpringBootApplication
public class Application {
    public static void main(String[] args) {}
}
`
	syms := entryIn(t, "java", "Application.java", src)
	e := findSym(syms, "Application")
	if e == nil || e.Kind != "class" || e.Entry {
		t.Fatalf("expected Application class symbol, got %+v", e)
	}
	app := findEntry(syms, "spring-boot")
	if app == nil || app.Kind != "entry" || app.Route != "" {
		t.Fatalf("expected spring-boot entry for Application, got %+v", app)
	}
}

func TestPythonFlaskRoutes(t *testing.T) {
	src := `from flask import Flask
app = Flask(__name__)

@app.route("/")
def index():
    return "hi"

@app.post("/users")
def create_user():
    return "ok"
`
	syms := entryIn(t, "python", "app.py", src)
	index := findSym(syms, "index")
	if index == nil || !index.Entry || index.Framework != "python-http" || index.Route != "/" {
		t.Fatalf("expected index entry route /, got %+v", index)
	}
	cu := findSym(syms, "create_user")
	if cu == nil || !cu.Entry || cu.Route != "/users" {
		t.Fatalf("expected create_user route /users, got %+v", cu)
	}
}

func TestPythonFastAPIRouter(t *testing.T) {
	src := `from fastapi import APIRouter

router = APIRouter()

@router.get("/items/{id}")
def get_item(id: int):
    return id
`
	syms := entryIn(t, "python", "api.py", src)
	gi := findSym(syms, "get_item")
	if gi == nil || !gi.Entry || gi.Framework != "fastapi" || gi.Route != "/items/{id}" {
		t.Fatalf("expected get_item fastapi entry, got %+v", gi)
	}
}

func TestPythonDjangoUrls(t *testing.T) {
	src := `from django.urls import path
from . import views

urlpatterns = [
    path("users/", views.index),
    path("users/<int:pk>/", views.detail, name="detail"),
]
`
	syms := entryIn(t, "python", "urls.py", src)
	es := entriesOf(syms)
	if len(es) < 2 {
		t.Fatalf("expected 2 django route entries, got %d: %+v", len(es), es)
	}
	if es[0].Framework != "django" || es[0].Route != "/users" || es[0].Name != "index" {
		t.Fatalf("unexpected first entry: %+v", es[0])
	}
}

func TestJavascriptExpressRoutes(t *testing.T) {
	src := `const express = require('express')
const app = express()

app.get('/users', listUsers)
app.post('/users', function (req, res) { return res.json({}) })

function listUsers(req, res) { return req }
`
	syms := entryIn(t, "javascript", "app.js", src)
	lu := findSym(syms, "listUsers")
	if lu == nil || !lu.Entry || lu.Framework != "js-router" || lu.Route != "/users" {
		t.Fatalf("expected listUsers js-router entry, got %+v", lu)
	}
	// anonymous handler still yields a route marker
	var anon bool
	for _, s := range syms {
		if s.Kind == "entry" && s.Route == "/users" && s.Framework == "js-router" {
			anon = true
		}
	}
	if !anon {
		t.Fatal("expected an entry marker for the anonymous POST /users handler")
	}
}

func TestNestJSController(t *testing.T) {
	src := `import { Controller, Get } from '@nestjs/common';

@Controller('users')
export class UsersController {
  @Get()
  findAll() { return []; }

  @Get(':id')
  findOne(@Param('id') id: string) { return id; }
}
`
	syms := entryIn(t, "typescript", "users.controller.ts", src)
	findAll := findSym(syms, "UsersController.findAll")
	if findAll == nil || !findAll.Entry || findAll.Framework != "nestjs" || findAll.Route != "/users" {
		t.Fatalf("expected findAll nestjs entry /users, got %+v", findAll)
	}
	findOne := findSym(syms, "UsersController.findOne")
	if findOne == nil || !findOne.Entry || findOne.Route != "/users/:id" {
		t.Fatalf("expected findOne nestjs entry /users/:id, got %+v", findOne)
	}
}

func TestRubyRailsRoutes(t *testing.T) {
	src := `Rails.application.routes.draw do
  root "home#index"
  get "/users", to: "users#index"
  resources :posts
end
`
	syms := entryIn(t, "ruby", "config/routes.rb", src)
	es := entriesOf(syms)
	if len(es) < 3 {
		t.Fatalf("expected >=3 rails entries, got %d: %+v", len(es), es)
	}
	routes := map[string]bool{}
	for _, e := range es {
		routes[e.Route] = true
	}
	if !routes["/users"] || !routes["/"] || !routes["/posts"] {
		t.Fatalf("missing rails routes, got %v", routes)
	}
}

func TestPHPLaravelRoutes(t *testing.T) {
	src := `<?php
use Illuminate\Support\Facades\Route;

Route::get('/users', [UserController::class, 'index']);
Route::post('/users', 'UserController@store');
`
	syms := entryIn(t, "php", "routes/web.php", src)
	es := entriesOf(syms)
	if len(es) < 2 {
		t.Fatalf("expected 2 laravel entries, got %d", len(es))
	}
	if es[0].Framework != "laravel" || es[0].Route != "/users" {
		t.Fatalf("unexpected laravel entry: %+v", es[0])
	}
}

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

func TestJoinRoute(t *testing.T) {
	cases := []struct{ base, route, want string }{
		{"", "/users", "/users"},
		{"/api", "", "/api"},
		{"/api", "/users", "/api/users"},
		{"api/", "/users/", "/api/users"},
		{"", "", ""},
		{"", "/", "/"},
		{"/", "", "/"},
		{"/api", "/", "/api"},
	}
	for _, c := range cases {
		if got := joinRoute(c.base, c.route); got != c.want {
			t.Errorf("joinRoute(%q,%q) = %q, want %q", c.base, c.route, got, c.want)
		}
	}
}

func TestCleanHandler(t *testing.T) {
	cases := []struct{ in, want string }{
		{"users#index", "index"},
		{"UserController@store", "store"},
		{"[UserController::class, 'show']", "show"},
		{"views.detail", "detail"},
		{"[UserController::class, ProfileController::class]", "UserController"},
	}
	for _, c := range cases {
		if got := cleanHandler(c.in); got != c.want {
			t.Errorf("cleanHandler(%q) = %q, want %q", c.in, got, c.want)
		}
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
