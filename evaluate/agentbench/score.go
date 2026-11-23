package agentbench

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Usage is the token accounting extracted from a raw agent event stream.
type Usage struct {
	InputSum  *int64 `json:"input_tokens_sum,omitempty"`
	OutputSum *int64 `json:"output_tokens_sum,omitempty"`
	InputMax  *int64 `json:"input_tokens_max,omitempty"`
	OutputMax *int64 `json:"output_tokens_max,omitempty"`
	Events    int    `json:"events_with_tokens"`
	Note      string `json:"note,omitempty"`
}

// RunOutcome is the scored result of one (arm, task) execution.
type RunOutcome struct {
	Arm      string `json:"arm"`
	Task     string `json:"task"`
	Pass     bool   `json:"pass"`
	TimedOut bool   `json:"timed_out"`
	Exit     int    `json:"exit"`
	// DurationSec is wall-clock agent time; -1 when unknown (rescored rows
	// carry the original run value read back from metrics.json).
	DurationSec  float64 `json:"duration_sec"`
	FilesChanged int     `json:"files_changed"`
	NonTestLOC   int     `json:"non_test_loc"`
	TestLOC      int     `json:"test_loc"`
	Usage        Usage   `json:"usage"`
}

// Delta is the deterministic over-engineering proxy: how much non-test code
// the agent added or changed relative to the pristine fixture. Test code is
// counted separately — tests are never bloat.
type Delta struct {
	FilesChanged int `json:"files_changed"` // added or modified
	NonTestLOC   int `json:"non_test_loc"`  // lines in added/modified non-test files
	TestLOC      int `json:"test_loc"`      // lines in added/modified test files
}

// isTestPath classifies a repo-relative path as test code: a Go file ending
// in _test.go, or anything under a test/, testdata/, or *_test/ segment.
func isTestPath(rel string) bool {
	base := filepath.Base(rel)
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == "test" || seg == "testdata" || strings.HasSuffix(seg, "_test") {
			return true
		}
	}
	return false
}

// isGeneratedStatePath excludes tool-owned index and telemetry state from the
// agent's code delta. These files are not task work, even when a plugin creates
// them inside the benchmark workspace.
func isGeneratedStatePath(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".kern" || seg == ".densic" {
			return true
		}
	}
	return false
}

// loc counts lines in data (a trailing partial line counts as one).
func loc(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// workspaceDelta diffs the run workspace against the original fixture.
// Added files contribute all their lines; modified files contribute their
// full new line count (a crude but deterministic proxy); deleted files count
// as changed but contribute no LOC.
func WorkspaceDelta(fixture, ws string) *Delta {
	if st, err := os.Stat(ws); err != nil || !st.IsDir() {
		return nil
	}
	var d Delta
	fixFiles := map[string]bool{}
	_ = filepath.WalkDir(fixture, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(fixture, path)
		if err != nil {
			return nil
		}
		if isGeneratedStatePath(rel) {
			if e.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if e.IsDir() {
			return nil
		}
		fixFiles[rel] = true
		return nil
	})
	_ = filepath.WalkDir(ws, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(ws, path)
		if err != nil {
			return nil
		}
		if isGeneratedStatePath(rel) {
			if e.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if e.IsDir() {
			return nil
		}
		fdata, err := os.ReadFile(filepath.Join(fixture, rel))
		if err == nil {
			wdata, rerr := os.ReadFile(path)
			if rerr == nil && string(fdata) == string(wdata) {
				return nil // unchanged
			}
		}
		d.FilesChanged++
		wdata, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if isTestPath(rel) {
			d.TestLOC += loc(wdata)
		} else {
			d.NonTestLOC += loc(wdata)
		}
		return nil
	})
	return &d
}

// ArmSummary aggregates one arm's runs for the report table.
type ArmSummary struct {
	Arm              string  `json:"arm"`
	Passed           int     `json:"passed"`
	Total            int     `json:"total"`
	MedianTokensSum  *int64  `json:"median_tokens_sum"`
	MedianNonTestLOC int     `json:"median_non_test_loc"`
	TotalWallSec     float64 `json:"total_wall_sec"`
}

// Results is the run-wide artifact written to results.json.
type Results struct {
	GeneratedAt time.Time    `json:"generated_at"`
	Runs        []RunOutcome `json:"runs"`
	Arms        []ArmSummary `json:"arms"`
	Warnings    []string     `json:"warnings,omitempty"`
}

func NewResults(runs []RunOutcome) Results {
	res := Results{GeneratedAt: time.Now().UTC(), Runs: runs}
	byArm := map[string][]RunOutcome{}
	var armOrder []string
	for _, r := range runs {
		if _, ok := byArm[r.Arm]; !ok {
			armOrder = append(armOrder, r.Arm)
		}
		byArm[r.Arm] = append(byArm[r.Arm], r)
	}
	for _, arm := range armOrder {
		rs := byArm[arm]
		var sums []int64
		var locs []int64
		var wall float64
		passed := 0
		for _, r := range rs {
			if r.Pass {
				passed++
			}
			if r.Usage.InputSum != nil {
				sums = append(sums, *r.Usage.InputSum+*r.Usage.OutputSum)
			}
			if r.NonTestLOC > 0 {
				locs = append(locs, int64(r.NonTestLOC))
			}
			wall += r.DurationSec
		}
		s := ArmSummary{
			Arm:              arm,
			Passed:           passed,
			Total:            len(rs),
			MedianTokensSum:  median(sums),
			MedianNonTestLOC: int(medianOrZero(locs)),
			TotalWallSec:     wall,
		}
		res.Arms = append(res.Arms, s)
	}
	return res
}

func median(xs []int64) *int64 {
	if len(xs) == 0 {
		return nil
	}
	s := append([]int64(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	mid := len(s) / 2
	if len(s)%2 == 1 {
		v := s[mid]
		return &v
	}
	v := (s[mid-1] + s[mid]) / 2
	return &v
}

func medianOrZero(xs []int64) int64 {
	if m := median(xs); m != nil {
		return *m
	}
	return 0
}

func PrintSummary(w io.Writer, res Results) {
	fmt.Fprintln(w, "arm          passed    median tokens  median non-test LOC  wall")
	for _, a := range res.Arms {
		tok := "?"
		if a.MedianTokensSum != nil {
			tok = fmt.Sprintf("%d", *a.MedianTokensSum)
		}
		fmt.Fprintf(w, "%-12s %3d/%-3d  %12s  %19d  %6.1fs\n",
			a.Arm, a.Passed, a.Total, tok, a.MedianNonTestLOC, a.TotalWallSec)
	}
}
