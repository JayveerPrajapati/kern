package index

import (
	"testing"
)

const tinyProject = `package demo

// Greeter greets.
type Greeter struct{ Name string }

// Hello greets name.
func (g Greeter) Hello() string { return "hello " + g.Name }

// Run calls Hello.
func Run(g Greeter) string { return g.Hello() }
`

// TestIndexStatusNotBuilt verifies StatusReport reports Built=false for a
// root with no cached index, without building anything (read-only contract).
func TestIndexStatusNotBuilt(t *testing.T) {
	root := t.TempDir()
	st, err := StatusReport(root, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Built {
		t.Errorf("expected Built=false for unbuilt root, got %+v", st)
	}
	if !st.Stale {
		t.Error("expected Stale=true for unbuilt root")
	}
}

// TestIndexBuildAndStatus verifies BuildPersisted produces a real index and
// StatusReport then reports it as built with the expected symbol/file counts.
func TestIndexBuildAndStatus(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "demo.go", tinyProject)

	ix, err := BuildPersisted(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if ix == nil {
		t.Fatal("Build returned nil index")
	}
	if len(ix.Symbols) < 3 {
		t.Errorf("expected >=3 symbols (Greeter/Hello/Run), got %d", len(ix.Symbols))
	}

	st, err := StatusReport(root, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Built {
		t.Fatalf("expected Built=true after Build, got %+v", st)
	}
	if st.Symbols != len(ix.Symbols) {
		t.Errorf("Status.Symbols=%d, want %d", st.Symbols, len(ix.Symbols))
	}
	if st.Files != len(ix.FileHashes) {
		t.Errorf("Status.Files=%d, want %d", st.Files, len(ix.FileHashes))
	}
	if st.Packages != len(ix.Pkgs) {
		t.Errorf("Status.Packages=%d, want %d", st.Packages, len(ix.Pkgs))
	}
	if st.Version == 0 {
		t.Error("Status.Version should be non-zero")
	}
	if st.Store == "" {
		t.Error("Status.Store should name the on-disk index")
	}
	if st.IndexIdentity == nil {
		t.Error("Status.IndexIdentity should be populated after Build")
	}
}
