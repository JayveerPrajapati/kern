// Package testfixture provides a small, deterministic multi-package Go
// repository used as the analysis subject by end-to-end tests (app, mcp,
// cicd, loop). Using the whole kern repo as a test subject makes every
// Analyze/Plan/WhatIf/Impact call cost seconds (index build + context engine
// over ~5000 symbols); a five-file fixture exercises the identical code paths
// with millisecond index builds, so the same assertions run 10-50x faster.
//
// The fixture is generated into a fresh temp git repo per call, so tests
// never depend on shared mutable state and never pollute the real repo.
package testfixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Fixture module path. Intentionally short so analysis output stays readable.
const ModulePath = "example.com/fixture"

// Files is the fixture source tree: a tiny Go module with two packages
// (web, db) plus a main package, exposing the symbols e2e tests reference
// (NewServer, UserService, NewUserService, Server, DB, NewDB, Query).
var Files = map[string]string{
	"go.mod": "module " + ModulePath + "\n\ngo 1.23\n",
	"main.go": `package main

import "example.com/fixture/web"

func main() {
	s := web.NewServer()
	_ = s
}
`,
	"db/db.go": `// Package db is the persistence layer of the fixture.
package db

// DB is the fixture database handle.
type DB struct{}

// NewDB constructs a DB.
func NewDB() *DB { return &DB{} }

// Query runs a query and returns a result string.
func (d *DB) Query(q string) (string, error) { return "result:" + q, nil }
`,
	"web/server.go": `// Package web is the HTTP layer of the fixture.
package web

import "example.com/fixture/db"

// UserService serves user requests; it depends on the db package so the
// fixture has a real cross-package call edge (db.DB is called from web).
type UserService struct {
	db *db.DB
}

// NewUserService constructs a UserService.
func NewUserService() *UserService { return &UserService{db: db.NewDB()} }

// Get returns a user record for id.
func (u *UserService) Get(id string) string {
	res, _ := u.db.Query("user:" + id)
	return res
}
`,
	"web/handler.go": `package web

// Server is the fixture HTTP server.
type Server struct {
	svc *UserService
}

// NewServer constructs a Server.
func NewServer() *Server { return &Server{svc: NewUserService()} }

// Handle serves a request.
func (s *Server) Handle(id string) string { return s.svc.Get(id) }
`,
}

// Repo creates a fresh temp git repository containing the fixture source,
// commits it, and returns the repo path. The repo is registered with t for
// cleanup. Every call returns an independent repo, so concurrent tests never
// share state; the fixture is small, so the per-call cost is milliseconds.
func Repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range Files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("testfixture: mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("testfixture: write %s: %v", rel, err)
		}
	}
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.name", "testfixture")
	git(t, dir, "config", "user.email", "testfixture@example.com")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "fixture init")
	return dir
}

// git runs a git command in dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("testfixture: git %v: %v\n%s", args, err, out)
	}
}

// Symbol returns a fixture symbol of the given kind, so tests can reference
// a stable target without hard-coding the fixture contents twice. kind is
// one of "server", "user", "db". Returns "" for unknown kinds.
func Symbol(kind string) string {
	switch kind {
	case "server":
		return "NewServer"
	case "user":
		return "NewUserService"
	case "db":
		return "NewDB"
	default:
		return ""
	}
}

// Path returns the fixture root for tests that only need the path and want
// to assert on it in messages.
func Path(t *testing.T) string {
	return Repo(t)
}

// Module returns the fixture module path (for assertions that reference it).
func Module() string { return ModulePath }
