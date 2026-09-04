package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/gitleaks"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/jscpd"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/metrics"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/sandbox"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
)

// proposal is one --file/--content pair from the fix command: a cleaned
// repo-relative path and the proposed content to write into the worktree.
type proposal struct {
	Path    string // cleaned repo-relative path
	Content string
}

// runFix executes the `blueprint fix` command — the concrete, SAFE "agent
// repair loop". An agent (or a human on the agent's behalf) proposes file
// content via --file/--content pairs; fix validates that content against the
// FULL canonical pipeline inside an ISOLATED SANDBOXED WORKTREE and reports
// per-check status plus the exact diff the fix would produce.
//
// Blueprint fix NEVER mutates the user's repository:
//
//   - a detached git worktree of the repo (the same mechanism the sandbox
//     check uses) is created under a temp dir and removed on exit (defer);
//   - proposed content is written into that worktree only, never into the
//     user's tree;
//   - the validation pipeline runs with the WORKTREE as RepositoryRoot;
//   - audit and metrics state is scoped to the worktree's .blueprint dir, so
//     even the tool's own state dir in the user's repo is untouched.
//
// No rule has machine-applicable fixes today: `fix` verifies agent-authored
// content safely; it does not manufacture fixes itself.
//
// Flags:
//
//	--repo=PATH        Repository root (default: current directory).
//	--file=RELPATH     Repo-relative path to fix (repeatable).
//	--content=STRING   Proposed content for the preceding --file (repeatable).
//	--json             Emit a structured JSON result instead of terminal text.
//
// Exit codes (documented contract): 0 = fix verifies clean (no findings);
// 1 = findings remain (fix blocked); 2 = tool error (kern missing, bad or
// escaping paths, not a git repo, worktree failure); 3 = invalid Blueprint
// configuration.
func runFix(args []string) int {
	fs := flag.NewFlagSet("fix", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "emit JSON output")
	repoRoot := fs.String("repo", "", "repository root (default: current directory)")
	var files, contents stringList
	fs.Var(&files, "file", "repo-relative file path to fix (repeatable, paired with --content)")
	fs.Var(&contents, "content", "proposed content for the preceding --file (repeatable)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0 // -h/--help: usage already printed; exit clean
		}
		return 2
	}
	jsonMode := *jsonOut

	// The change request is agent-authored content, never a diff against the
	// working tree, so at least one --file/--content pair is required.
	if code := requireFixProposals(files, contents); code != 0 {
		return code
	}

	absRoot, code := resolveRepoRoot(*repoRoot)
	if code != 0 {
		return code
	}
	if !isGitRepo(absRoot) {
		if jsonMode {
			emitErrorJSON(2, "not a git repository: "+absRoot)
		} else {
			fmt.Fprintf(os.Stderr, "blueprint: not a git repository: %s\n", absRoot)
		}
		return 2
	}

	// Confine proposed paths before touching anything (see confineProposals):
	// reject paths that escape the worktree via "..", absolutes, or ".git".
	proposals, code := confineProposals(files, contents, jsonMode)
	if code != 0 {
		return code
	}

	// Load configuration exactly like check.go (verdict consistency is the
	// whole point of fix): same loader, same policy engine, same exit 3.
	cfg, err := policy.Load(absRoot)
	if err != nil {
		if jsonMode {
			emitErrorJSON(3, "invalid configuration: "+err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "blueprint: invalid configuration: %v\n", err)
		}
		return 3
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "blueprint: warning: %s\n", w)
	}

	// Isolated sandboxed worktree of the USER's repo. Proposed content is
	// applied here; the worktree is removed on exit.
	worktree, cleanup, err := sandbox.CreateWorktree(absRoot)
	if err != nil {
		if jsonMode {
			emitErrorJSON(2, "create sandbox worktree: "+err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "blueprint: create sandbox worktree: %v\n", err)
		}
		return 2
	}
	defer cleanup()

	// Apply the proposed content into the worktree at its repo-relative path.
	if code := applyProposals(worktree, proposals, jsonMode); code != 0 {
		return code
	}

	// Same client + check set as check.go (see runFixPipeline): the repair
	// loop's checks are architecture, secrets, and duplication.
	result, err := runFixPipeline(absRoot, worktree, cfg, proposals)
	if err != nil {
		if jsonMode {
			emitErrorJSON(2, "kern binary not found: "+err.Error())
		} else {
			fmt.Fprintf(os.Stderr, "blueprint: kern binary not found: %v\n", err)
		}
		return 2
	}

	// Exit-code mapping for fix: ANY finding (WARN or BLOCK per policy) means
	// the fix is blocked and the loop must iterate (see fixExitCode).
	exitCode := fixExitCode(result)
	return renderFixOutput(result, exitCode, absRoot, worktree, proposals, jsonMode)
}

// requireFixProposals validates that at least one --file/--content pair is
// present and that they are paired. Returns 0 on success, 2 after emitting
// the error when the args are invalid.
func requireFixProposals(files, contents []string) int {
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "blueprint: fix: at least one --file RELPATH --content STRING pair is required")
		return 2
	}
	if len(files) != len(contents) {
		fmt.Fprintf(os.Stderr, "blueprint: fix: --file and --content must be paired (got %d files, %d contents)\n", len(files), len(contents))
		return 2
	}
	return 0
}

// confineProposals validates and cleans the proposed repo-relative paths
// before anything is touched (same pattern as the duplication content path
// and the secret content path): reject paths that clean to "..", start with
// "../", are absolute, or shadow ".git" (in a linked worktree .git is a file
// — shadowing it with a directory would corrupt the isolation mechanism).
// Duplicate paths are rejected. Returns 0 on success, 2 after emitting the
// error when a path is invalid.
func confineProposals(files, contents []string, jsonMode bool) ([]proposal, int) {
	proposals := make([]proposal, 0, len(files))
	seen := make(map[string]bool, len(files))
	for i, p := range files {
		rel := filepath.Clean(p)
		if rel == "" || rel == "." || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
			filepath.IsAbs(rel) ||
			rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			msg := fmt.Sprintf("invalid path in --file (must be repo-relative, no .., no absolute, not .git): %q", p)
			if jsonMode {
				emitErrorJSON(2, msg)
			} else {
				fmt.Fprintf(os.Stderr, "blueprint: fix: %s\n", msg)
			}
			return nil, 2
		}
		if seen[rel] {
			msg := fmt.Sprintf("duplicate --file path: %q", p)
			if jsonMode {
				emitErrorJSON(2, msg)
			} else {
				fmt.Fprintf(os.Stderr, "blueprint: fix: %s\n", msg)
			}
			return nil, 2
		}
		seen[rel] = true
		proposals = append(proposals, proposal{Path: rel, Content: contents[i]})
	}
	return proposals, 0
}

// applyProposals writes each proposed file into the worktree at its
// repo-relative path, creating parent directories as needed. Returns 0 on
// success, 2 after emitting the error when a write fails.
func applyProposals(worktree string, proposals []proposal, jsonMode bool) int {
	for _, pr := range proposals {
		dest := filepath.Join(worktree, pr.Path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			msg := fmt.Sprintf("create parent dirs for %s: %v", pr.Path, err)
			if jsonMode {
				emitErrorJSON(2, msg)
			} else {
				fmt.Fprintf(os.Stderr, "blueprint: %s\n", msg)
			}
			return 2
		}
		if err := os.WriteFile(dest, []byte(pr.Content), 0o644); err != nil {
			msg := fmt.Sprintf("write proposed content for %s: %v", pr.Path, err)
			if jsonMode {
				emitErrorJSON(2, msg)
			} else {
				fmt.Fprintf(os.Stderr, "blueprint: %s\n", msg)
			}
			return 2
		}
	}
	return 0
}

// runFixPipeline assembles the fix validation pipeline — same client + check
// set as check.go. The sandbox/build-test check is deliberately NOT included:
// it creates its OWN worktree at HEAD, which would not carry the proposed
// (uncommitted) changes, so it would validate the original state rather than
// the fix — and it adds a full go build+test pass on every loop iteration.
// The repair loop's checks are architecture, secrets, and duplication. The
// pipeline runs against the isolated worktree; audit and metrics are scoped
// to the (removed-on-exit) worktree so fix never writes into the user's
// repository, not even .blueprint/.
func runFixPipeline(absRoot, worktree string, cfg *policy.LoadedConfig, proposals []proposal) (domain.ValidationResult, error) {
	client, err := kern.NewKernClient()
	if err != nil {
		return domain.ValidationResult{}, err
	}
	// P2-4: probe the kern version once for provenance stamping. Best-effort:
	// an empty string on probe failure must never fail validation.
	kernVersion, _ := client.Version()

	req := domain.ChangeRequest{
		RepositoryRoot: worktree,
		Source:         domain.SourceAgent,
		Operation:      domain.OpWrite,
		Files:          make([]domain.FileChange, 0, len(proposals)),
		// P0.4 authz: fix is always an agent flow, so it always carries an
		// identity ($BLUEPRINT_AGENT_ID or "agent").
		AgentID: defaultAgentID(),
	}
	for _, pr := range proposals {
		req.Files = append(req.Files, domain.FileChange{Path: pr.Path, Op: domain.OpWrite, Content: pr.Content})
	}

	checks := []service.Check{
		kern.NewArchitectureCheck(client),
		// T2.1: detection delegated to incumbents (gitleaks, jscpd); each
		// falls back to its in-house check when the binary is absent.
		gitleaks.NewCheck(client),
		jscpd.NewCheck(client),
	}

	opts := []service.Option{
		service.WithConfig(cfg.Service),
		service.WithKernVersion(kernVersion),
		service.WithPolicy(policy.NewEngine(cfg.Policy)),
		// Audit and metrics are scoped to the (removed-on-exit) worktree so
		// fix never writes into the user's repository, not even .blueprint/.
		service.WithAudit(audit.NewWriter(filepath.Join(worktree, ".blueprint", "audit", "audit.jsonl"))),
	}
	if m, err := metrics.Load(metrics.DefaultPath(worktree)); err == nil {
		opts = append(opts, service.WithMetrics(m, metrics.DefaultPath(worktree)))
	}
	svc := service.New(checks, opts...)

	ctx, cancel := context.WithTimeout(context.Background(), timeoutDuration(cfg.Service.TimeoutSec))
	defer cancel()

	return svc.Validate(ctx, req), nil
}

// fixExitCode maps a ValidationResult to the fix exit code (per the
// documented contract): the pipeline's own WARN => 0 is NOT reused — for the
// repair loop ANY finding (WARN or BLOCK per policy) means the fix is blocked
// and the loop must iterate. PASS and SKIP-only runs verify clean.
func fixExitCode(result domain.ValidationResult) int {
	switch {
	case result.Status == domain.StatusError:
		return 2
	case len(result.Findings) > 0:
		return 1
	default:
		return 0
	}
}

// renderFixOutput computes and emits the fix output. Diffs are rendered only
// when the fix verifies clean — a blocked or warned proposal may contain the
// very content that failed validation (secrets, forbidden imports); echoing
// the diff would violate the redaction invariant (spec: never send secrets to
// agents). The agent already holds the content it authored, so nothing is
// lost. Returns the exit code (2 when a diff cannot be computed).
func renderFixOutput(result domain.ValidationResult, exitCode int, absRoot, worktree string, proposals []proposal, jsonMode bool) int {
	showDiffs := len(result.Findings) == 0 && result.Status != domain.StatusError

	diffs := make([]fixDiff, 0, len(proposals))
	if showDiffs {
		for _, pr := range proposals {
			d, newFile, err := diffForFile(absRoot, worktree, pr.Path)
			if err != nil {
				msg := fmt.Sprintf("cannot compute diff for %s: %v", pr.Path, err)
				if jsonMode {
					emitErrorJSON(2, msg)
				} else {
					fmt.Fprintf(os.Stderr, "blueprint: %s\n", msg)
				}
				return 2
			}
			diffs = append(diffs, fixDiff{Path: pr.Path, Diff: d, NewFile: newFile})
		}
	}

	if jsonMode {
		emitFixJSON(result, exitCode, diffs, len(diffs) == 0 && len(proposals) > 0)
	} else {
		emitFixText(result, exitCode, diffs, showDiffs)
	}
	return exitCode
}

// fixDiff is one proposed-file diff in the fix result.
type fixDiff struct {
	Path    string `json:"path"`
	Diff    string `json:"diff"`
	NewFile bool   `json:"new_file"`
}

// fixJSON is the --json output shape for `blueprint fix`.
type fixJSON struct {
	Status        string               `json:"status"`
	ExitCode      int                  `json:"exit_code"`
	CorrelationID string               `json:"correlation_id"`
	DurationMs    int64                `json:"duration_ms"`
	Summary       domain.Summary       `json:"summary"`
	Checks        []domain.CheckResult `json:"checks"`
	Findings      []domain.Finding     `json:"findings"`
	Diffs         []fixDiff            `json:"diffs"`
	DiffOmitted   bool                 `json:"diffs_omitted,omitempty"`
	Note          string               `json:"note,omitempty"`
}

// diffForFile computes the diff between the repo's current on-disk content
// and the proposed (worktree) content for a repo-relative path, using
// `git diff --no-index`. For files that do not exist in the repo yet, it
// returns the proposed content as an addition. git diff exits 1 when the
// files differ — that is the expected "differences exist" signal; exit 0
// means identical; any other exit is a tool error.
func diffForFile(repoRoot, worktree, rel string) (string, bool, error) {
	repoFile := filepath.Join(repoRoot, rel)
	worktreeFile := filepath.Join(worktree, rel)

	if _, err := os.Stat(repoFile); err != nil {
		if !os.IsNotExist(err) {
			return "", false, err
		}
		b, err := os.ReadFile(worktreeFile)
		if err != nil {
			return "", false, err
		}
		return string(b), true, nil
	}

	wtRel, err := filepath.Rel(repoRoot, worktreeFile)
	if err != nil {
		return "", false, err
	}
	cmd := exec.Command("git", "diff", "--no-index", "--", rel, wtRel)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	d := string(out)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// Differences exist: git's normal "not identical" signal.
		} else {
			return "", false, fmt.Errorf("git diff --no-index: %w: %s", err, strings.TrimSpace(d))
		}
	}
	// Normalize the b/ side header back to the repo-relative path so the
	// diff reads like the future commit.
	d = strings.ReplaceAll(d, "b/"+wtRel, "b/"+rel)
	return d, false, nil
}

// fixWarnNote is the clarifying line printed (text mode) / carried as the
// additive "note" field (--json mode) when a fix run exits 1 with a WARN-only
// result. The exit-code contract (see fixExitCode) is deliberate: ANY
// remaining finding — WARN or BLOCK — means the fix is blocked and the repair
// loop must iterate; fix deliberately does NOT reuse the pipeline's own
// WARN => 0 mapping.
const fixWarnNote = "note: fix exits 1 while ANY finding remains (WARN or BLOCK); iterate the repair loop until the fix verifies clean (exit 0)"

// emitFixText renders the terminal output: check.go-style per-check lines,
// the findings list, and (for clean fixes) the Diff section.
func emitFixText(result domain.ValidationResult, exitCode int, diffs []fixDiff, showDiffs bool) {
	fmt.Printf("blueprint: %s (exit %d)\n", result.Status, exitCode)
	if exitCode == 1 && result.Status == domain.StatusWarn {
		fmt.Println(fixWarnNote)
	}
	fmt.Printf("correlation: %s\n", result.CorrelationID)
	fmt.Printf("duration: %dms\n", result.DurationMs)
	fmt.Printf("findings: %d (errors=%d warnings=%d blocks=%d)\n",
		result.Summary.Total, result.Summary.Errors, result.Summary.Warnings, result.Summary.Blocks)
	for _, cr := range result.Checks {
		fmt.Printf("  [%s] %s (%dms, %d findings)\n", cr.Status, cr.Name, cr.Duration, len(cr.Findings))
	}
	if len(result.Findings) > 0 {
		fmt.Println("\nFindings:")
		for _, f := range result.Findings {
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			fmt.Printf("  [%s] %s: %s (%s)\n", f.Severity, loc, f.Message, f.RuleID)
		}
	}
	if len(diffs) > 0 {
		fmt.Println("\nDiff:")
		for _, d := range diffs {
			if d.NewFile {
				fmt.Printf("\n%s (new file)\n", d.Path)
			} else {
				fmt.Printf("\n%s\n", d.Path)
			}
			fmt.Println(d.Diff)
		}
	} else if !showDiffs {
		fmt.Println("\nDiff: omitted (findings remain — proposed content redacted)")
	}
}

// emitFixJSON prints the structured fix result.
func emitFixJSON(result domain.ValidationResult, exitCode int, diffs []fixDiff, diffOmitted bool) {
	out := fixJSON{
		Status:        string(result.Status),
		ExitCode:      exitCode,
		CorrelationID: result.CorrelationID,
		DurationMs:    result.DurationMs,
		Summary:       result.Summary,
		Checks:        result.Checks,
		Findings:      result.Findings,
		Diffs:         diffs,
		DiffOmitted:   diffOmitted,
	}
	if exitCode == 1 && result.Status == domain.StatusWarn {
		out.Note = fixWarnNote
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

// stringList collects repeatable string flags (--file, --content).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
