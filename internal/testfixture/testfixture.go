// Package testfixture provides a small, deterministic multi-package Go
// repository used as the analysis subject by end-to-end tests (app, mcp,
// cicd, loop). Using the whole kern repo as a test subject makes every
// Analyze/Plan/WhatIf/Impact call cost seconds (index build + context engine
// over ~5000 symbols); a five-file fixture exercises the identical code paths
// with millisecond index builds, so the same assertions run 10-50x faster.
//
// The fixture is generated into a fresh temp git repo per call, so tests
package testfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// ModulePath is the fixture module path. Intentionally short so analysis output stays readable.
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

var (
	templateOnce sync.Once
	templateDir  string
	templateErr  error
)

func initTemplate() {
	dir, err := os.MkdirTemp("", "kern-testfixture-template-*")
	if err != nil {
		templateErr = err
		return
	}
	templateDir = dir
	for rel, content := range Files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			templateErr = err
			return
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			templateErr = err
			return
		}
	}
	// Pre-ignore tool-state directories so status and fast-paths stay clean
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("/.kern\n/.blueprint\n"), 0o644)
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		templateErr = fmt.Errorf("git init: %v (%s)", err, out)
		return
	}
	_ = exec.Command("git", "-C", dir, "config", "user.name", "testfixture").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.email", "testfixture@example.com").Run()
	_ = exec.Command("git", "-C", dir, "add", "-A").Run()
	if out, err := exec.Command("git", "-C", dir, "commit", "-qm", "fixture init").CombinedOutput(); err != nil {
		templateErr = fmt.Errorf("git commit: %v (%s)", err, out)
		return
	}
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// Repo creates a fresh temp git repository containing the fixture source,
// commits it, and returns the repo path. The repo is registered with t for
// cleanup. Every call returns an independent repo, so concurrent tests never
// share state; the fixture is small, so the per-call cost is milliseconds.
func Repo(t *testing.T) string {
	t.Helper()
	templateOnce.Do(initTemplate)
	if templateErr != nil {
		t.Fatalf("testfixture: template init: %v", templateErr)
	}
	dir := t.TempDir()
	if err := copyDir(templateDir, dir); err != nil {
		t.Fatalf("testfixture: copy template: %v", err)
	}
	return dir
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
