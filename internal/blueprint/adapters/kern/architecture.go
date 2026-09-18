package kern

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// ArchitectureCheck enforces the architecture boundaries declared in
// .kern/boundaries.json by running `kern guard check --file <staged> --json`
// in the repository root. The check may create or refresh `.kern/` (kern's
// local index cache — gitignore it) when the index is missing or stale, but it
// never mutates source files.
//
// New-change principle (spec Phase 2, lines 727-731): only violations in
// changed files are reported. This is enforced two ways:
//  1. IndexBuild refreshes the symbol graph so edges reflect current files.
//  2. GuardCheckFiles passes --file with the staged file paths, so kern's
//     CheckBoundaries only examines those files (not the whole repo).
//
// Strict-baseline mode: when StrictBaseline is true, ALL violations in the
// repo are reported (kern guard check with no --file), not just new ones.
type ArchitectureCheck struct {
	client         *KernClient
	StrictBaseline bool // when true, check whole repo (all violations), not just changed files
}

// NewArchitectureCheck constructs an ArchitectureCheck backed by client.
func NewArchitectureCheck(client *KernClient) *ArchitectureCheck {
	return &ArchitectureCheck{client: client}
}

// NewArchitectureCheckStrict constructs an ArchitectureCheck in strict-baseline
// mode: it reports all violations in the repository, not just those introduced
// by the current change.
func NewArchitectureCheckStrict(client *KernClient) *ArchitectureCheck {
	return &ArchitectureCheck{client: client, StrictBaseline: true}
}

// Name returns the stable check identifier used for policy routing
// ("<category>:<detail>", see policy.categoryFromCheck).
func (c *ArchitectureCheck) Name() string { return "architecture:guard" }

// Run executes the boundary check and maps every violation to a blocking
// finding. A nil error with StatusPass/StatusBlock means the check ran to
// completion; StatusError is returned for tool or environment failures.
func (c *ArchitectureCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	// Degraded / missing-.kern / empty-change pre-checks: when the guard
	// cannot run at all, report the visible result (WARN finding, SKIP, or
	// ERROR) and return.
	if result, done := c.degradedWarnFinding(req); done {
		return result, nil
	}

	// Strict mode checks ALL tracked files in the repo, not just changed ones.
	// kern's default ChangedFiles only sees working-tree diffs, so we enumerate
	// tracked files via `git ls-files` and pass them to --file explicitly.
	// Tracked files are also fed to the staleness check below (strict mode).
	var tracked []string
	if c.StrictBaseline {
		var lsErr error
		tracked, lsErr = gitTrackedFiles(req.RepositoryRoot)
		if lsErr != nil {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "strict baseline: " + lsErr.Error()}, nil
		}
	}

	// An empty staged set (e.g. an all-deletions change: every File has
	// OpDelete, which stagedFilePaths drops) leaves nothing new to check.
	// GuardCheckFiles with an empty list falls back to GuardCheck, which scans
	// the whole working tree (staged AND unstaged) and would surface
	// pre-existing unstaged violations as if they belonged to this change.
	// Skip instead, mirroring the missing-.kern skip above.
	if !c.StrictBaseline && len(stagedFilePaths(req)) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusSkip, Skipped: true}, nil
	}

	// Rebuild the kern index only when it is stale (content-addressed
	// staleness verdict; P2-4 index freshness provenance; see
	// ensureFreshIndex). A stale index is rebuilt once and re-verified; a
	// rebuild that does not converge is an ERROR, never a silent pass.
	freshness, result, done := c.ensureFreshIndex(ctx, req)
	if done {
		return result, nil
	}

	// authz gate: ask kern for the change's authorization verdict BEFORE
	// the boundary check (degradation contract and denial handling in
	// authzVerdict). A denied verdict blocks and skips boundary evaluation.
	authzFindings, result, done := c.authzVerdict(ctx, req)
	if done {
		return result, nil
	}

	var err error
	var violations []Violation
	if c.StrictBaseline {
		violations, _, _, err = c.client.GuardCheckFiles(ctx, req.RepositoryRoot, tracked)
	} else {
		// New-change principle: scope to staged files only.
		files := stagedFilePaths(req)
		violations, _, _, err = c.client.GuardCheckFiles(ctx, req.RepositoryRoot, files)
	}
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: err.Error()}, nil
	}

	// Map the guard violations, the projected-import check (B6), and any authz
	// WARN findings into the finding list and aggregate the final status.
	return c.buildGuardFindings(req, violations, authzFindings, freshness), nil
}

// degradedWarnFinding handles the pre-check paths where the guard cannot run
// at all, returning (done=true) the result to return immediately:
//   - kern binary unavailable: WARN (kern:unavailable). The architecture leg
//     is blocking (domain/legs.go), so a bare SKIP would hide that the guard
//     never ran; WARN keeps the leg visible as "ran but degraded" and exits 0
//     (a BLOCK from another check still wins: BLOCK > WARN in aggregation);
//   - missing repository root: ERROR;
//   - no .kern directory: WARN (architecture:not-enforced) for a non-empty
//     change — "failure is never a silent pass" — and SKIP for a change with
//     nothing to check (no staged files means there is no signal to warn
//     about). Policy mode "off" still downgrades to SKIP downstream.
func (c *ArchitectureCheck) degradedWarnFinding(req domain.ChangeRequest) (domain.CheckResult, bool) {
	if c.client == nil {
		return domain.CheckResult{
			Name:   c.Name(),
			Status: domain.StatusWarn,
			Findings: []domain.Finding{{
				RuleID:       "kern:unavailable",
				Severity:     domain.SeverityWarn,
				Category:     domain.CategoryPolicy,
				Message:      "architecture guard skipped: kern binary unavailable",
				Explanation:  "The architecture guard requires the kern index. The kern binary was not found, so this check cannot run. Install kern or run with --require-kern to enforce.",
				SuggestedFix: "Install kern (set KERN_BINARY or add kern to $PATH), or run blueprint check with --require-kern to fail closed when kern is missing.",
				RuleVersion:  "1",
				Confidence:   1.0,
				Scope:        "repo",
			}},
		}, true
	}
	if req.RepositoryRoot == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, true
	}
	// No .kern directory means no boundaries have been declared. This is not
	// an error: a repository with no guardrails permits everything (mirrors
	// kern's LoadBoundaries, which yields an empty rule set for a missing
	// file). But architecture that is not evaluated must be visible, so a
	// non-empty change surfaces a WARN finding instead of a bare SKIP.
	// kern's index directory is gitignored, so a validation run in a
	// detached-worktree sandbox (e.g. CI at a non-HEAD ref) will not contain
	// it even when the source repo has a fresh index — distinguish that case
	// from "boundaries were never declared".
	if _, err := os.Stat(filepath.Join(req.RepositoryRoot, ".kern")); err != nil {
		if len(stagedFilePaths(req)) == 0 {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusSkip, Skipped: true}, true
		}
		msg, expl, fix := archNoKernDetail(req.RepositoryRoot)
		return domain.CheckResult{
			Name:   c.Name(),
			Status: domain.StatusWarn,
			Findings: []domain.Finding{{
				RuleID:       "architecture:not-enforced",
				Severity:     domain.SeverityWarn,
				Category:     domain.CategoryArchitecture,
				Message:      msg,
				Explanation:  expl,
				SuggestedFix: fix,
				RuleVersion:  "1",
				Confidence:   1.0,
				Scope:        "repo",
			}},
		}, true
	}
	return domain.CheckResult{}, false
}

// archNoKernDetail builds the architecture:not-enforced message for a repo
// (or validation sandbox) with no .kern/ at the evaluated path. When the git
// worktree list shows the source repo keeps a .kern/ index elsewhere (kern's
// index is gitignored, so a detached-worktree CI sandbox cannot see it), the
// WARN is phrased as "sandbox cannot see the index" rather than "kern has
// never been run here".
func archNoKernDetail(evalRoot string) (msg, explanation, fix string) {
	noIndexAtAll := true
	if out, err := exec.Command("git", "-C", evalRoot, "worktree", "list", "--porcelain").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "worktree ") {
				continue
			}
			p := strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
			if _, err := os.Stat(filepath.Join(p, ".kern")); err == nil {
				noIndexAtAll = false
				break
			}
		}
	}
	if noIndexAtAll {
		return "no .kern/ index present in this repository; architecture boundaries are not evaluated, so this change is not checked against any boundary rules",
			"No .kern/ means no architecture boundaries have been declared for this repository. Without the index, the guard check cannot detect boundary violations, so a change that would otherwise be blocked could pass silently.",
			"Declare boundaries with `kern guard init` (writes .kern/boundaries.json) or commit a .kern/boundaries.json, then re-run the check."
	}
	return "the repository has a .kern/ index, but this validation sandbox cannot see it; architecture boundaries were not enforced for this change",
		"The .kern/ index exists in the repository's git worktree(s), but this validation ran in an isolated sandbox (e.g. a detached-worktree CI run) where the gitignored index is absent. The change was therefore NOT checked against the declared boundary rules — it is not proof that boundaries are clean.",
		"Commit boundaries into the repo so sandboxes can enforce them (kern guard init writes .kern/boundaries.json; make sure it is tracked, while .kern/the-index itself stays gitignored), or run the check against the indexed working tree."
}

// ensureFreshIndex rebuilds the kern index only when it is stale and returns
// the index freshness verdict ("fresh"/"rebuilt") for provenance stamping on
// every finding. Staleness is kern's authoritative content-addressed
// verdict, not an mtime heuristic — operations that preserve mtimes (e.g.
// `git apply`) can no longer hide a stale index (git hashes content, so a
// content edit flips the tree OID even when the mtime is untouched).
// A stale index is rebuilt once and re-verified; a rebuild that does NOT
// converge is an ERROR (done=true), never a silent pass on a
// potentially-misleading index.
//
// Phase 2 consolidation: the default path is ONE subprocess —
// `kern index ensure-fresh --json` (EnsureFreshIndex) — which performs the
// whole probe → update → re-verify sequence internally: a cheap NON-strict
// git tree-OID stale check (no content walk), an incremental update (or full
// build when no index loads) when stale, a strict post-save re-verify from
// disk as the trust anchor, a loose re-verify when strict cannot converge
// (strict counts unparseable files a build skips), and a fail-closed "stale"
// result on non-convergence. This replaces the former 3-4 subprocess sequence
// (status --strict → update → status --strict → status) that cost ~2.6s per
// pre-commit `kern check` invocation. The pre-update probe's strictness was
// deliberately redundant — the post-update strict re-verify is the real
// observation.
//
// The BLUEPRINT_ALLOW_STALE_REBUILD=1 env branch is unchanged: an explicit
// opt-in that rebuilds once and trusts the result without re-verifying
// convergence.
func (c *ArchitectureCheck) ensureFreshIndex(ctx context.Context, req domain.ChangeRequest) (freshness string, result domain.CheckResult, done bool) {
	if os.Getenv("BLUEPRINT_ALLOW_STALE_REBUILD") == "1" {
		// Explicit opt-in to the legacy rebuild-and-continue behavior: rebuild
		// once and trust the result without re-verifying convergence.
		if _, _, err := c.client.IndexUpdate(ctx, req.RepositoryRoot); err != nil {
			return "", domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "stale index and rebuild failed: " + err.Error()}, true
		}
		log.Printf("blueprint: BLUEPRINT_ALLOW_STALE_REBUILD=1: rebuilt stale kern index without re-verification (explicit risk opt-in)")
		return "rebuilt", domain.CheckResult{}, false
	}
	// Default path (P2-4 consolidation): ONE subprocess —
	// `kern index ensure-fresh --json` — loads the cached index, cheaply
	// checks staleness (git tree-OID compare, no content walk), updates (or
	// full-builds) when stale, strictly re-verifies from disk, and fails
	// closed when the rebuild does not converge. The pre-update probe's
	// strictness was deliberately redundant: the post-update strict
	// re-verify is the trust anchor. This replaces the former
	// probe → IndexUpdate → re-verify-strict → re-verify-loose sequence
	// (3-4 subprocesses, ~2.6s) with a single subprocess.
	freshness, err := c.client.EnsureFreshIndex(ctx, req.RepositoryRoot)
	if err != nil {
		if freshness == "stale" {
			// Stale-non-converging: refuse to pass on a potentially-misleading
			// index. StatusError is the signal; the finding carries the detail.
			return "", domain.CheckResult{
				Name:   c.Name(),
				Status: domain.StatusError,
				Findings: []domain.Finding{{
					RuleID:         "architecture:index-stale",
					Severity:       domain.SeverityError,
					Category:       domain.CategoryArchitecture,
					Message:        "kern index is stale and did not converge after rebuild",
					Explanation:    "A stale index can hide architecture boundary violations. The rebuild did not produce a fresh index, indicating concurrent edits or index corruption. The check refuses to pass on a potentially-misleading index.",
					SuggestedFix:   "Run `kern index` manually, ensure no concurrent edits, then re-run blueprint.",
					RuleVersion:    "1",
					IndexFreshness: "stale",
					Confidence:     1.0,
					Scope:          "repo",
				}},
			}, true
		}
		return "", domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "kern index ensure-fresh: " + err.Error()}, true
	}
	return freshness, domain.CheckResult{}, false
}

// authzVerdict asks kern for the change's authorization verdict when it
// carries an agent identity, BEFORE the boundary check. An
// unauthorized agent's structural violations are moot — a denied verdict
// blocks the change (done=true) and skips the boundary evaluation entirely.
// Degradation contract (never a hard failure):
//   - verdict == nil (no --task scope, or kern without the authz key):
//     proceed unchanged (backward compat);
//   - err != nil (probe failed): emit an authz:verdict-error WARN and
//     proceed with the boundary check — the architecture check must not
//     hard-fail on an authz probe error;
//   - Decision == "allowed" | "unknown": proceed; the verdict is recorded in
//     provenance but does not block.
func (c *ArchitectureCheck) authzVerdict(ctx context.Context, req domain.ChangeRequest) (authzFindings []domain.Finding, result domain.CheckResult, done bool) {
	if req.AgentID == "" {
		return nil, domain.CheckResult{}, false
	}
	task := ""
	if req.Metadata != nil {
		// The authz task scope is the change intent: the --task flag
		// (authz scope) wins, falling back to --intent (approval gate).
		if t := req.Metadata["task"]; t != "" {
			task = t
		} else {
			task = req.Metadata["intent"]
		}
	}
	verdict, err := c.client.AuthzVerdict(ctx, req.RepositoryRoot, req.AgentID, task, stagedFilePaths(req))
	if err != nil {
		log.Printf("blueprint: authz verdict unavailable for agent %q: %v", req.AgentID, err)
		return []domain.Finding{{
			RuleID:       "authz:verdict-error",
			Severity:     domain.SeverityWarn,
			Category:     domain.CategoryPolicy,
			Message:      fmt.Sprintf("authorization verdict unavailable for agent %q: %v", req.AgentID, err),
			Explanation:  "The authz probe failed (kern error, contract mismatch, or malformed output). The change is evaluated against the architecture boundaries anyway; the WARN makes the missing authz gate visible.",
			SuggestedFix: "Check that kern is installed and speaks the v2 contract (upgrade kern or pin KERN_BINARY), then re-run the check.",
			RuleVersion:  "1",
			Confidence:   1.0,
			Scope:        "repo",
		}}, domain.CheckResult{}, false
	}
	if verdict != nil && verdict.Decision == "denied" {
		evidence := make([]domain.Evidence, 0, len(verdict.DeniedFiles))
		for _, f := range verdict.DeniedFiles {
			evidence = append(evidence, domain.Evidence{
				Kind:        "authz-denied-file",
				Description: "agent not authorized to modify this file",
				Location:    f,
			})
		}
		return nil, domain.CheckResult{
			Name:   c.Name(),
			Status: domain.StatusBlock,
			Findings: []domain.Finding{{
				RuleID:       "authz:unauthorized",
				Severity:     domain.SeverityBlock,
				Category:     domain.CategoryPolicy,
				Message:      fmt.Sprintf("agent %q not authorized to modify %d file(s)", verdict.AgentID, len(verdict.DeniedFiles)),
				Explanation:  "The change's agent identity is not authorized to modify the listed files. An unauthorized agent's structural violations are moot, so the boundary check was skipped.",
				SuggestedFix: "Authorize the agent for the denied files (update the agent's permissions or task scope), then re-run the check.",
				RuleVersion:  "1",
				Confidence:   1.0,
				Scope:        "repo",
				Evidence:     evidence,
			}},
		}, true
	}
	// allowed / unknown / nil verdict: fall through to the boundary check.
	return nil, domain.CheckResult{}, false
}

// buildGuardFindings maps the guard violations, the projected-import check
// (B6 — pre-write architecture check beyond disk-state: proposed new files
// that do not exist on disk yet cannot be seen by `kern guard`, so their
// imports are projected and checked against the same boundary rules; ADDITIVE
// and scoped to new files only), and any authz WARN findings (authz:
// verdict-error rides along visible but never a gate) into the check's
// finding list, then aggregates the final status: any Block finding forces
// StatusBlock; otherwise any finding at all yields StatusWarn.
func (c *ArchitectureCheck) buildGuardFindings(req domain.ChangeRequest, violations []Violation, authzFindings []domain.Finding, freshness string) domain.CheckResult {
	findings := make([]domain.Finding, 0, len(violations)+1)
	if msg := c.javaImportCoverageWarning(req); msg != "" {
		findings = append(findings, domain.Finding{
			RuleID:         "architecture:java-import-boundaries-not-enforced",
			Severity:       domain.SeverityWarn,
			Category:       domain.CategoryArchitecture,
			Message:        msg,
			Explanation:    "The guard check runs against the installed kern binary's index, which extracts Java imports (per-file import edges) in addition to call edges, so Java boundary crossings are reported; matching is precise on full package paths (see importMatches).",
			SuggestedFix:   "Boundary rules match on full package paths; a Java import only violates a rule when its package path is the exact suffix of the forbidden directory.",
			RuleVersion:    "1",
			IndexFreshness: freshness,
			Confidence:     1.0,
			Scope:          "file",
		})
	}
	for _, v := range violations {
		findings = append(findings, domain.Finding{
			RuleID:         "architecture:boundary-violation",
			Severity:       domain.SeverityBlock,
			Category:       domain.CategoryArchitecture,
			File:           v.CallerFile,
			Line:           v.Line,
			Message:        fmt.Sprintf("%s (%s) calls into %s: forbidden by boundary rule %s -> %s", v.CallerFile, v.Symbol, v.CalleeFile, v.RuleFrom, v.RuleTo),
			Explanation:    "This call crosses an architecture boundary declared in .kern/boundaries.json. The change introduces a forbidden dependency.",
			SuggestedFix:   fmt.Sprintf("Remove the dependency from %s on %s, or add an allow rule in .kern/boundaries.json.", v.RuleFrom, v.RuleTo),
			RuleVersion:    "1",
			IndexFreshness: freshness,
			Confidence:     1.0,
			Scope:          "file",
			Evidence: []domain.Evidence{{
				Kind:        "import-edge",
				Description: fmt.Sprintf("import edge %s -> %s", v.CallerFile, v.CalleeFile),
				Location:    fmt.Sprintf("%s:%d", v.CallerFile, v.Line),
			}},
		})
	}
	findings = append(findings, c.projectedImportFindings(req, freshness)...)
	findings = append(authzFindings, findings...)

	status := domain.StatusPass
	for _, f := range findings {
		if f.Severity == domain.SeverityBlock {
			status = domain.StatusBlock
			break
		}
	}
	if len(findings) > 0 && status == domain.StatusPass {
		status = domain.StatusWarn
	}
	return domain.CheckResult{Name: c.Name(), Status: status, Findings: findings}
}

// javaImportCoverageWarning returns a WARN message when the change touches
// Java files but the installed kern build cannot see Java imports. The guard
// check's import-level boundary detection depends on the kern index carrying
// package imports; older kern builds only extract imports for Go, so a
// Java-only violation would pass silently. The warning reads the freshly
// rebuilt index (IndexBuild runs before guard) and checks whether any java
// package carries imports. Returns "" when Java imports are indexed, when no
// Java files are staged, or when no boundaries are declared.
//
// WHY this parses kern's private index format: there is no supported kern
// surface for per-language import coverage — `kern index --status --json`
// exposes only counts, not whether Java import edges exist. The index layout
// is kern's internal detail, so this function is deliberately fail-closed:
// an unreadable, malformed, or structurally different index.json (schema
// drift in a future kern) degrades to "" (no warning) and never errors the
// check. A false negative (missing advisory) is acceptable; a false positive
// (blocking/warning on healthy Java) would be worse.
func (c *ArchitectureCheck) javaImportCoverageWarning(req domain.ChangeRequest) string {
	hasJava := false
	for _, f := range stagedFilePaths(req) {
		if strings.HasSuffix(strings.ToLower(f), ".java") {
			hasJava = true
			break
		}
	}
	if !hasJava {
		return ""
	}
	rules, err := os.ReadFile(filepath.Join(req.RepositoryRoot, ".kern", "boundaries.json"))
	if err != nil || !bytes.Contains(rules, []byte(`"action"`)) {
		return "" // no rules declared -> nothing to enforce
	}
	raw, err := os.ReadFile(filepath.Join(req.RepositoryRoot, ".kern", "index.json"))
	if err != nil {
		return ""
	}
	var idx struct {
		Packages map[string]struct {
			Lang    string   `json:"lang"`
			Imports []string `json:"imports"`
		} `json:"packages"`
	}
	if json.Unmarshal(raw, &idx) != nil || idx.Packages == nil {
		return ""
	}
	javaPkgs, withImports := 0, 0
	for _, p := range idx.Packages {
		if p.Lang == "java" {
			javaPkgs++
			if len(p.Imports) > 0 {
				withImports++
			}
		}
	}
	if javaPkgs > 0 && withImports == 0 {
		return "Java import boundaries are not enforced: the installed kern build does not extract Java import edges, so forbidden Java imports would pass silently. Upgrade kern to a build with Java import support."
	}
	return ""
}

// stagedFilePaths extracts the relative file paths from a ChangeRequest's
// Files slice, for passing to kern guard check --file.
func stagedFilePaths(req domain.ChangeRequest) []string {
	files := make([]string, 0, len(req.Files))
	for _, f := range req.Files {
		if f.Op == domain.OpDelete {
			continue // deleted files have no symbols to check
		}
		files = append(files, f.Path)
	}
	return files
}

// gitTrackedFiles returns all files tracked by git in the repo root, via
// `git ls-files`. Used by strict-baseline mode to check the entire repo
// (not just working-tree-changed files). Non-go files are filtered out
// because kern's boundary check only applies to indexed source files.
func gitTrackedFiles(repoRoot string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "ls-files")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Only include source files kern can index (go, ts, tsx, js, py, rb, java).
		// This avoids passing test files and non-source files to guard check.
		if isIndexableSource(line) {
			files = append(files, line)
		}
	}
	return files, nil
}

// isIndexableSource reports whether a path is a source file kern can index
// (and thus a candidate for boundary checks). Test files are excluded because
// kern's CheckBoundaries skips them (guard.go: isTestFile).
func isIndexableSource(path string) bool {
	if isTestFilePath(path) {
		return false
	}
	lower := strings.ToLower(path)
	for _, suffix := range []string{".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rb", ".java"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// isTestFilePath reports whether a path looks like a test file. Mirrors
// kern's isTestFile heuristic for the common conventions.
func isTestFilePath(rel string) bool {
	lower := strings.ToLower(rel)
	base := strings.ToLower(filepath.Base(rel))
	if strings.HasSuffix(lower, "_test.go") {
		return true
	}
	for _, suffix := range []string{".test.ts", ".test.tsx", ".test.js", ".test.jsx", "_spec.rb", "_spec.js"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(lower, ".py") {
		return true
	}
	return false
}

// --- Projected import check (B6) ---

// boundaryRule mirrors kern's intel.BoundaryRule: one allowed or forbidden
// dependency edge between two directory/package patterns.
type boundaryRule struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Action string `json:"action"`
}

// loadBoundaries reads .kern/boundaries.json into its rule set. A missing or
// unreadable file yields an error; the caller treats that as "no rules" (the
// missing-.kern case is already surfaced earlier in Run, and kern's own
// LoadBoundaries yields an empty rule set for a missing file).
func loadBoundaries(path string) ([]boundaryRule, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Rules []boundaryRule `json:"rules"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc.Rules, nil
}

// projectedImportFindings implements the pre-write (projected) import check:
// for every file in the change that is NOT yet on disk (a proposed new file
// carrying content), it extracts the imports from the proposed content and
// flags any import that crosses a forbidden boundary. It mirrors kern's guard
// import-level check (internal/intel/guard.go): a file in directory fromDir
// that imports a local directory toDir violates when a rule forbids
// fromDir -> toDir. Candidate target directories come from the repo's own
// source tree (kern resolves imports against its index; without reindexing the
// proposed file, the on-disk tree is the closest safe approximation, so an
// external package whose path merely ends in a forbidden layer name is not
// flagged).
//
// Returns nil (no findings) when there are no proposed new files, no
// declarable rules, or no matching violations. Never returns an error: a
// projected check that cannot evaluate degrades to no finding, and the
// on-disk guard check remains the source of truth for what kern can see.
func (c *ArchitectureCheck) projectedImportFindings(req domain.ChangeRequest, freshness string) []domain.Finding {
	if req.RepositoryRoot == "" {
		return nil
	}
	rules, err := loadBoundaries(filepath.Join(req.RepositoryRoot, ".kern", "boundaries.json"))
	if err != nil || len(rules) == 0 {
		return nil // no declarable rules -> nothing to project
	}

	// Only proposed new files need the projected check: files already on disk
	// are fully evaluated by `kern guard` above. A new file carries its
	// proposed content (FileChange.Content) and does not exist at its path yet.
	var proposed []domain.FileChange
	for _, f := range req.Files {
		if f.Content == "" {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(req.RepositoryRoot, f.Path)); statErr == nil {
			continue // already on disk -> kern guard covers it
		}
		proposed = append(proposed, f)
	}
	if len(proposed) == 0 {
		return nil
	}

	// Candidate target directories: every local directory holding indexable
	// source (kern's indexDirs, read from disk instead of the index).
	targetDirs := sourceDirs(req.RepositoryRoot)

	var findings []domain.Finding
	for _, f := range proposed {
		fromDir := filepath.Dir(filepath.Clean(f.Path))
		for _, imp := range extractImports(f.Path, f.Content) {
			for toDir := range targetDirs {
				if toDir == fromDir {
					continue
				}
				if !importMatches(imp, toDir) {
					continue
				}
				rule := forbidVerdict(rules, fromDir, toDir)
				if rule == nil {
					continue
				}
				findings = append(findings, domain.Finding{
					RuleID:         "architecture:projected-import-violation",
					Severity:       domain.SeverityBlock,
					Category:       domain.CategoryArchitecture,
					File:           f.Path,
					Message:        fmt.Sprintf("%s imports %s: forbidden by boundary rule %s -> %s (projected pre-write check)", f.Path, imp, rule.From, rule.To),
					Explanation:    "This change proposes a NEW file that does not exist on disk yet, so `kern guard` cannot see its imports or calls (the index has no entry for it). The import was checked against the declared boundaries directly from the proposed content before the file is written — the most common architecture breach (importing a forbidden package) is caught pre-write. This is import-level projection: call edges inside the proposed file are not resolved until the file is written and the index is rebuilt.",
					SuggestedFix:   fmt.Sprintf("Remove the import of %s from %s, or add an allow rule in .kern/boundaries.json.", imp, f.Path),
					RuleVersion:    "1",
					IndexFreshness: freshness,
					Confidence:     0.9,
					Scope:          "file",
					Evidence: []domain.Evidence{{
						Kind:        "projected-import-edge",
						Description: fmt.Sprintf("proposed file %s imports %s (target directory %s)", f.Path, imp, toDir),
						Location:    f.Path,
					}},
				})
			}
		}
	}
	return findings
}

// sourceDirs returns the set of relative directories that hold indexable
// source files, mirroring kern's indexDirs but read from disk (the projected
// check has no index entry for the proposed file yet). Hidden and vendor
// trees are skipped.
func sourceDirs(root string) map[string]bool {
	dirs := map[string]bool{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == ".kern" || name == "vendor" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || !isIndexableSource(rel) {
			return nil
		}
		if d := filepath.Dir(rel); d != "." && d != "" {
			dirs[d] = true
		}
		return nil
	})
	return dirs
}

// importMatches reports whether an import path refers to a local directory,
// mirroring kern's internal/intel/guard.go importMatches.
func importMatches(importPath, dir string) bool {
	if importPath == "" {
		return false
	}
	if strings.HasSuffix(importPath, "/"+dir) || importPath == dir {
		return true
	}
	return strings.HasSuffix(importPath, "/"+filepath.Base(dir))
}

// dirMatch matches a rule pattern against a directory: exact, "pattern/…"
// prefix, or "…/pattern" suffix (kern's internal/intel/guard.go dirMatch).
func dirMatch(pattern, dir string) bool {
	if pattern == "" {
		return false
	}
	return dir == pattern ||
		strings.HasPrefix(dir, pattern+"/") ||
		strings.HasSuffix(dir, "/"+pattern)
}

// normalizeGlob reduces a self-or-descendant glob ("web/**", "web/...") to its
// base so dirMatch can evaluate it (kern's normalizeGlob).
func normalizeGlob(pattern string) string {
	if strings.HasSuffix(pattern, "/**") {
		return strings.TrimSuffix(pattern, "/**")
	}
	if strings.HasSuffix(pattern, "/...") {
		return strings.TrimSuffix(pattern, "/...")
	}
	return pattern
}

// forbidVerdict returns the forbidding rule for the fromDir -> toDir edge, or
// nil when the edge is permitted. Mirrors kern's verdict: an allow rule
// dominates any forbid rule for the same pair, order-invariant.
func forbidVerdict(rules []boundaryRule, fromDir, toDir string) *boundaryRule {
	for i := range rules {
		if rules[i].Action == "allow" && dirMatch(normalizeGlob(rules[i].From), fromDir) && dirMatch(normalizeGlob(rules[i].To), toDir) {
			return nil
		}
	}
	for i := range rules {
		if rules[i].Action == "forbid" && dirMatch(normalizeGlob(rules[i].From), fromDir) && dirMatch(normalizeGlob(rules[i].To), toDir) {
			return &rules[i]
		}
	}
	return nil
}

// Import-extraction regexes. These capture the same import signal kern's index
// records (ImportsByFile), computed from the proposed text so the check works
// before the file exists on disk.
var (
	goImportBlockRe  = regexp.MustCompile(`import\s*\(([^)]*)\)`)
	goImportSingleRe = regexp.MustCompile(`import\s+(?:[A-Za-z0-9_]+\.?\s*)?"([^"]+)"`)
	javaImportRe     = regexp.MustCompile(`(?m)^\s*import\s+(?:static\s+)?([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*)(?:\.\*)?;`)
	pyImportRe       = regexp.MustCompile(`(?m)^\s*import\s+([A-Za-z0-9_.]+)`)
	pyFromRe         = regexp.MustCompile(`(?m)^\s*from\s+([A-Za-z0-9_.]+)\s+import`)
	rubyRequireRe    = regexp.MustCompile(`require\s*\(?["']([^"']+)["']`)
	tsFromRe         = regexp.MustCompile(`import\s+[^"']*?\s+from\s+["']([^"']+)["']`)
	tsRequireRe      = regexp.MustCompile(`require\s*\(["']([^"']+)["']\)`)
	tsSideEffectRe   = regexp.MustCompile(`(?m)^\s*import\s+["']([^"']+)["']`)
	quotedStringRe   = regexp.MustCompile(`"([^"]+)"`)
)

// extractImports pulls the import/require statements out of proposed source
// content, language-aware by file extension. Java FQCNs are converted to slash
// form ("com.example.db.X" -> "com/example/db/X") so importMatches can compare
// against directories; every other language keeps its natural path form.
func extractImports(path, content string) []string {
	lower := strings.ToLower(path)
	imports := []string{}
	seen := map[string]bool{}
	add := func(imp string) {
		imp = strings.TrimSpace(imp)
		if imp == "" || seen[imp] {
			return
		}
		seen[imp] = true
		imports = append(imports, imp)
	}
	quoted := func(s string) []string {
		var out []string
		for _, m := range quotedStringRe.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}
		return out
	}

	switch {
	case strings.HasSuffix(lower, ".go"):
		for _, m := range goImportBlockRe.FindAllStringSubmatch(content, -1) {
			for _, imp := range quoted(m[1]) {
				add(imp)
			}
		}
		for _, m := range goImportSingleRe.FindAllStringSubmatch(content, -1) {
			add(m[1])
		}
	case strings.HasSuffix(lower, ".java"):
		for _, m := range javaImportRe.FindAllStringSubmatch(content, -1) {
			add(strings.ReplaceAll(m[1], ".", "/"))
		}
	case strings.HasSuffix(lower, ".py"):
		for _, m := range pyImportRe.FindAllStringSubmatch(content, -1) {
			add(m[1])
		}
		for _, m := range pyFromRe.FindAllStringSubmatch(content, -1) {
			add(m[1])
		}
	case strings.HasSuffix(lower, ".rb"):
		for _, m := range rubyRequireRe.FindAllStringSubmatch(content, -1) {
			add(m[1])
		}
	case strings.HasSuffix(lower, ".ts"), strings.HasSuffix(lower, ".tsx"),
		strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".jsx"):
		for _, m := range tsFromRe.FindAllStringSubmatch(content, -1) {
			add(m[1])
		}
		for _, m := range tsRequireRe.FindAllStringSubmatch(content, -1) {
			add(m[1])
		}
		for _, m := range tsSideEffectRe.FindAllStringSubmatch(content, -1) {
			add(m[1])
		}
	}
	return imports
}
