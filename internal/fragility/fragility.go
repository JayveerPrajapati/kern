// Package fragility correlates git defect/fix commit history with the AST symbol
// call graph to compute causal fragility hotspot ratings. It warns agents before
// mutating high-risk, regression-prone components.
package fragility

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// DefectKeywords are patterns matching bug fixes and regression patches in commit subjects.
var defectKeywords = regexp.MustCompile(`(?i)\b(fix|fixed|fixes|bug|bugs|patch|resolve|resolved|resolves|crash|crashes|regression|panic|leak|defect|hotfix|vuln|vulnerability|flaky|race)\b`)

// Hotspot represents a historically fragile file or symbol.
type Hotspot struct {
	Target         string   `json:"target"`
	Kind           string   `json:"kind"` // "file" or "symbol"
	File           string   `json:"file"`
	DefectCommits  int      `json:"defect_commits"`
	TotalCommits   int      `json:"total_commits"`
	CallerCount    int      `json:"caller_count"`
	FragilityScore float64  `json:"fragility_score"`
	RiskLevel      string   `json:"risk_level"` // "CRITICAL", "HIGH", "MEDIUM", "LOW"
	RecentFixes    []string `json:"recent_fixes,omitempty"`
	TopDependents  []string `json:"top_dependents,omitempty"`
}

// Options configures fragility analysis.
type Options struct {
	Root     string `json:"root"`
	Target   string `json:"target,omitempty"` // optional specific file or symbol filter
	Limit    int    `json:"limit,omitempty"`
	Commits  int    `json:"commits,omitempty"`
	MinFixes int    `json:"min_fixes,omitempty"`
}

// Report encapsulates the complete fragility analysis.
type Report struct {
	Root               string    `json:"root"`
	EvaluatedCommits   int       `json:"evaluated_commits"`
	DefectCommitsCount int       `json:"defect_commits_count"`
	Hotspots           []Hotspot `json:"hotspots"`
}

type commitRecord struct {
	Hash    string
	Subject string
	IsFix   bool
	Files   []string
}

// Analyze scans git history and symbol graph to detect regression-prone fragility hotspots.
func Analyze(ctx context.Context, opts Options) (*Report, error) {
	if opts.Root == "" {
		opts.Root = "."
	}
	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		absRoot = opts.Root
	}
	if opts.Limit <= 0 {
		opts.Limit = 15
	}
	if opts.Commits <= 0 {
		opts.Commits = 100
	}
	if opts.MinFixes <= 0 {
		opts.MinFixes = 1
	}

	records, err := readGitCommitLog(ctx, absRoot, opts.Commits)
	if err != nil {
		// If git history cannot be read (e.g. not a git repo), degrade gracefully
		return &Report{
			Root:     absRoot,
			Hotspots: nil,
		}, nil
	}

	ix, _ := index.LoadOrBuild(absRoot)

	// Aggregate per-file statistics
	fileFixes := make(map[string]int)
	fileTotal := make(map[string]int)
	fileRecentFixes := make(map[string][]string)
	defectCommitCount := 0

	for _, rec := range records {
		if rec.IsFix {
			defectCommitCount++
		}
		for _, f := range rec.Files {
			fileTotal[f]++
			if rec.IsFix {
				fileFixes[f]++
				if len(fileRecentFixes[f]) < 5 {
					fileRecentFixes[f] = append(fileRecentFixes[f], fmt.Sprintf("%s: %s", rec.Hash[:min(7, len(rec.Hash))], rec.Subject))
				}
			}
		}
	}

	var hotspots []Hotspot

	// 1. File-level Hotspots
	for file, fixCount := range fileFixes {
		if fixCount < opts.MinFixes {
			continue
		}

		callerCount := 0
		var dependents []string

		if ix != nil {
			if syms, ok := ix.SymbolsByFile[file]; ok {
				depSet := make(map[string]bool)
				for _, s := range syms {
					for _, caller := range ix.CallersFor(s) {
						depSet[caller] = true
					}
				}
				callerCount = len(depSet)
				for dep := range depSet {
					if len(dependents) < 5 {
						dependents = append(dependents, dep)
					}
				}
			}
		}

		score := calculateScore(fixCount, fileTotal[file], callerCount)
		risk := scoreToRisk(score)

		h := Hotspot{
			Target:         file,
			Kind:           "file",
			File:           file,
			DefectCommits:  fixCount,
			TotalCommits:   fileTotal[file],
			CallerCount:    callerCount,
			FragilityScore: score,
			RiskLevel:      risk,
			RecentFixes:    fileRecentFixes[file],
			TopDependents:  dependents,
		}

		if opts.Target == "" || strings.Contains(strings.ToLower(file), strings.ToLower(opts.Target)) {
			hotspots = append(hotspots, h)
		}
	}

	// 2. Symbol-level Hotspots for symbols in high-fix files
	if ix != nil {
		for _, sym := range ix.Symbols {
			if sym.Kind != "func" && sym.Kind != "method" {
				continue
			}
			fixes := fileFixes[sym.File]
			if fixes < opts.MinFixes {
				continue
			}

			callers := ix.CallersFor(sym)
			callerCount := len(callers)
			if callerCount == 0 && fixes < 2 {
				continue
			}

			score := calculateScore(fixes, fileTotal[sym.File], callerCount)
			risk := scoreToRisk(score)

			h := Hotspot{
				Target:         sym.FullName(),
				Kind:           "symbol",
				File:           sym.File,
				DefectCommits:  fixes,
				TotalCommits:   fileTotal[sym.File],
				CallerCount:    callerCount,
				FragilityScore: score,
				RiskLevel:      risk,
				RecentFixes:    fileRecentFixes[sym.File],
				TopDependents:  callers[:min(5, len(callers))],
			}

			if opts.Target == "" || strings.Contains(strings.ToLower(sym.FullName()), strings.ToLower(opts.Target)) {
				hotspots = append(hotspots, h)
			}
		}
	}

	// Sort hotspots by FragilityScore descending
	sort.Slice(hotspots, func(i, j int) bool {
		if hotspots[i].FragilityScore != hotspots[j].FragilityScore {
			return hotspots[i].FragilityScore > hotspots[j].FragilityScore
		}
		return hotspots[i].DefectCommits > hotspots[j].DefectCommits
	})

	if len(hotspots) > opts.Limit {
		hotspots = hotspots[:opts.Limit]
	}

	return &Report{
		Root:               absRoot,
		EvaluatedCommits:   len(records),
		DefectCommitsCount: defectCommitCount,
		Hotspots:           hotspots,
	}, nil
}

func calculateScore(defectCommits, totalCommits, callerCount int) float64 {
	// Formula: defectCommits * 3.0 + log2(1 + callerCount) * 2.0 + totalCommits * 0.2
	score := float64(defectCommits)*3.0 + math.Log2(1.0+float64(callerCount))*2.0 + float64(totalCommits)*0.2
	return math.Round(score*10) / 10
}

func scoreToRisk(score float64) string {
	switch {
	case score >= 15.0:
		return "CRITICAL"
	case score >= 8.0:
		return "HIGH"
	case score >= 4.0:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

func readGitCommitLog(ctx context.Context, root string, maxCommits int) ([]commitRecord, error) {
	cmd := exec.CommandContext(ctx, "git", "log", "--no-merges", fmt.Sprintf("-n%d", maxCommits), "--name-only", "--format=COMMIT:%H|||%s")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var records []commitRecord
	var cur *commitRecord

	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "COMMIT:") {
			if cur != nil {
				records = append(records, *cur)
			}
			parts := strings.Split(strings.TrimPrefix(line, "COMMIT:"), "|||")
			hash := parts[0]
			subj := ""
			if len(parts) > 1 {
				subj = parts[1]
			}
			cur = &commitRecord{
				Hash:    hash,
				Subject: subj,
				IsFix:   defectKeywords.MatchString(subj),
			}
		} else if cur != nil {
			cur.Files = append(cur.Files, line)
		}
	}
	if cur != nil {
		records = append(records, *cur)
	}

	return records, nil
}
