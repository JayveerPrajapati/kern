package ownership

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAndLookup(t *testing.T) {
	dir := t.TempDir()
	codeowners := `# comment
*       @everyone
*.go    @backend-team
src/    @backend-team
src/auth/ @security-team
src/auth/login.go @security-team @backend-team
`
	path := filepath.Join(dir, "CODEOWNERS")
	if err := os.WriteFile(path, []byte(codeowners), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := Parse(path, dir)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path   string
		owners []string
	}{
		{"README.md", []string{"@everyone"}},
		{"main.go", []string{"@backend-team"}},
		{"src/main.go", []string{"@backend-team"}}, // src/ matches, *.go also matches but src/ is checked — most specific wins
		{"src/auth/login.go", []string{"@security-team", "@backend-team"}},
		{"src/auth/signup.go", []string{"@security-team"}}, // src/auth/ matches
	}
	for _, c := range cases {
		got := m.Lookup(c.path)
		if !equalStrings(got, c.owners) {
			t.Errorf("Lookup(%q) = %v, want %v", c.path, got, c.owners)
		}
	}
}

func TestParseMissingFile(t *testing.T) {
	m, err := Parse("/nonexistent/CODEOWNERS", "/nonexistent")
	if err != nil {
		t.Errorf("expected nil error for missing file, got %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil Map")
	}
	if owners := m.Lookup("anything.go"); owners != nil {
		t.Errorf("Lookup on empty map = %v, want nil", owners)
	}
}

func TestParseFromRepo(t *testing.T) {
	dir := t.TempDir()
	ghDir := filepath.Join(dir, ".github")
	if err := os.MkdirAll(ghDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(ghDir, "CODEOWNERS"), []byte("* @team-a\n"), 0644)

	m, err := ParseFromRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Lookup("foo.go"); len(got) != 1 || got[0] != "@team-a" {
		t.Errorf("Lookup = %v, want [@team-a]", got)
	}
}

func TestTeams(t *testing.T) {
	m := &Map{rules: []Rule{
		{Pattern: "*", Owners: []string{"@everyone"}},
		{Pattern: "src/", Owners: []string{"@backend", "@platform"}},
	}}
	teams := m.Teams()
	want := []string{"@backend", "@everyone", "@platform"}
	if !equalStrings(teams, want) {
		t.Errorf("Teams() = %v, want %v", teams, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
