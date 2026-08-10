package intel

import (
	"strings"
	"testing"
)

func TestCoChangePairs(t *testing.T) {
	root := newGitRepo(t)
	report, err := CoChange(root, "", "")
	if err != nil {
		t.Fatalf("CoChange: %v", err)
	}
	if report.Commits < 1 {
		t.Errorf("expected at least 1 commit, got %d", report.Commits)
	}
	// Commit 2 changed a.go and b.go together, so they must be mutual partners.
	var a, b *CoChangeEntry
	for i := range report.Entries {
		switch report.Entries[i].File {
		case "a.go":
			a = &report.Entries[i]
		case "b.go":
			b = &report.Entries[i]
		}
	}
	if a == nil || b == nil {
		t.Fatalf("expected a.go and b.go entries, got %v", report.Entries)
	}
	if a.PartnerCounts["b.go"] < 1 {
		t.Errorf("a.go should co-change with b.go, got %v", a.PartnerCounts)
	}
	if b.PartnerCounts["a.go"] < 1 {
		t.Errorf("b.go should co-change with a.go, got %v", b.PartnerCounts)
	}
}

func TestCoChangeNoGit(t *testing.T) {
	report, err := CoChange(t.TempDir(), "", "")
	if err == nil {
		t.Fatal("expected error for non-git dir")
	}
	if report != nil {
		t.Errorf("expected nil report on error, got %+v", report)
	}
}

func TestParseCommitBlocks(t *testing.T) {
	out := "a.go\nb.go\n\na.go\n\nc.go\n"
	blocks := parseCommitBlocks(out)
	if len(blocks) != 3 {
		t.Fatalf("parseCommitBlocks = %v; want 3 blocks", blocks)
	}
	if len(blocks[0]) != 2 || len(blocks[1]) != 1 || len(blocks[2]) != 1 {
		t.Errorf("block sizes wrong: %v", blocks)
	}
	if blocks[0][0] != "a.go" || blocks[0][1] != "b.go" {
		t.Errorf("first block = %v; want [a.go b.go]", blocks[0])
	}
}

func TestRenderCoChange(t *testing.T) {
	report := &CoChangeReport{
		From:    "HEAD~2",
		To:      "HEAD",
		Commits: 3,
		Files:   2,
		Entries: []CoChangeEntry{
			{
				File:          "a.go",
				Commits:       2,
				Partners:      []string{"b.go"},
				PartnerCounts: map[string]int{"b.go": 2},
			},
			{File: "b.go", Commits: 1},
		},
		WorkingTree: map[string]bool{"a.go": true},
	}
	out := RenderCoChange(report, 0)
	for _, want := range []string{"HEAD~2..HEAD", "3 commits", "a.go", "b.go", "2×", "[modified NOW]"} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderCoChange missing %q in:\n%s", want, out)
		}
	}

	limited := RenderCoChange(report, 1)
	if strings.Contains(limited, "b.go") && strings.Count(limited, "a.go") == 1 {
		// Limit 1 shows only the top entry a.go (partners line for b.go may
		// appear under it); b.go as a top-level entry must not appear twice.
		if strings.Count(limited, "with") > 1 {
			t.Errorf("limit not honored:\n%s", limited)
		}
	}
	if !strings.Contains(RenderCoChange(&CoChangeReport{Commits: 1, Files: 0}, 0), "over 1 commit") {
		t.Error("empty-range header missing")
	}
}
