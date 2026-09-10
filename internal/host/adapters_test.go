package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlockMarkersUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range NewRegistry().Adapters() {
		start, end := blockMarkers(a.Name())
		if seen[start] || seen[end] {
			t.Errorf("duplicate markers for %s: %q / %q", a.Name(), start, end)
		}
		seen[start] = true
		seen[end] = true
	}
	if s, e := blockMarkers("opencode"); s == e {
		t.Error("start and end markers must differ")
	}
}

func TestFileAdapterLifecycle(t *testing.T) {
	adapters := NewRegistry().Adapters()
	pkt := testPacket()
	for _, a := range adapters {
		t.Run(a.Name(), func(t *testing.T) {
			dir := t.TempDir()
			rel := a.FilePath("")
			original := "# before sentinel\n# after sentinel\n"
			if err := writeTestFile(dir, rel, original); err != nil {
				t.Fatalf("write sentinel file: %v", err)
			}
			if !a.Detect(dir) {
				t.Fatal("Detect should be true after writing the file")
			}

			// Inject: block is written, sentinels preserved.
			block, err := a.Inject(dir, pkt, 1200)
			if err != nil {
				t.Fatalf("Inject: %v", err)
			}
			if !strings.Contains(block, "kern context") {
				t.Errorf("block missing header: %q", block)
			}
			got, err := a.Extract(dir)
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if got != strings.TrimSpace(block) {
				t.Errorf("Extract = %q, want block %q", got, block)
			}
			full, _ := os.ReadFile(a.FilePath(dir))
			if !strings.Contains(string(full), "# before sentinel") || !strings.Contains(string(full), "# after sentinel") {
				t.Error("sentinels not preserved after Inject")
			}

			// Inject twice → exactly one block (replaced, no duplication).
			block2, err := a.Inject(dir, pkt, 1200)
			if err != nil {
				t.Fatalf("second Inject: %v", err)
			}
			full, _ = os.ReadFile(a.FilePath(dir))
			start, _ := blockMarkers(a.Name())
			if c := strings.Count(string(full), start); c != 1 {
				t.Errorf("start marker count after double inject = %d, want 1", c)
			}
			if got, _ := a.Extract(dir); got != strings.TrimSpace(block2) {
				t.Error("Extract after double inject should be the new block only")
			}

			// Uninstall: block gone, sentinel content intact, idempotent.
			if err := a.Uninstall(dir); err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			got, _ = a.Extract(dir)
			if got != "" {
				t.Errorf("Extract after Uninstall = %q, want empty", got)
			}
			full, _ = os.ReadFile(a.FilePath(dir))
			if string(full) != original {
				t.Errorf("file after Uninstall = %q, want original %q", string(full), original)
			}
			if err := a.Uninstall(dir); err != nil {
				t.Errorf("second Uninstall should be nil, got %v", err)
			}
		})
	}
}

func TestFileAdapterDetectMissingFile(t *testing.T) {
	for _, a := range NewRegistry().Adapters() {
		if a.Detect(t.TempDir()) {
			t.Errorf("%s Detect = true on empty dir", a.Name())
		}
	}
}

// TestFileAdapterMissingFileCreated verifies Inject creates a missing file
// (including parent directories for the cursor rules path).
func TestFileAdapterMissingFileCreated(t *testing.T) {
	a := NewCursorAdapter()
	dir := t.TempDir()
	if _, err := a.Inject(dir, testPacket(), 1200); err != nil {
		t.Fatalf("Inject into missing file: %v", err)
	}
	if !a.Detect(dir) {
		t.Error("Detect should be true after Inject created the file")
	}
	full, err := os.ReadFile(a.FilePath(dir))
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if !strings.Contains(string(full), "kern-host:cursor:start") {
		t.Errorf("created file missing cursor block: %q", string(full))
	}
}

// TestCoexistOnSameFile verifies opencode and codex (both AGENTS.md) can be
// injected side by side and each Extract returns only its own block.
func TestCoexistOnSameFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeTestFile(dir, "AGENTS.md", "# project rules\n"); err != nil {
		t.Fatal(err)
	}
	oc := NewOpenCodeAdapter()
	cx := NewCodexAdapter()
	pkt := testPacket()

	if _, err := oc.Inject(dir, pkt, 1200); err != nil {
		t.Fatal(err)
	}
	if _, err := cx.Inject(dir, pkt, 1200); err != nil {
		t.Fatal(err)
	}
	full, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	text := string(full)
	if !strings.Contains(text, "kern-host:opencode:start") || !strings.Contains(text, "kern-host:codex:start") {
		t.Fatal("both markers must be present on the shared file")
	}
	ocBlock, _ := oc.Extract(dir)
	cxBlock, _ := cx.Extract(dir)
	if ocBlock == "" || cxBlock == "" {
		t.Fatal("both extracts should be non-empty")
	}
	if strings.Contains(ocBlock, "kern-host:codex") {
		t.Error("opencode Extract leaks codex's marker")
	}
	if strings.Contains(cxBlock, "kern-host:opencode") {
		t.Error("codex Extract leaks opencode's marker")
	}
	// Uninstall codex only: opencode's block survives.
	if err := cx.Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	full, _ = os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(full), "kern-host:opencode:start") {
		t.Error("opencode block removed when codex was uninstalled")
	}
	if strings.Contains(string(full), "kern-host:codex:start") {
		t.Error("codex block still present after Uninstall")
	}
}
