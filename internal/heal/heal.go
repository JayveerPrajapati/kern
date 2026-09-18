// Package heal implements a self-correction loop: when validation fails, it
// asks a local LLM for corrected file contents, applies the fix to a
// throwaway snapshot copy of the project, re-runs validation there, and
// reports the resulting diff. The user's working tree is never touched.
package heal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/diff"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/llm"
	"github.com/JayveerPrajapati/kern/internal/sandbox"
	"github.com/JayveerPrajapati/kern/internal/validate"
)

// Replacement is a full-file replacement suggested by the model.
type Replacement struct {
	Path    string
	Content string
}

var fileBlockRe = regexp.MustCompile(`^###\s*FILE:\s*(\S+)\s*$`)

// Result of a heal cycle.
type Result struct {
	Command    *validate.Command
	Validated  bool     // final validation passed
	Iterations int      // number of correction rounds used
	Changes    []string // relative paths changed in the snapshot
	Diff       string   // unified diff old vs new for changed files
	LastOutput string   // final validation output (or last failure)
	Err        error    // non-nil if LLM unavailable or apply failed
	Duration   time.Duration
	// Unvalidated lists the per-extension checks that could not run (missing
	// toolchain or no syntax parser). It is set — with Validated false and no
	// repair attempted — when validation produced no failures but also no
	// real verdict: "unable to validate" must never be reported as OK.
	Unvalidated []string
}

const systemPrompt = `You are a senior software engineer fixing build/test failures.
You receive a task, the failing validation output, and the content of the file(s)
involved. Reply with corrected FULL file contents, one per file, formatted as:

### FILE: path/relative/to/root
<entire corrected file>

Do not include any other text, commentary, or diff markers. Only the FILE blocks.`

// roundTimeout bounds a single LLM round: a provider (or the agent-CLI
// chain it falls back to) that hangs must not block the heal loop beyond
// this bound. It is a package var so tests can inject a short deadline.
var roundTimeout = 120 * time.Second

// ParseReplacements extracts ### FILE: blocks from model output.
func ParseReplacements(text string) []Replacement {
	var out []Replacement
	lines := strings.Split(text, "\n")
	var cur *Replacement
	flush := func() {
		if cur != nil && cur.Path != "" {
			cur.Content = strings.TrimRight(cur.Content, "\n")
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, ln := range lines {
		if m := fileBlockRe.FindStringSubmatch(ln); m != nil {
			flush()
			cur = &Replacement{Path: strings.TrimPrefix(m[1], "./")}
			continue
		}
		if cur != nil {
			cur.Content += ln + "\n"
		}
	}
	flush()
	return out
}

// fencedBlockRe matches a ``` fenced code block (any language hint).
var fencedBlockRe = regexp.MustCompile("(?s)```[^\n]*\n(.*?)```")

// ParseReplacementsWithFallback is ParseReplacements with a fenced-code-block
// fallback for agent-style replies: some LLM front-ends (e.g. `opencode run`,
// which is an agent, not a raw generator) reply with prose and fenced code
// blocks instead of `### FILE:` markers. When no FILE block is present and
// exactly ONE failing file is known, the first fenced block is treated as the
// replacement for that file. Any other shape (multiple FILE-less blocks, no
// fences, multiple failing files) stays ambiguous and returns nothing — the
// caller keeps its loud "no FILE blocks" error rather than guessing.
func ParseReplacementsWithFallback(text string, fallbackPaths []string) []Replacement {
	if reps := ParseReplacements(text); len(reps) > 0 {
		return reps
	}
	if len(fallbackPaths) != 1 {
		return nil
	}
	m := fencedBlockRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	content := strings.TrimSpace(m[1])
	if content == "" {
		return nil
	}
	return []Replacement{{Path: fallbackPaths[0], Content: content}}
}

// Apply writes replacements into root (any relative dirs created).
func Apply(root string, reps []Replacement) error {
	for _, r := range reps {
		p := filepath.Join(root, filepath.FromSlash(r.Path))
		if !strings.HasPrefix(p, filepath.Clean(root)+string(filepath.Separator)) {
			return fmt.Errorf("replacement escapes root: %s", r.Path)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(r.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Run runs a heal cycle on a snapshot of root. task is the user's original
// instruction. model selects the Ollama model ("" = default). maxRounds caps
// correction attempts. Original tree is untouched; the diff is computed
// against the live files so the user can review and apply. ctx cancels the
// loop (validation runs are aborted) when the caller aborts.
//
// force overrides the P2 mutation gate: each round's repair targets are
// assessed with the shared pre-edit verdict, and a HIGH verdict refuses the
// round unless force is set. The live tree is never written either way — the
// gate keeps the loop from burning LLM rounds on (and proposing) hub-wide
// rewrites the operator has not sanctioned.
func Run(ctx context.Context, root, task, model string, maxRounds int, timeout time.Duration, force bool) *Result {
	return RunFile(ctx, root, task, model, "", maxRounds, timeout, force)
}

// RunFile is Run constrained to a single root-relative file (heal --file):
// validation only covers the per-extension checks for that file, and repair
// replacements are filtered to it. A file filter never widens validation —
// sibling files in other languages are neither checked nor repaired.
//
// Validation is per-extension (validate.RunChecks), replacing the old
// single-language auto-detect: a polyglot repo used to validate with one
// command for whichever language won detection, so a broken .go file passed
// as "validated OK" when Python won. Now every detected language group runs
// its own check — .go always gets the deterministic in-process go/parser
// syntax parse (no toolchain), .py keeps compileall — and a group with no
// available checker is reported as "unable to validate" (Unvalidated), never
// as OK.
func RunFile(ctx context.Context, root, task, model, file string, maxRounds int, timeout time.Duration, force bool) *Result {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	res := &Result{}
	// Detect still supplies the primary command label for display/compat; the
	// actual validation below is per-extension and does not depend on it.
	c, derr := validate.Detect(root)
	res.Command = c

	snap, err := sandbox.Snapshot(root)
	if err != nil {
		res.Err = fmt.Errorf("snapshot: %w", err)
		res.Duration = time.Since(start)
		return res
	}
	defer snap.Close()

	// Baseline validation in the snapshot (same result as the live tree).
	base := validate.RunChecks(ctx, snap.Tmp(), file, timeout)
	res.LastOutput = base.Output
	if base.Err != nil {
		if derr != nil {
			res.Err = derr
		} else {
			res.Err = base.Err
		}
		res.Duration = time.Since(start)
		return res
	}
	if base.OK {
		res.Validated = true
		res.Duration = time.Since(start)
		return res
	}
	if len(base.FailedChecks()) == 0 {
		// Nothing failed, but some checks could not run: there is nothing to
		// repair and no verdict to claim. Report "unable to validate".
		res.Unvalidated = base.SkippedChecks()
		res.Duration = time.Since(start)
		return res
	}

	prov, perr := llm.NewProvider()
	if perr != nil {
		res.Err = fmt.Errorf("llm: %w", perr)
		res.Iterations = 0
		res.Duration = time.Since(start)
		return res
	}
	iter := 0
	// Gate index, loaded once: repairs land in the snapshot while the live
	// tree (and therefore this index) stays fixed across rounds.
	var gateIx *index.Index
	if !force {
		if ix, ierr := intel.ReadIndex(root); ierr == nil {
			gateIx = ix
		}
	}
	for iter < maxRounds {
		iter++
		if ctx.Err() != nil {
			res.Err = fmt.Errorf("cancelled")
			res.Iterations = iter - 1
			res.Duration = time.Since(start)
			return res
		}
		failPaths := failingFiles(root, base.Output)
		if file != "" {
			failPaths = keepFile(failPaths, file)
		}
		// P2 mutation gate: refuse to draft hub-wide rewrites without
		// sanction. The live tree is untouched either way — the gate fires
		// before any LLM round is spent.
		if gateIx != nil && len(failPaths) > 0 {
			if msg := intel.AssessEditFiles(gateIx, failPaths).Refusal("heal"); msg != "" {
				res.Err = fmt.Errorf("%s", msg)
				res.Iterations = iter - 1
				res.Duration = time.Since(start)
				return res
			}
		}
		var b strings.Builder
		b.WriteString("TASK: " + task + "\n\n")
		if c != nil {
			b.WriteString("VALIDATION COMMAND: " + c.Cmd + " " + strings.Join(c.Args, " ") + "\n\n")
		} else {
			b.WriteString("VALIDATION COMMAND: per-extension syntax checks\n\n")
		}
		b.WriteString("FAILING OUTPUT:\n" + truncate(base.Output, 6000) + "\n\n")
		b.WriteString("RELEVANT FILE CONTENTS:\n")
		for _, fp := range failPaths {
			data, rerr := os.ReadFile(filepath.Join(root, fp))
			if rerr != nil {
				continue
			}
			b.WriteString(fmt.Sprintf("### FILE: %s\n%s\n", fp, truncate(string(data), 8000)))
		}
		if len(failPaths) == 0 {
			b.WriteString("(no file:line references found in output; apply your own judgement)\n")
		}
		// A per-round hard deadline keeps a single hung Generate (e.g. an
		// agent CLI in the provider chain waiting on a session) from blocking
		// the loop beyond a bound; the outer ctx still cancels the whole run.
		roundCtx, roundCancel := context.WithTimeout(ctx, roundTimeout)
		reply, cerr := prov.Generate(roundCtx, systemPrompt, b.String(), llm.Options{Model: model})
		roundCancel()
		if cerr != nil {
			if errors.Is(cerr, context.DeadlineExceeded) {
				res.Err = fmt.Errorf("llm round %d: %w (timed out after %s)", iter, cerr, roundTimeout)
			} else {
				res.Err = fmt.Errorf("llm round %d: %w", iter, cerr)
			}
			res.Iterations = iter
			res.Duration = time.Since(start)
			return res
		}
		reps := ParseReplacementsWithFallback(reply, failPaths)
		if file != "" {
			reps = keepReps(reps, file)
		}
		if len(reps) == 0 {
			if file != "" {
				res.Err = fmt.Errorf("llm round %d: no ### FILE: blocks for %s", iter, file)
			} else {
				res.Err = fmt.Errorf("llm round %d: no ### FILE: blocks in reply", iter)
			}
			res.Iterations = iter
			res.Duration = time.Since(start)
			return res
		}
		if aerr := Apply(snap.Tmp(), reps); aerr != nil {
			res.Err = fmt.Errorf("apply round %d: %w", iter, aerr)
			res.Iterations = iter
			res.Duration = time.Since(start)
			return res
		}
		// Rerun validation.
		next := validate.RunChecks(ctx, snap.Tmp(), file, timeout)
		res.LastOutput = next.Output
		base = next
		for _, r := range reps {
			res.Changes = append(res.Changes, r.Path)
		}
		res.Iterations = iter
		if next.OK {
			res.Validated = true
			// Build a unified diff against live files for review.
			var d strings.Builder
			for _, r := range reps {
				oldB, err1 := os.ReadFile(filepath.Join(root, r.Path))
				if err1 != nil {
					continue
				}
				d.WriteString(diff.Unified(r.Path, r.Path+" (healed)", splitLines(string(oldB)), splitLines(r.Content)))
			}
			res.Diff = d.String()
			res.Duration = time.Since(start)
			return res
		}
		if len(next.FailedChecks()) == 0 {
			// Repair changed the tree but left only unvalidatable checks: stop
			// rather than burning more rounds on a project we cannot judge.
			res.Unvalidated = next.SkippedChecks()
			res.Duration = time.Since(start)
			return res
		}
	}
	res.Duration = time.Since(start)
	return res
}

// keepFile narrows failing-file candidates to the --file target.
func keepFile(paths []string, file string) []string {
	var out []string
	for _, p := range paths {
		if p == file {
			out = append(out, p)
		}
	}
	return out
}

// keepReps narrows model replacements to the --file target.
func keepReps(reps []Replacement, file string) []Replacement {
	var out []Replacement
	for _, r := range reps {
		if r.Path == file {
			out = append(out, r)
		}
	}
	return out
}

var failLineRe = regexp.MustCompile(`(?m)^([^\s:\n][^:\n]+):(\d+)(?::\d+)?[: ]`)

// failingFiles extracts relative file paths from compiler/test output and
// keeps only those that exist under root. Paths are resolved against root
// (not the process working directory) so `kern heal` is correct when invoked
// from elsewhere. Absolute paths and paths escaping root via ".." are never
// probed — untrusted tool output must not become a filesystem oracle.
func failingFiles(root, output string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range failLineRe.FindAllStringSubmatch(output, -1) {
		p := filepath.Clean(strings.TrimPrefix(m[1], "./"))
		if p == "." || p == "" || filepath.IsAbs(p) {
			continue
		}
		cand := filepath.Join(root, p)
		if _, err := os.Stat(cand); err != nil {
			continue
		}
		if root != "" {
			rel, err := filepath.Rel(root, cand)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// Candidate is one speculative repair candidate containing replacements and a label.
type Candidate struct {
	ID           string
	Summary      string
	Replacements []Replacement
}

// CandidateResult is the validation outcome of evaluating one speculative Candidate.
type CandidateResult struct {
	Candidate Candidate
	Passed    bool
	Output    string
	Diff      string
	Duration  time.Duration
}

// EvaluateCandidate evaluates a single candidate replacement in an isolated snapshot.
func EvaluateCandidate(ctx context.Context, root string, cand Candidate, file string, timeout time.Duration) CandidateResult {
	start := time.Now()
	snap, err := sandbox.Snapshot(root)
	if err != nil {
		return CandidateResult{
			Candidate: cand,
			Passed:    false,
			Output:    fmt.Sprintf("snapshot: %v", err),
			Duration:  time.Since(start),
		}
	}
	defer snap.Close()

	if err := Apply(snap.Tmp(), cand.Replacements); err != nil {
		return CandidateResult{
			Candidate: cand,
			Passed:    false,
			Output:    fmt.Sprintf("apply: %v", err),
			Duration:  time.Since(start),
		}
	}

	res := validate.RunChecks(ctx, snap.Tmp(), file, timeout)
	var diffBuilder strings.Builder
	for _, r := range cand.Replacements {
		oldB, err1 := os.ReadFile(filepath.Join(root, r.Path))
		if err1 != nil {
			continue
		}
		diffBuilder.WriteString(diff.Unified(r.Path, r.Path+" (candidate)", splitLines(string(oldB)), splitLines(r.Content)))
	}

	return CandidateResult{
		Candidate: cand,
		Passed:    res.OK,
		Output:    res.Output,
		Diff:      diffBuilder.String(),
		Duration:  time.Since(start),
	}
}

// EvaluateCandidates evaluates multiple speculative repair candidates in isolated sandboxes
// and returns the first passing candidate (or all results if none pass).
func EvaluateCandidates(ctx context.Context, root string, candidates []Candidate, file string, timeout time.Duration) (*CandidateResult, []CandidateResult) {
	var all []CandidateResult
	for _, cand := range candidates {
		if ctx.Err() != nil {
			break
		}
		res := EvaluateCandidate(ctx, root, cand, file, timeout)
		all = append(all, res)
		if res.Passed {
			return &res, all
		}
	}
	return nil, all
}
