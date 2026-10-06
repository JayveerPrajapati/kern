package brief

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestDigestRecentSessionActivity pins the cross-lane reuse path: auto
// captures (the plugin's session memory) must surface in the buddy digest as
// bounded recent activity — while staying out of the lessons section — so a
// fresh session or subagent lane sees what recent sessions did instead of
// re-deriving it.
func TestDigestRecentSessionActivity(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := memory.AddAuto(root, "Edited /tmp/lane/a.go"); err != nil {
		t.Fatal(err)
	}
	if err := memory.AddAuto(root, "Command failed: panic: boom"); err != nil {
		t.Fatal(err)
	}
	// Explicit lessons are typed-store entries (P2-14: the digest's
	// "Project memory" section reads the typed store; the v1 store now
	// feeds only the auto-captures activity section).
	if _, err := memory.NewMemoryStore(root).Add(domain.Memory{
		Type:    domain.MemoryLesson,
		Content: "deliberate lesson",
		Source:  "human",
		Scope:   "project",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := BuildWithOptions(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "## Recent session activity") {
		t.Fatalf("activity section missing from digest:\n%s", out)
	}
	if !strings.Contains(out, "Edited /tmp/lane/a.go") || !strings.Contains(out, "Command failed: panic: boom") {
		t.Fatalf("auto captures missing from activity section:\n%s", out)
	}
	// The explicit lesson must stay in the lessons section, and auto
	// captures must never leak into it.
	lessons, _, ok := strings.Cut(out, "## Recent session activity")
	if !ok {
		t.Fatalf("cannot split digest at activity section")
	}
	if !strings.Contains(lessons, "deliberate lesson") {
		t.Fatalf("explicit lesson missing from lessons section:\n%s", lessons)
	}
	if strings.Contains(lessons, "Edited /tmp/lane/a.go") {
		t.Fatalf("auto capture leaked into lessons section:\n%s", lessons)
	}
}

// TestDigestRecentActivityCapped pins the bound: only the most recent
// recentActivityMax auto captures render, so a long-lived project's digest
// cannot be flooded by session noise.
func TestDigestRecentActivityCapped(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	for i := 0; i < 12; i++ {
		if err := memory.AddAuto(root, "Edited /tmp/lane/file"+string(rune('A'+i))+".go"); err != nil {
			t.Fatal(err)
		}
	}

	out, err := BuildWithOptions(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "fileA.go") || strings.Contains(out, "fileB.go") {
		t.Fatalf("oldest auto captures should be dropped by the cap:\n%s", out)
	}
	if !strings.Contains(out, "fileC.go") || !strings.Contains(out, "fileL.go") {
		t.Fatalf("newest auto captures missing:\n%s", out)
	}
}
