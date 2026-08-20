package twin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestMergeAddsTwinNodes(t *testing.T) {
	// Create a temp dir with a fixture (a .go file + a docker-compose.yml)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services:\n  api:\n    image: api:v1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Build graph from index, merge twin extractors
	g, err := MergeIntoGraph(dir, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should have symbol nodes (from code) + service node (from docker-compose)
	hasService := false
	for _, n := range g.Nodes {
		if n.Kind == "service" {
			hasService = true
		}
	}
	if !hasService {
		t.Error("no service nodes after merge")
	}
}

func TestDedupNodes(t *testing.T) {
	nodes := []domain.Node{
		{ID: "a", Kind: "service"},
		{ID: "b", Kind: "service"},
		{ID: "a", Kind: "service"}, // duplicate
	}
	result := dedupNodes(nodes)
	if len(result) != 2 {
		t.Errorf("dedup = %d, want 2", len(result))
	}
}

func TestTeamType(t *testing.T) {
	team := domain.Team{ID: "@backend", Name: "Backend", Members: []string{"alice", "bob"}}
	if team.Name != "Backend" || len(team.Members) != 2 {
		t.Error("Team fields not set correctly")
	}
}
