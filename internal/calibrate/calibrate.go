// Package calibrate measures how well kern's review model predicts the files
// a commit actually changed — the same F1-style protocol code-review-graph
// uses to publish its calibration numbers.
//
// It reports two complementary tables:
// 1. Threshold sweep. kern_changes assigns every changed file a risk score.
// "Flagging" a file means the reviewer should look at it; the sweep shows
// recall (fraction of changed files the model still flags) and the mean
// review load (flagged files per commit) at each threshold. This is the
// honest calibration of the risk scale: there is no precision here,
// because the analysis is diff-driven and can never flag a file the
// commit did not touch.
// 2. Impact F1 (CRG protocol). Given the symbols a commit touched, the graph
// predicts which files are affected (their blast radius). The ground
// truth is the set of files the commit actually edited. Precision =
// predicted files that were really edited; recall = edited files the
// graph predicted. This measures how well the call graph anticipates
// ripple edits.
// The root defaults to this repository (self-calibration on kern's own PR
// history). Requires a git checkout with a populated history.
package calibrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/fsutil"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/version"
)

// Run executes the calibration harness and writes its report to w.
// root must be a git checkout with populated history. commitsN caps how many
// recent commits are scored; thresholds is the risk-threshold sweep. The
// output format is stable — calibration numbers must stay comparable across
// versions.
func Run(root string, commitsN int, thresholds []float64, w io.Writer) error {
	thr := append([]float64(nil), thresholds...)
	sort.Float64s(thr)

	// Resolve the exact commit range first (cheap): the result cache is keyed
	// by (root, exact range, thresholds), so a warm rerun over an unchanged
	// range returns the stored report without paying for the index load or
	// the history walk.
	out, err := exec.Command("git", append([]string{"-C", root}, "rev-list", "--max-count", fmt.Sprint(commitsN), "HEAD")...).Output()
	if err != nil {
		return fmt.Errorf("rev-list: %w", err)
	}
	commits := nonEmptyLines(string(out))

	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if rep, ok := loadCalibrateCache(abs, commits, thr); ok {
		fmt.Fprintf(w, "# cached calibrate result (%d commits, thresholds %s)\n", len(commits), thresholdKey(thr))
		_, _ = io.WriteString(w, rep)
		return nil
	}

	ix, err := index.Load(root)
	if err != nil || ix == nil {
		ix, err = index.Build(root)
		if err != nil {
			return fmt.Errorf("index: %w", err)
		}
		if serr := ix.Save(); serr != nil {
			log.Printf("calibrate: index save failed (next run will re-index): %v", serr)
		}
	}

	flagged := make([]int, len(thr))  // files flagged at each threshold
	recalled := make([]int, len(thr)) // flagged files that were actually changed
	scored := 0
	skipped := 0
	changedTotal := 0
	var risks []float64

	var tp, fp, fn int // impact-F1 aggregates
	var f1Commits int  // commits with a nonzero predicted set

	// Phase timing (KERN_CALIBRATE_TIMING=1): breakdown of where the history
	// loop spends its time, for perf audits of the harness itself.
	timing := os.Getenv("KERN_CALIBRATE_TIMING") != ""
	tTotal := time.Now()
	var tg, ta, tb time.Duration // git, analyze, blast+affected

	for i, c := range commits {
		// Progress on stderr: the report is written only after the whole
		// history loop, so without this the command looks hung for minutes
		// on large repos (audit M5: zero output in 280s).
		if i%10 == 0 || i == len(commits)-1 {
			fmt.Fprintf(os.Stderr, "calibrate: commit %d/%d (%s)\n", i+1, len(commits), c[:8])
		}
		tSeg := time.Now()
		fc, err := intel.FilesForRangeL(root, c+"^", c)
		if timing {
			tg += time.Since(tSeg)
		}
		if err != nil {
			skipped++
			continue
		}
		tSeg = time.Now()
		report := intel.AnalyzeChangesRanged(ix, fc)
		if timing {
			ta += time.Since(tSeg)
		}
		if len(report.Changes) == 0 {
			skipped++
			continue
		}
		scored++

		ground := map[string]bool{}
		affected := map[string]bool{}
		tSeg = time.Now()
		var blastSyms []string
		for _, ch := range report.Changes {
			ground[ch.File] = true
			changedTotal++
			risks = append(risks, ch.Risk)
			for i, t := range thr {
				if ch.Risk >= t {
					flagged[i]++
					recalled[i]++
				}
			}
			_, blast := intel.BlastRadius(ix, ch.Symbols)
			for s := range blast {
				blastSyms = append(blastSyms, s)
			}
		}
		// AffectedFiles(ix, syms) builds the symbol→file map once per call.
		// Call it once per commit with the union of the commit's blast symbols
		// instead of once per blast symbol: per-symbol calls were O(blast ×
		// symbols) per commit because every call re-walked the whole symbol
		// table (report D1: the 225-file commit alone spent ~98s in this loop
		// on 63k map rebuilds). The union is identical because AffectedFiles
		// only unions the distinct files of its input symbols.
		for _, f := range intel.AffectedFiles(ix, blastSyms) {
			affected[f] = true
		}
		if timing {
			tb += time.Since(tSeg)
		}
		for _, f := range report.Files {
			affected[f] = true
		}

		ctp, cfp, cfn := 0, 0, 0
		for f := range ground {
			if affected[f] {
				ctp++
			} else {
				cfn++
			}
		}
		for f := range affected {
			if !ground[f] {
				cfp++
			}
		}
		tp, fp, fn = tp+ctp, fp+cfp, fn+cfn
		if len(affected) > 0 {
			f1Commits++
		}
	}
	if timing {
		fmt.Fprintf(os.Stderr, "calibrate timing: git=%.2fs analyze=%.2fs blast+affected=%.2fs total=%.2fs\n",
			tg.Seconds(), ta.Seconds(), tb.Seconds(), time.Since(tTotal).Seconds())
	}

	// Render the report into a buffer so the exact same bytes can be cached
	// and replayed on a warm rerun (output format stays byte-stable).
	var rep strings.Builder
	renderReport(&rep, root, scored, skipped, changedTotal, flagged, recalled, thr, tp, fp, fn, f1Commits, risks)
	_ = saveCalibrateCache(abs, commits, thr, rep.String())
	_, _ = io.WriteString(w, rep.String())
	return nil
}

// renderReport writes the calibration report (stable output format — the
// calibration numbers must stay comparable across versions).
func renderReport(rep *strings.Builder, root string, scored, skipped, changedTotal int, flagged, recalled []int, thr []float64, tp, fp, fn, f1Commits int, risks []float64) {
	_, _ = fmt.Fprintf(rep, "root:        %s\n", root)
	_, _ = fmt.Fprintf(rep, "commits:     %d scored, %d skipped (no indexable changes)\n", scored, skipped)

	_, _ = fmt.Fprintf(rep, "\n1) risk-threshold calibration (review load vs recall)\n")
	_, _ = fmt.Fprintf(rep, "%-10s %-12s %-16s\n", "threshold", "recall", "mean flagged/commit")
	for i, t := range thr {
		_, _ = fmt.Fprintf(rep, "%-10.1f %-12.3f %-16.2f\n", t, prec(recalled[i], changedTotal), float64(flagged[i])/float64(scored))
	}

	p := prec(tp, tp+fp)
	r := prec(tp, tp+fn)
	f1 := 0.0
	if p+r > 0 {
		f1 = 2 * p * r / (p + r)
	}
	_, _ = fmt.Fprintf(rep, "\n2) impact F1 (blast radius vs files actually edited)\n")
	_, _ = fmt.Fprintf(rep, "precision=%.3f recall=%.3f F1=%.3f (commits with nonzero predicted set: %d/%d)\n", p, r, f1, f1Commits, scored)

	_, _ = fmt.Fprintln(rep, "\nrisk distribution (across all scored changes):")
	for _, b := range histogram(risks) {
		_, _ = fmt.Fprintf(rep, "  [%4.1f, %4.1f) %5d\n", b.lo, b.hi, b.n)
	}
	_, _ = fmt.Fprintln(rep, `
The threshold with the best recall-vs-load tradeoff on this repo's history is
the calibration point for the risk scale. Pick the knee, or keep the default
4.0 (base 1.0 + log2 callers + log2 transitive blast + 1.5 cross-pkg + 2.0
untested + hub bonuses). Impact F1 is the graph's own error budget: it is what
a review would miss (unpredicted files) and what it would over-flag (predicted
but unedited).`)
}

func prec(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

type bin struct {
	lo, hi float64
	n      int
}

func histogram(risks []float64) []bin {
	if len(risks) == 0 {
		return nil
	}
	sort.Float64s(risks)
	maxN := risks[len(risks)-1]
	width := 2.0
	var out []bin
	for lo := 0.0; lo < maxN+width; lo += width {
		hi := lo + width
		n := 0
		for _, r := range risks {
			if r >= lo && r < hi {
				n++
			}
		}
		if n > 0 || len(out) > 0 {
			out = append(out, bin{lo, hi, n})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Result cache: a warm rerun over an unchanged (root, commit-range,
// thresholds) tuple replays the stored report instead of re-scoring history.
// The cache lives under <root>/.kern/calibrate-cache.json alongside the
// index. A stale key (new commits, amended history, different thresholds or
// window, or a different engine version) simply misses and the entry is
// replaced on the next full run.

const (
	calibrateCacheVersion    = 2
	calibrateCacheMaxEntries = 8
)

type calibrateCacheFile struct {
	Version int                   `json:"version"`
	Entries []calibrateCacheEntry `json:"entries"`
}

type calibrateCacheEntry struct {
	Key        string `json:"key"`
	Root       string `json:"root"`
	Commits    int    `json:"commits"`
	Head       string `json:"head"`
	Thresholds string `json:"thresholds"`
	Report     string `json:"report"`
	SavedAt    string `json:"saved_at"`
}

func calibrateCachePath(root string) string {
	return filepath.Join(root, ".kern", "calibrate-cache.json")
}

// thresholdKey renders the sorted threshold sweep as a stable string.
func thresholdKey(thr []float64) string {
	parts := make([]string, len(thr))
	for i, t := range thr {
		parts[i] = strconv.FormatFloat(t, 'g', -1, 64)
	}
	return strings.Join(parts, ",")
}

// calibrateCacheKey hashes the exact inputs that determine the report:
// absolute root (distinguishes checkouts), the exact commit range, the
// thresholds, and the engine version (internal/version.Version — stamped at
// build time for releases, "dev" on source checkouts). The version component
// is what makes the cache invalidate automatically when a future kern changes
// scoring: a release bump produces a different key, so stale reports can
// never replay across engine versions. Any change to those inputs produces a
// different key and a miss.
func calibrateCacheKey(root string, commits []string, thr []float64) string {
	h := sha256.New()
	_, _ = io.WriteString(h, root)
	_, _ = io.WriteString(h, "\x00")
	for _, c := range commits {
		_, _ = io.WriteString(h, c)
		_, _ = io.WriteString(h, "\x00")
	}
	_, _ = io.WriteString(h, thresholdKey(thr))
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, version.Version)
	return hex.EncodeToString(h.Sum(nil))
}

// loadCalibrateCache returns the stored report for the exact (root, range,
// thresholds) tuple. A missing, corrupt, or version-mismatched cache is a
// plain miss — the harness recomputes.
func loadCalibrateCache(root string, commits []string, thr []float64) (string, bool) {
	key := calibrateCacheKey(root, commits, thr)
	data, err := os.ReadFile(calibrateCachePath(root))
	if err != nil {
		return "", false
	}
	var cf calibrateCacheFile
	if err := json.Unmarshal(data, &cf); err != nil || cf.Version != calibrateCacheVersion {
		return "", false
	}
	for _, e := range cf.Entries {
		if e.Key == key && e.Root == root {
			return e.Report, true
		}
	}
	return "", false
}

// saveCalibrateCache records the freshly computed report under its key,
// replacing any stale entry for the same key and pruning the oldest entries
// beyond calibrateCacheMaxEntries. Writes are atomic (temp file + rename);
// a failure to persist is reported but never fails the calibration itself.
func saveCalibrateCache(root string, commits []string, thr []float64, report string) error {
	key := calibrateCacheKey(root, commits, thr)
	path := calibrateCachePath(root)

	var cf calibrateCacheFile
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cf)
	}
	if cf.Version != calibrateCacheVersion {
		cf = calibrateCacheFile{Version: calibrateCacheVersion}
	}

	head := ""
	if len(commits) > 0 {
		head = commits[0]
	}
	kept := []calibrateCacheEntry{{
		Key:        key,
		Root:       root,
		Commits:    len(commits),
		Head:       head,
		Thresholds: thresholdKey(thr),
		Report:     report,
		SavedAt:    time.Now().UTC().Format(time.RFC3339),
	}}
	for _, e := range cf.Entries {
		if len(kept) >= calibrateCacheMaxEntries {
			break
		}
		if e.Key != key {
			kept = append(kept, e)
		}
	}
	cf.Entries = kept

	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, 0o600)
}
