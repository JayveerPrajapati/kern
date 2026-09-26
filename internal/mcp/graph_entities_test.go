package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mcpGraphEntitiesProject writes a temp repo whose twin extractors emit an
// API entity (gin route on GetUsers) plus a service, so the entities=true
// block has real connections to assert on.
func mcpGraphEntitiesProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.22\n")
	write("main.go", `package main

import "fmt"

// GetUsers lists users.
func GetUsers() {
	fmt.Println("users")
}

// Router registers the API surface.
func Router() {
	r := gin.New()
	r.GET("/v1/users", GetUsers)
}
`)
	write("docker-compose.yml", "services:\n  api:\n    image: api:v1\n")
	return root
}

// TestGraphToolEntitiesTrue locks kern_graph entities=true (Feature Batch E):
// the graph text includes the entity-node block with the twin entities
// connected to the symbol.
func TestGraphToolEntitiesTrue(t *testing.T) {
	t.Parallel()
	root := mcpGraphEntitiesProject(t)
	out := mcpAssertOK(t, "kern_graph", map[string]any{
		"root":     root,
		"symbol":   "GetUsers",
		"entities": "true",
	})
	if !strings.Contains(out, "entities (") {
		t.Fatalf("entities=true must render the entity block, got:\n%s", out)
	}
	if !strings.Contains(out, "/v1/users") {
		t.Errorf("expected the /v1/users API entity, got:\n%s", out)
	}
	if !strings.Contains(out, "entity->code") {
		t.Errorf("expected the entity->code direction, got:\n%s", out)
	}
}

// TestGraphToolEntitiesAbsent locks byte-compat of the default surface: no
// entities arg → no entity block appended.
func TestGraphToolEntitiesAbsent(t *testing.T) {
	t.Parallel()
	root := mcpGraphEntitiesProject(t)
	out := mcpAssertOK(t, "kern_graph", map[string]any{
		"root":   root,
		"symbol": "GetUsers",
	})
	if strings.Contains(out, "entities (") {
		t.Fatalf("entities must default off, got entity block:\n%s", out)
	}
}

// TestGraphToolEntitiesUnknownSymbol locks the unknown-symbol contract when
// entities=true cannot resolve the symbol.
func TestGraphToolEntitiesUnknownSymbol(t *testing.T) {
	t.Parallel()
	root := mcpGraphEntitiesProject(t)
	resp := mcpCall(t, "kern_graph", map[string]any{
		"root":     root,
		"symbol":   "Nope",
		"entities": "true",
	})
	out, isErr := toolResultText(t, resp)
	if !isErr || !strings.Contains(out, "unknown symbol") {
		t.Fatalf("expected unknown-symbol isError, got: %+v", resp)
	}
}
