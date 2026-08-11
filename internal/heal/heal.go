// Package heal implements the self-correction loop (#9): when validation
// fails, it asks a local LLM for corrected file contents, applies the fix to
// a throwaway snapshot copy of the project, re-runs validation there, and
// reports the resulting diff. The user's working tree is never touched.
package heal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/diff"
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
}

const systemPrompt = `You are a senior software engineer fixing build/test failures.
You receive a task, the failing validation output, and the content of the file(s)
involved. Reply with corrected FULL file contents, one per file, formatted as:

### FILE: path/relative/to/root
<entire corrected file>

Do not include any other text, commentary, or diff markers. Only the FILE blocks.`

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
func Run(ctx context.Context, root, task, model string, maxRounds int, timeout time.Duration) *Result {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	res := &Result{}
	c, err := validate.Detect(root)
	if err != nil {
		res.Err = err
		res.Duration = time.Since(start)
		return res
	}
	res.Command = c

	snap, err := sandbox.Snapshot(root)
	if err != nil {
		res.Err = fmt.Errorf("snapshot: %w", err)
		res.Duration = time.Since(start)
		return res
	}
	defer snap.Close()

	// Baseline validation in the snapshot (same result as the live tree).
	base := validate.Run(ctx, snap.Tmp(), c, timeout)
	res.LastOutput = base.Output
	if base.OK {
		res.Validated = true
		res.Duration = time.Since(start)
		return res
	}

	client := llm.New(model)
	iter := 0
	for iter < maxRounds {
		iter++
		if ctx.Err() != nil {
			res.Err = fmt.Errorf("cancelled")
			res.Iterations = iter - 1
			res.Duration = time.Since(start)
			return res
		}
		failPaths := failingFiles(root, base.Output)
		var b strings.Builder
		b.WriteString("TASK: " + task + "\n\n")
		b.WriteString("VALIDATION COMMAND: " + c.Cmd + " " + strings.Join(c.Args, " ") + "\n\n")
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
		reply, cerr := client.Complete(systemPrompt, b.String())
		if cerr != nil {
			res.Err = fmt.Errorf("llm round %d: %w", iter, cerr)
			res.Iterations = iter
			res.Duration = time.Since(start)
			return res
		}
		reps := ParseReplacements(reply)
		if len(reps) == 0 {
			res.Err = fmt.Errorf("llm round %d: no ### FILE: blocks in reply", iter)
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
		next := validate.Run(ctx, snap.Tmp(), c, timeout)
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
	}
	res.Duration = time.Since(start)
	return res
}

var failLineRe = regexp.MustCompile(`(?m)^([^\s:][^:]+):(\d+)(?::\d+)?[: ]`)

// failingFiles extracts relative file paths from compiler/test output and
// keeps only those that exist under root. Paths are resolved against root
// (not the process working directory) so `kern heal` is correct when invoked
// from elsewhere. Absolute paths and paths escaping root via ".." are never
// probed — untrusted tool output must not become a filesystem oracle (W2-32).
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
