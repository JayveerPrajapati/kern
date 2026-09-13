package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConstructorAssignedReceiverCrossPackage pins the merge-time rewrite
// for the standard main-wiring pattern: "h := api.NewHandlers(); h.Routes()"
// must resolve to Handlers.Routes even though the constructor is declared in
// a different package than the call. Regression for the live evaluation
// finding where graph/blast-radius/path went blind across main.go.
func TestConstructorAssignedReceiverCrossPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api", "handlers.go"), []byte(`package api

type Handlers struct{}

func NewHandlers() *Handlers {
	return &Handlers{}
}

func (h *Handlers) Routes() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import "example.com/app/api"

func main() {
	h := api.NewHandlers()
	h.Routes()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := ix.Callers["Handlers.Routes"]
	if len(got) != 1 || got[0] != "main" {
		t.Fatalf("canonical Callers[Handlers.Routes] = %v, want [main] (cross-package constructor-assigned receiver resolved at merge)", got)
	}
	// The intermediate qualified form must not leak into the canonical map.
	if got := ix.Callers["api.NewHandlers.Routes"]; len(got) != 0 {
		t.Fatalf("stale intermediate callee api.NewHandlers.Routes still has callers %v", got)
	}
}

// TestConstructorAssignedReceiverCrossPackageSQLite pins the same invariant
// through a full SQLite save+load round-trip: the persisted Constructors
// column must let the load-time rewrite reproduce the build-time Callers.
func TestConstructorAssignedReceiverCrossPackageSQLite(t *testing.T) {
	if !SQLiteEnabled() {
		t.Skip("no sqlite tag")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api", "handlers.go"), []byte(`package api

type Handlers struct{}

func NewHandlers() *Handlers {
	return &Handlers{}
}

func (h *Handlers) Routes() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import "example.com/app/api"

func main() {
	h := api.NewHandlers()
	h.Routes()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSQLite(dir, ix); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSQLite(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Callers["Handlers.Routes"]
	if len(got) != 1 || got[0] != "main" {
		t.Fatalf("sqlite-roundtrip Callers[Handlers.Routes] = %v, want [main]", got)
	}
}