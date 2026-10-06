package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// graphEntitiesFixture writes a temp repo with one of every twin entity
// kind: an API endpoint (gin route on GetUsers), a topic (Kafka Topic: in
// the same file), a service (docker-compose), and a deployment
// (.kern/runtime.json). Shared by the --entities CLI tests.
func graphEntitiesFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
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
	msg := struct{ Topic string }{Topic: "user.created"}
	producer.SendMessage(msg)
}
`)
	write("docker-compose.yml", "services:\n  api:\n    image: api:v1\n")
	write(".kern/runtime.json", `{"events": [], "deployments": [{"Service":"api","Version":"v1.2.3","CommitSHA":"abc1234","DeployedAt":"2026-09-22T12:00:00Z"}], "commits": []}`)
	return dir
}

// TestGraphEntitiesSymbol locks `kern graph --entities <symbol>`: the entity
// block renders the twin entity nodes connected to the symbol with a
// deterministic direction column.
func TestGraphEntitiesSymbol(t *testing.T) {
	dir := graphEntitiesFixture(t)
	out := captureStdout(t, func() {
		if code := dispatchCommand("graph", []string{"--entities", "GetUsers", dir}); code != 0 {
			t.Fatalf("graph --entities exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "entities (") {
		t.Fatalf("expected the entities header, got:\n%s", out)
	}
	if !strings.Contains(out, "/v1/users") {
		t.Errorf("expected the /v1/users API entity, got:\n%s", out)
	}
	if !strings.Contains(out, "entity->code") {
		t.Errorf("expected the entity->code direction, got:\n%s", out)
	}
}

// TestGraphEntitiesJSONSymbol locks `kern graph --entities --json <symbol>`:
// the additive {"entities": [...]} JSON shape with per-entity direction.
func TestGraphEntitiesJSONSymbol(t *testing.T) {
	dir := graphEntitiesFixture(t)
	out := captureStdout(t, func() {
		if code := dispatchCommand("graph", []string{"--entities", "--json", "GetUsers", dir}); code != 0 {
			t.Fatalf("graph --entities --json exit = %d, want 0", code)
		}
	})
	var payload struct {
		Entities []map[string]any `json:"entities"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(payload.Entities) == 0 {
		t.Fatal("entities array is empty")
	}
	found := false
	for _, e := range payload.Entities {
		if e["kind"] == "api" && e["direction"] == "entity->code" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an api entity with entity->code direction, got: %s", out)
	}
}

// TestGraphEntitiesInventory locks the no-symbol inventory: `kern graph
// --entities` renders all entity nodes grouped by kind, including the
// deployment's version/commit attributes.
func TestGraphEntitiesInventory(t *testing.T) {
	dir := graphEntitiesFixture(t)
	out := captureStdout(t, func() {
		if code := dispatchCommand("graph", []string{"--entities", "--root", dir}); code != 0 {
			t.Fatalf("graph --entities (no symbol) exit = %d, want 0", code)
		}
	})
	for _, want := range []string{"api (", "deployment (", "service (", "topic (", "(version=v1.2.3 commit=abc1234)"} {
		if !strings.Contains(out, want) {
			t.Errorf("inventory missing %q, got:\n%s", want, out)
		}
	}
}

// TestGraphEntitiesJSONInventory locks the JSON inventory shape.
func TestGraphEntitiesJSONInventory(t *testing.T) {
	dir := graphEntitiesFixture(t)
	out := captureStdout(t, func() {
		if code := dispatchCommand("graph", []string{"--entities", "--json", "--root", dir}); code != 0 {
			t.Fatalf("graph --entities --json (no symbol) exit = %d, want 0", code)
		}
	})
	var payload struct {
		Entities []map[string]any `json:"entities"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	kinds := map[string]bool{}
	for _, e := range payload.Entities {
		kinds[e["kind"].(string)] = true
	}
	for _, want := range []string{"api", "deployment", "service", "topic"} {
		if !kinds[want] {
			t.Errorf("inventory JSON missing kind %q, got: %s", want, out)
		}
	}
}

// TestGraphDefaultUnchanged locks that plain `kern graph <symbol>` output is
// untouched by the --entities feature (default branch still the code graph).
func TestGraphDefaultUnchanged(t *testing.T) {
	dir := graphEntitiesFixture(t)
	out := captureStdout(t, func() {
		if code := dispatchCommand("graph", []string{"GetUsers", dir}); code != 0 {
			t.Fatalf("graph exit = %d, want 0", code)
		}
	})
	if strings.Contains(out, "entities (") {
		t.Errorf("default graph output must not include the entity block:\n%s", out)
	}
	if !strings.Contains(out, "GetUsers") {
		t.Errorf("default graph output should mention the symbol:\n%s", out)
	}
}

// TestGraphDeadAlias locks `kern graph dead <dir>`: the first positional
// "dead" is the dead-code report subcommand (top-level `kern dead`), not a
// symbol to resolve — so it must render dead-code output and exit 0 instead
// of failing symbol lookup with "no symbol found: dead".
func TestGraphDeadAlias(t *testing.T) {
	dir := graphEntitiesFixture(t)
	out := captureStdout(t, func() {
		if code := dispatchCommand("graph", []string{"dead", dir}); code != 0 {
			t.Fatalf("graph dead exit = %d, want 0", code)
		}
	})
	if strings.Contains(out, "no symbol found") {
		t.Fatalf("graph dead must not attempt a symbol lookup:\n%s", out)
	}
	if !strings.Contains(out, "dead code (no production callers):") {
		t.Fatalf("expected dead-code report output, got:\n%s", out)
	}
}

// TestGraphDeadAliasSymbolLookupUnchanged locks that the alias is exact and
// lowercase-only: a capital "Dead" still resolves through the graph symbol
// path exactly as before (failed lookup, exit 1, did-you-mean), and a real
// symbol still renders the code graph (not the dead-code report).
func TestGraphDeadAliasSymbolLookupUnchanged(t *testing.T) {
	dir := graphEntitiesFixture(t)
	recovered := catchExit(t, func() {
		dispatchCommand("graph", []string{"Dead", dir})
	})
	if ee, ok := recovered.(exitError); !ok || ee.code == 0 {
		t.Fatalf("graph Dead (capital) must stay a symbol lookup — no fixture symbol 'Dead' exists, so exit must be non-zero, got %v", recovered)
	}
	out := captureStdout(t, func() {
		if code := dispatchCommand("graph", []string{"GetUsers", dir}); code != 0 {
			t.Fatalf("graph GetUsers exit = %d, want 0", code)
		}
	})
	if strings.Contains(out, "dead code (no production callers):") {
		t.Errorf("graph <realsymbol> must render the code graph, not the dead-code report:\n%s", out)
	}
	if !strings.Contains(out, "GetUsers") {
		t.Errorf("graph <realsymbol> output should mention the symbol:\n%s", out)
	}
}
