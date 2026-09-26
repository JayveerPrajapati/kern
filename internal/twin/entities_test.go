package twin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// entitiesFixture builds a temp repo whose twin extractors emit one of every
// entity kind: an API endpoint (gin route on GetUsers), a topic (Kafka
// Topic: in the same file), a service (docker-compose), and a deployment
// (.kern/runtime.json). Returns the root and the merged graph.
func entitiesFixture(t *testing.T) (string, *index.Index) {
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
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, ix
}

// TestEntitiesForSymbol locks the symbol-mode entity render: the API endpoint
// and topic connected to GetUsers via twin edges, with deterministic
// direction (entity->code for the api implements edge, code->entity for the
// file->topic publishes edge).
func TestEntitiesForSymbol(t *testing.T) {
	root, ix := entitiesFixture(t)
	g := MergeIntoIndex(ix, root)
	ents, err := Entities(g, "GetUsers")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) == 0 {
		t.Fatal("no entities connected to GetUsers")
	}
	var api, topic bool
	for _, e := range ents {
		switch e.Kind {
		case "api":
			api = true
			if e.Direction != "entity->code" {
				t.Errorf("api direction = %q, want entity->code", e.Direction)
			}
			if e.Edge != "implements" {
				t.Errorf("api edge = %q, want implements", e.Edge)
			}
			if !strings.Contains(e.Name, "/v1/users") {
				t.Errorf("api name = %q, want the /v1/users route", e.Name)
			}
		case "topic":
			topic = true
			if e.Direction != "code->entity" {
				t.Errorf("topic direction = %q, want code->entity", e.Direction)
			}
		}
	}
	if !api {
		t.Error("api entity missing from GetUsers entity list")
	}
	if !topic {
		t.Error("topic entity missing from GetUsers entity list")
	}
}

// TestEntitiesUnknownSymbol locks the unresolvable-symbol error so the CLI
// and MCP surfaces both report the standard no-symbol contract.
func TestEntitiesUnknownSymbol(t *testing.T) {
	root, ix := entitiesFixture(t)
	g := MergeIntoIndex(ix, root)
	if _, err := Entities(g, "DoesNotExist"); err == nil {
		t.Fatal("expected error for unresolvable symbol")
	}
}

// TestEntitiesInventory locks the no-symbol inventory: every entity node,
// grouped by kind, with the deployment's version/commit attributes present.
func TestEntitiesInventory(t *testing.T) {
	root, ix := entitiesFixture(t)
	g := MergeIntoIndex(ix, root)
	ents, err := Entities(g, "")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	var depVersion, depCommit string
	for _, e := range ents {
		kinds[e.Kind]++
		if e.Kind == "deployment" {
			depVersion = e.Version
			depCommit = e.CommitSHA
		}
	}
	for _, want := range []string{"api", "topic", "service", "deployment"} {
		if kinds[want] < 1 {
			t.Errorf("inventory missing kind %q (kinds: %v)", want, kinds)
		}
	}
	if depVersion != "v1.2.3" || depCommit != "abc1234" {
		t.Errorf("deployment inventory attrs = version=%q commit=%q, want v1.2.3/abc1234", depVersion, depCommit)
	}
}

// TestRenderEntities locks the deterministic text block shape for both
// symbol and inventory modes.
func TestRenderEntities(t *testing.T) {
	root, ix := entitiesFixture(t)
	g := MergeIntoIndex(ix, root)
	ents, err := Entities(g, "GetUsers")
	if err != nil {
		t.Fatal(err)
	}
	text := RenderEntities(ents)
	if !strings.HasPrefix(text, "entities (") {
		t.Errorf("symbol render must start with the entities header, got %q", text)
	}
	if !strings.Contains(text, "entity->code") {
		t.Errorf("symbol render missing direction, got:\n%s", text)
	}
	inv, err := Entities(g, "")
	if err != nil {
		t.Fatal(err)
	}
	invText := RenderEntities(inv)
	if !strings.Contains(invText, "deployment (") {
		t.Errorf("inventory render missing deployment kind group, got:\n%s", invText)
	}
	if !strings.Contains(invText, "(version=v1.2.3 commit=abc1234)") {
		t.Errorf("inventory render missing deployment attributes, got:\n%s", invText)
	}
}

// TestMergeIntoIndexLocked pins that the CLI/MCP graph path reuse helper
// produces the twin-merged graph (service + deployment nodes present).
func TestMergeIntoIndexLocked(t *testing.T) {
	root, ix := entitiesFixture(t)
	g := MergeIntoIndex(ix, root)
	kinds := map[string]bool{}
	for _, n := range g.Nodes {
		kinds[n.Kind] = true
	}
	for _, want := range []string{"api", "topic", "service", "deployment"} {
		if !kinds[want] {
			t.Errorf("merged graph missing kind %q", want)
		}
	}
}

// indexEntitiesFixture writes a temp repo whose entities exist ONLY in the
// index's framework entry-point metadata (mux.HandleFunc — not one of the
// extractor's route patterns), plus an in-process broker struct. The code
// lives in a named sub-package so the package-service linking applies. This
// is the shape that used to yield "entities (0)" for well-connected symbols
// on a real index.
func indexEntitiesFixture(t *testing.T) (string, *index.Index) {
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
	write("srv/main.go", `package main

import (
	"fmt"
	"net/http"
)

// ServeMux registers the server's endpoints.
func ServeMux() {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", handleHealth)
}

// handleHealth serves the health endpoint.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "ok")
}

// NewServer builds the service; it is not itself an entry point.
func NewServer() *Server { return &Server{} }

// Server hosts the endpoints.
type Server struct{}

// Bus is an in-process broker whose methods have no HTTP surface.
type Bus struct{}

// EnqueueDeadLetter routes a message to the dead-letter queue.
func (b *Bus) EnqueueDeadLetter() {}
`)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, ix
}

// TestEntitiesIndexEntry locks the index-driven api entity: a handler marked
// as an entry point by the index (but invisible to the regex extractors)
// resolves to its real endpoint.
func TestEntitiesIndexEntry(t *testing.T) {
	root, ix := indexEntitiesFixture(t)
	g := MergeIntoIndex(ix, root)
	ents, err := Entities(g, "handleHealth")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range ents {
		if e.Kind == "api" && strings.Contains(e.Name, "/v1/health") {
			found = true
			if e.Direction != "entity->code" {
				t.Errorf("api direction = %q, want entity->code", e.Direction)
			}
			if e.Edge != "implements" {
				t.Errorf("api edge = %q, want implements", e.Edge)
			}
		}
	}
	if !found {
		t.Errorf("handleHealth entity list missing the /v1/health api entity, got:\n%+v", ents)
	}
}

// TestEntitiesPackageService locks the package-service linking: a constructor
// (NewServer) and a broker method (Bus.EnqueueDeadLetter) that no twin edge
// touches still resolve to their package's service entity, so symbol queries
// never return an empty set for symbols inside a service.
func TestEntitiesPackageService(t *testing.T) {
	root, ix := indexEntitiesFixture(t)
	g := MergeIntoIndex(ix, root)
	for _, sym := range []string{"NewServer", "Bus.EnqueueDeadLetter"} {
		ents, err := Entities(g, sym)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range ents {
			if e.Kind == "service" && e.Edge == "hosts" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected a package service entity (hosts), got:\n%+v", sym, ents)
		}
	}
}

// TestEntitiesConstructorResolvesToServiceEndpoints locks the served-endpoint
// expansion: a constructor that is not itself an entry point resolves to the
// service entity of its package AND the endpoints that service serves.
func TestEntitiesConstructorResolvesToServiceEndpoints(t *testing.T) {
	root, ix := indexEntitiesFixture(t)
	g := MergeIntoIndex(ix, root)
	ents, err := Entities(g, "NewServer")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range ents {
		if e.Kind == "api" && strings.Contains(e.Name, "/v1/health") {
			found = true
		}
	}
	if !found {
		t.Errorf("NewServer must surface the endpoints of its package service, got:\n%+v", ents)
	}
}
