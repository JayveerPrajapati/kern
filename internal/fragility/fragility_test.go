package fragility

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestScoreAndRisk(t *testing.T) {
	// Low
	s1 := calculateScore(1, 1, 0)
	if s1 < 3.0 || scoreToRisk(s1) != "LOW" && scoreToRisk(s1) != "MEDIUM" {
		t.Errorf("s1 = %f, risk = %s", s1, scoreToRisk(s1))
	}

	// High
	s2 := calculateScore(3, 5, 8)
	if s2 < 10.0 || scoreToRisk(s2) != "HIGH" && scoreToRisk(s2) != "CRITICAL" {
		t.Errorf("s2 = %f, risk = %s", s2, scoreToRisk(s2))
	}

	// Critical
	s3 := calculateScore(10, 20, 50)
	if scoreToRisk(s3) != "CRITICAL" {
		t.Errorf("s3 = %f, want CRITICAL", s3)
	}
}

func TestDefectKeywordsMatching(t *testing.T) {
	tests := []struct {
		subject string
		want    bool
	}{
		{"fix(router): resolve crash on null token", true},
		{"bugfix: prevent memory leak in worker pool", true},
		{"hotfix for production race condition", true},
		{"chore: update dependencies", false},
		{"feat: add new user profile endpoint", false},
		{"refactor: clean up unused variables", false},
		{"fix flaky test in storage package", true},
	}

	for _, tc := range tests {
		got := defectKeywords.MatchString(tc.subject)
		if got != tc.want {
			t.Errorf("defectKeywords.MatchString(%q) = %v, want %v", tc.subject, got, tc.want)
		}
	}
}

func TestAnalyzeGitFixture(t *testing.T) {
	dir := t.TempDir()

	// Initialize git repo
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	run("init")
	run("config", "user.name", "tester")
	run("config", "user.email", "test@example.com")

	// Commit 1: Initial
	f1 := filepath.Join(dir, "server.go")
	_ = os.WriteFile(f1, []byte("package main\nfunc Run() {}\n"), 0644)
	run("add", ".")
	run("commit", "-m", "feat: initial server implementation")

	// Commit 2: Bug fix on server.go
	_ = os.WriteFile(f1, []byte("package main\nfunc Run() { /* fix */ }\n"), 0644)
	run("add", ".")
	run("commit", "-m", "fix: resolve panic on shutdown in server.go")

	// Commit 3: Another bug fix on server.go
	_ = os.WriteFile(f1, []byte("package main\nfunc Run() { /* fix 2 */ }\n"), 0644)
	run("add", ".")
	run("commit", "-m", "bug: handle race condition in server.go")

	report, err := Analyze(context.Background(), Options{
		Root:     dir,
		MinFixes: 1,
	})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if report.DefectCommitsCount != 2 {
		t.Errorf("defect commits = %d, want 2", report.DefectCommitsCount)
	}

	if len(report.Hotspots) == 0 {
		t.Fatalf("expected at least 1 hotspot on server.go")
	}

	h := report.Hotspots[0]
	if h.File != "server.go" || h.DefectCommits != 2 {
		t.Errorf("hotspot = %+v", h)
	}
	if len(h.RecentFixes) != 2 {
		t.Errorf("recent fixes = %+v", h.RecentFixes)
	}
}
