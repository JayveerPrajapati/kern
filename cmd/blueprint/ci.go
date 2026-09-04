package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/gitleaks"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/jscpd"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/metrics"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/receipt"
	"github.com/JayveerPrajapati/kern/internal/blueprint/sandbox"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
	blueprintversion "github.com/JayveerPrajapati/kern/internal/blueprint/version"
)

// ciFlags carries the parsed `blueprint ci` command-line flags.
type ciFlags struct {
	repoRoot      string
	baseRef       string
	headRef       string
	jsonOut       bool
	artifactFile  string
	noHuman       bool
	strictLatency bool
	noCache       bool
	receiptFlag   bool
}

// parseCIFlags parses the `blueprint ci` flags. It returns the parsed flags
// and an exit code: 0 means ready to run, 2 means a usage error was printed,
// and exitFlagHelp means -h/--help was requested (the flag package has
// already rendered usage, so the caller exits 0 rather than treating help as
// a parse error).
func parseCIFlags(args []string) (ciFlags, int) {
	fs := flag.NewFlagSet("ci", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoRoot := fs.String("repo", "", "repository root (default: current directory)")
	baseRef := fs.String("base", "main", "base revision (branch/tag/sha)")
	headRef := fs.String("head", "HEAD", "proposed revision (branch/tag/sha)")
	jsonOut := fs.Bool("json", false, "emit JSON artifact (always emitted to --artifact-file regardless)")
	artifactFile := fs.String("artifact-file", "blueprint-result.json", "path to write JSON artifact")
	noHuman := fs.Bool("no-human", false, "suppress human-readable summary on stderr")
	strictLatency := fs.Bool("strict-latency", false, "treat the WARN-only latency budget finding as a hard failure (exit 1)")
	noCache := fs.Bool("no-cache", false, "bypass the verdict cache and force a full re-validation (BLUEPRINT_NO_CACHE=1 also works)")
	receiptFlag := fs.Bool("receipt", true, "generate a tamper-evident receipt for PASS/WARN results (default: true)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ciFlags{}, exitFlagHelp // -h/--help: usage already printed
		}
		return ciFlags{}, 2
	}
	return ciFlags{
		repoRoot:      *repoRoot,
		baseRef:       *baseRef,
		headRef:       *headRef,
		jsonOut:       *jsonOut,
		artifactFile:  *artifactFile,
		noHuman:       *noHuman,
		strictLatency: *strictLatency,
		noCache:       *noCache,
		receiptFlag:   *receiptFlag,
	}, 0
}

// runCI implements `blueprint ci` — the CI/protected-branch enforcement
// command (spec Phase 11). It runs validation against a PR-like change (base
// vs proposed revision) and emits both a JSON artifact and a human-readable
// summary.
//
// CI must NOT depend on developer-local daemon state (spec line 1397). It
// reconstructs the validation context from:
//   - repository checkout (--repo)
//   - base revision (--base, default: main)
//   - proposed revision (--head, default: HEAD)
//   - configuration (.blueprint/config.yaml or defaults)
//   - deterministic kern index (built inside the validation worktree, CI-clean)
//
// Exit codes follow the spec Section 6 contract:
//
//	0 = PASS, 1 = BLOCK, 2 = ERROR, 3 = config error
func runCI(args []string) int {
	fl, code := parseCIFlags(args)
	if code != 0 {
		if code == exitFlagHelp {
			return 0
		}
		return code
	}

	absRoot, code := resolveRepoRoot(fl.repoRoot)
	if code != 0 {
		return code
	}

	start := time.Now()
	artifact := CIArtifact{
		Repo:    absRoot,
		Base:    fl.baseRef,
		Head:    fl.headRef,
		StartAt: start.UTC().Format(time.RFC3339),
	}

	// Step 1: Verify the base and head revisions exist.
	if code := verifyRevisions(absRoot, fl.baseRef, fl.headRef, &artifact, fl.artifactFile, fl.noHuman, fl.jsonOut); code != 0 {
		return code
	}

	// Step 2: Discover changed files between base and head.
	changes, code := discoverCIDiff(absRoot, fl.baseRef, fl.headRef, &artifact, fl.artifactFile, fl.noHuman, fl.jsonOut)
	if code != 0 {
		return code
	}
	artifact.FilesChanged = len(changes)

	// Step 2b: When the head ref is not HEAD, run the whole validation against
	// a throwaway detached worktree at that ref (see validateInWorktree).
	valRoot := absRoot
	if fl.headRef != "HEAD" {
		wtRoot, cleanup, err := validateInWorktree(absRoot, fl.headRef)
		if err != nil {
			artifact.Error = err.Error()
			emitCIResult(artifact, fl.artifactFile, fl.noHuman, fl.jsonOut)
			return 2
		}
		defer cleanup()
		valRoot = wtRoot
	}

	// Step 3: Load configuration (must be from the proposed revision's tree).
	cfg, code := loadCIConfig(valRoot, &artifact, fl.artifactFile, fl.noHuman, fl.jsonOut)
	if code != 0 {
		return code
	}

	// Step 3b: Verdict cache (keyless replay): byte-identical validation inputs
	// replay the prior verdict instead of re-running the full scan (see
	// replayOrMiss). BLUEPRINT_NO_CACHE=1 or --no-cache bypasses read+write.
	cacheDir := filepath.Join(absRoot, ".blueprint", "verdict-cache")
	bypassCache := fl.noCache || os.Getenv("BLUEPRINT_NO_CACHE") == "1"
	cacheKey, kernVersion, client, hit, hitResult := replayOrMiss(cacheDir, bypassCache,
		absRoot, valRoot, fl.baseRef, fl.headRef, changes, fl.jsonOut, fl.noHuman, fl.strictLatency, fl.artifactFile, start, &artifact, cfg)
	if hit {
		return ciExitCode(hitResult, fl.strictLatency)
	}

	// Step 4: Create the kern client, reusing the one from the cache check when
	// it was already created for a version probe (see ensureKernClient).
	client, kernVersion, code = ensureKernClient(client, kernVersion, &artifact, fl.artifactFile, fl.noHuman, fl.jsonOut)
	if code != 0 {
		return code
	}

	// Step 5: Run the validation pipeline (same engine as local).
	result, auditWriter := runValidationPipeline(valRoot, absRoot, changes, client, kernVersion, cfg)

	// Persist the verdict for keyless replay on identical future runs (tool
	// failures are never cached; see persistVerdict).
	persistVerdict(bypassCache, kernVersion, cacheDir, cacheKey, result)

	// Step 6: Emit artifact + summary.
	finishCIArtifact(&artifact, result, cfg, start, cacheKey)
	emitCIResult(artifact, fl.artifactFile, fl.noHuman, fl.jsonOut)

	// Step 7: Tamper-evident receipt (P1.4) for PASS/WARN validations only
	// (see sealReceipt). Best-effort: a failed receipt write must never fail CI.
	sealReceipt(fl.receiptFlag, result, absRoot, fl.baseRef, fl.headRef, auditWriter)

	return ciExitCode(result, fl.strictLatency)
}

// runValidationPipeline assembles and runs the same validation engine as the
// local `blueprint check`, against the validation root (the worktree when the
// head ref is not HEAD). The audit trail lives in the real repository
// (.blueprint/audit/), not the throwaway worktree, so CI audit records
// persist and P1.4 receipts stay verifiable after the worktree is cleaned up;
// the returned writer's chain endpoints (LastHash / LastKernChainHash) feed
// the receipt.
func runValidationPipeline(valRoot, absRoot string, changes []domain.FileChange, client *kern.KernClient, kernVersion string, cfg *policy.LoadedConfig) (domain.ValidationResult, *audit.Writer) {
	req := domain.ChangeRequest{
		RepositoryRoot: valRoot,
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          changes,
		// P0.4 authz: CI is a known non-human source; its identity is "ci".
		AgentID: "ci",
	}
	archCheck := kern.NewArchitectureCheck(client)
	// T2.1: detection delegated to incumbents (gitleaks, jscpd); each falls
	// back to its in-house check (flagged with a WARN) when the binary is
	// absent.
	secretCheck := gitleaks.NewCheck(client)
	dupCheck := jscpd.NewCheck(client)
	// CI defaults to network isolation ON where the platform supports it
	// (Linux CLONE_NEWNET). Requesting isolation on an unsupported platform
	// (macOS/Windows) would emit a sandbox:network-isolation-unavailable WARN
	// finding on every run — noise that breaks the clean-PR zero-findings
	// contract. Gate the request on platform support so CI stays deterministic;
	// callers who want the explicit finding signal can opt in directly.
	var sandboxCheck service.Check
	if sandbox.NetworkIsolationAvailable() {
		sandboxCheck = sandbox.NewDefaultCheck(sandbox.WithNetworkIsolation())
	} else {
		sandboxCheck = sandbox.NewDefaultCheck()
	}
	// The audit trail lives in the real repository (.blueprint/audit/), not
	// the throwaway worktree, so CI audit records persist and P1.4 receipts
	// stay verifiable after the worktree is cleaned up. The records still
	// carry req.RepositoryRoot (the tree that was actually scanned) as their
	// repo_root.
	auditWriter := audit.NewWriter(filepath.Join(absRoot, ".blueprint", "audit", "audit.jsonl"))
	svc := service.New([]service.Check{archCheck, secretCheck, dupCheck, sandboxCheck},
		service.WithConfig(cfg.Service),
		service.WithKernVersion(kernVersion),
		service.WithAudit(auditWriter))
	if m, err := metrics.Load(metrics.DefaultPath(absRoot)); err == nil {
		svc = service.New([]service.Check{archCheck, secretCheck, dupCheck, sandboxCheck},
			service.WithConfig(cfg.Service),
			service.WithKernVersion(kernVersion),
			service.WithMetrics(m, metrics.DefaultPath(absRoot)),
			service.WithAudit(auditWriter),
		)
	}
	return svc.Validate(context.Background(), req), auditWriter
}

// replayOrMiss consults the keyless verdict cache. When the validation inputs
// (staged file set + content hashes, policy config, kern version, blueprint
// version, check flags) are byte-identical to a previously-validated run, it
// replays the prior verdict: the artifact is filled and emitted, and the hit
// (with the replayed result) is returned so the caller can compute the exit
// code. A miss is always a full, fresh validation — the cache is an
// optimization, never a correctness dependency.
//
// The kern version is probed once per workspace and fingerprinted in
// meta.json (binary path + size + mtime): a cache hit therefore performs zero
// kern subprocess invocations. If the kern binary changes, the fingerprint no
// longer matches and the version is re-probed, which changes the key and
// forces a miss. BLUEPRINT_NO_CACHE=1 or --no-cache bypasses both the read
// and the write. On a miss, the kern client created for the version probe (if
// any) is returned for reuse by the fresh-validation path below.
func replayOrMiss(cacheDir string, bypassCache bool, absRoot, valRoot, baseRef, headRef string, changes []domain.FileChange, jsonOut, noHuman, strictLatency bool, artifactFile string, start time.Time, artifact *CIArtifact, cfg *policy.LoadedConfig) (cacheKey, kernVersion string, client *kern.KernClient, hit bool, result domain.ValidationResult) {
	if bypassCache {
		return "", "", nil, false, domain.ValidationResult{}
	}
	baseSHA := resolveRefSHA(absRoot, baseRef)
	headSHA := resolveRefSHA(absRoot, headRef)
	fileHash := computeFileSetHash(absRoot, valRoot, baseSHA, headSHA, changes)
	policyHash := computePolicyHash(valRoot)
	flagsHash := computeFlagsHash(jsonOut, noHuman, strictLatency)
	if v, ok := cachedKernVersion(cacheDir); ok {
		kernVersion = v
	} else {
		// Reuse this client for validation below (miss path).
		var err error
		client, err = kern.NewKernClient()
		if err == nil {
			kernVersion, _ = client.Version()
		}
	}
	cacheKey = computeVerdictKey(repoIdentity(absRoot), baseSHA, headSHA,
		fileHash, policyHash, kernVersion, blueprintversion.Version, flagsHash)
	if kernVersion != "" {
		if entry, ok := loadVerdictEntry(cacheDir, cacheKey); ok {
			replayed := entry.Result
			artifact.CacheStatus = "hit"
			artifact.CacheKey = cacheKey
			artifact.DurationMs = time.Since(start).Milliseconds()
			fillCIArtifact(artifact, replayed, cfg.Service.StagedLatencyBudgetMs)
			emitCIResult(*artifact, artifactFile, noHuman, jsonOut)
			return cacheKey, kernVersion, client, true, replayed
		}
	}
	return cacheKey, kernVersion, client, false, domain.ValidationResult{}
}

// validateInWorktree materializes the proposed revision in a throwaway
// detached git worktree so validation never mutates the user's working tree
// (no checkout of the real repo). It returns the worktree path to validate
// against and a cleanup function that best-effort removes the worktree and
// its directory when the caller is done.
func validateInWorktree(repoRoot, head string) (valRoot string, cleanup func(), err error) {
	wtDir, err := os.MkdirTemp("", "blueprint-ci-")
	if err != nil {
		return "", nil, fmt.Errorf("cannot create worktree dir: %w", err)
	}
	// git worktree add requires a non-existing (or empty) path.
	os.RemoveAll(wtDir)
	cmd := exec.Command("git", "-C", repoRoot, "worktree", "add", "--detach", wtDir, head)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(wtDir)
		return "", nil, fmt.Errorf("cannot create worktree for head %s: %v", head, strings.TrimSpace(string(out)))
	}
	// Best-effort cleanup: remove the throwaway worktree and its directory.
	cleanup = func() {
		_ = exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", wtDir).Run()
		_ = os.RemoveAll(wtDir)
	}
	return wtDir, cleanup, nil
}

// sealReceipt generates and saves the tamper-evident receipt (P1.4) for a
// PASS/WARN validation. The receipt binds the validation hash, the local
// audit-chain endpoint (LastHash), and kern's chain hash (when linked); it is
// the merge-time enforcement artifact verified by `blueprint verify-receipt`.
// Only successful validations get a receipt — a failed validation is its own
// evidence and must not be sealed. Best-effort: a failed receipt write must
// never fail CI.
func sealReceipt(enabled bool, result domain.ValidationResult, absRoot, baseRef, headRef string, auditWriter *audit.Writer) {
	if !enabled || (result.Status != domain.StatusPass && result.Status != domain.StatusWarn) {
		return
	}
	rec := receipt.Generate(result, absRoot, baseRef, headRef, auditWriter.LastHash(), auditWriter.LastKernChainHash())
	if err := receipt.NewStore(absRoot).Save(rec); err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: warning: cannot save receipt: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "Receipt %s generated at .blueprint/receipts/%s.json. Verify with: blueprint verify-receipt %s\n", rec.ReceiptID, rec.ReceiptID, rec.ReceiptID)
	}
}

// verifyRevisions checks that the base and head revisions exist before any
// diffing. On failure it records the error on the artifact, emits it, and
// returns exit code 2.
func verifyRevisions(absRoot, baseRef, headRef string, artifact *CIArtifact, artifactFile string, noHuman, jsonOut bool) int {
	if err := verifyRef(absRoot, baseRef); err != nil {
		artifact.Error = fmt.Sprintf("base revision %s not found: %v", baseRef, err)
		emitCIResult(*artifact, artifactFile, noHuman, jsonOut)
		return 2
	}
	if err := verifyRef(absRoot, headRef); err != nil {
		artifact.Error = fmt.Sprintf("head revision %s not found: %v", headRef, err)
		emitCIResult(*artifact, artifactFile, noHuman, jsonOut)
		return 2
	}
	return 0
}

// discoverCIDiff returns the changed files between the base and head
// revisions. On failure it records the error on the artifact, emits it, and
// returns exit code 2.
func discoverCIDiff(absRoot, baseRef, headRef string, artifact *CIArtifact, artifactFile string, noHuman, jsonOut bool) ([]domain.FileChange, int) {
	changes, err := discoverDiffChanges(absRoot, baseRef, headRef)
	if err != nil {
		artifact.Error = fmt.Sprintf("cannot diff %s..%s: %v", baseRef, headRef, err)
		emitCIResult(*artifact, artifactFile, noHuman, jsonOut)
		return nil, 2
	}
	return changes, 0
}

// loadCIConfig loads the Blueprint configuration from the proposed revision's
// tree. On failure it records the config error on the artifact and returns
// exit code 3 (config error contract).
func loadCIConfig(valRoot string, artifact *CIArtifact, artifactFile string, noHuman, jsonOut bool) (*policy.LoadedConfig, int) {
	cfg, err := policy.Load(valRoot)
	if err != nil {
		artifact.Error = fmt.Sprintf("invalid configuration: %v", err)
		artifact.Status = "ERROR"
		emitCIResult(*artifact, artifactFile, noHuman, jsonOut)
		return nil, 3
	}
	return cfg, 0
}

// ensureKernClient creates the kern client when the cache check did not
// already create one (it is reused when it was created for a version probe).
// The index is built by ArchitectureCheck.Run inside the validation root: the
// worktree has no .kern/, so it builds fresh — CI-clean, no local cache.
// P2-4: the kern version is probed once for provenance stamping — best-effort,
// an empty string on probe failure must never fail validation. On failure it
// records the error on the artifact and returns exit code 2.
func ensureKernClient(client *kern.KernClient, kernVersion string, artifact *CIArtifact, artifactFile string, noHuman, jsonOut bool) (*kern.KernClient, string, int) {
	if client == nil {
		var err error
		client, err = kern.NewKernClient()
		if err != nil {
			artifact.Error = fmt.Sprintf("kern binary not found: %v", err)
			artifact.Status = "ERROR"
			emitCIResult(*artifact, artifactFile, noHuman, jsonOut)
			return nil, "", 2
		}
	}
	if kernVersion == "" {
		kernVersion, _ = client.Version()
	}
	return client, kernVersion, 0
}

// persistVerdict writes the validated verdict to the keyless replay cache so
// identical future runs replay it instead of re-scanning. Tool failures
// (StatusError) are never cached: they may be transient, and the key already
// includes the kern version so a real upgrade invalidates.
func persistVerdict(bypassCache bool, kernVersion, cacheDir, cacheKey string, result domain.ValidationResult) {
	if !bypassCache && kernVersion != "" && result.Status != domain.StatusError {
		writeVerdictEntry(cacheDir, cacheKey, result)
		writeKernVersionMeta(cacheDir, kernVersion)
	}
}

// finishCIArtifact stamps the artifact with the run's duration, verdict
// fields, and cache key (when one was computed) so a fresh validation emits
// the same artifact shape as a cache-hit replay.
func finishCIArtifact(artifact *CIArtifact, result domain.ValidationResult, cfg *policy.LoadedConfig, start time.Time, cacheKey string) {
	artifact.DurationMs = time.Since(start).Milliseconds()
	fillCIArtifact(artifact, result, cfg.Service.StagedLatencyBudgetMs)
	if cacheKey != "" {
		artifact.CacheKey = cacheKey
	}
}

// fillCIArtifact converts a ValidationResult into the artifact's verdict
// fields (status, exit code, findings, per-check breakdown, latency budget).
// Shared by the fresh-validation and cache-hit paths so a replayed verdict
// produces an identical artifact to a real run.
func fillCIArtifact(artifact *CIArtifact, result domain.ValidationResult, latencyBudgetMs int) {
	artifact.Status = string(result.Status)
	artifact.ExitCode = result.ExitCode
	artifact.FindingsCount = len(result.Findings)
	artifact.LatencyBudgetMs = latencyBudgetMs

	// Convert findings to artifact format.
	for _, f := range result.Findings {
		artifact.Findings = append(artifact.Findings, CIFinding{
			RuleID:         f.RuleID,
			Severity:       string(f.Severity),
			Category:       string(f.Category),
			File:           f.File,
			Line:           f.Line,
			Message:        f.Message,
			Explanation:    f.Explanation,
			SuggestedFix:   f.SuggestedFix,
			Redacted:       f.Redacted,
			RuleVersion:    f.RuleVersion,
			KernVersion:    f.KernVersion,
			IndexFreshness: f.IndexFreshness,
			Confidence:     f.Confidence,
			Scope:          f.Scope,
		})
	}

	// P2-3: per-check breakdown (name/status/duration_ms) so the sec-cache and
	// batched git diff paths are measurable in CI.
	for _, cr := range result.Checks {
		artifact.Checks = append(artifact.Checks, CICheck{
			Name:          cr.Name,
			Status:        string(cr.Status),
			DurationMs:    cr.Duration,
			FindingsCount: len(cr.Findings),
		})
	}
}

// ciExitCode maps a ValidationResult to the CI exit code (spec Section 6):
// 0 = PASS, 1 = BLOCK, 2 = ERROR. --strict-latency upgrades the WARN-only
// latency finding to a hard CI failure (exit 1) so the performance budget can
// gate merges; BLOCK and ERROR keep their precedence above (BLOCK would be 1
// anyway; ERROR stays 2 — a tool failure must not be masked as a budget miss).
func ciExitCode(result domain.ValidationResult, strictLatency bool) int {
	switch result.Status {
	case domain.StatusBlock:
		return 1
	case domain.StatusError:
		return 2
	}
	if strictLatency && hasLatencyFinding(result.Findings) {
		return 1
	}
	return 0
}

// hasLatencyFinding reports whether the result carries the P2-3 performance
// latency-budget finding (used by `blueprint ci --strict-latency`).
func hasLatencyFinding(findings []domain.Finding) bool {
	for _, f := range findings {
		if f.RuleID == "performance:latency-budget" {
			return true
		}
	}
	return false
}

// CIArtifact is the JSON artifact written to disk for CI systems to consume.
type CIArtifact struct {
	Repo            string      `json:"repo"`
	Base            string      `json:"base"`
	Head            string      `json:"head"`
	Status          string      `json:"status"`
	ExitCode        int         `json:"exit_code"`
	FilesChanged    int         `json:"files_changed"`
	FindingsCount   int         `json:"findings_count"`
	DurationMs      int64       `json:"duration_ms"`
	StartAt         string      `json:"start_at"`
	Findings        []CIFinding `json:"findings,omitempty"`
	Checks          []CICheck   `json:"checks,omitempty"`
	LatencyBudgetMs int         `json:"latency_budget_ms,omitempty"`
	Error           string      `json:"error,omitempty"`
	// CacheStatus is "hit" when this artifact was replayed from the verdict
	// cache (keyless CI replay) instead of a fresh validation; empty otherwise.
	CacheStatus string `json:"cache_status,omitempty"`
	// CacheKey is the SHA-256 input fingerprint that addressed this verdict.
	CacheKey string `json:"cache_key,omitempty"`
}

// CICheck is one check's breakdown in the CI artifact (P2-3): name, enforced
// status, wall duration, and finding count, so per-check latency is
// measurable in CI.
type CICheck struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	DurationMs    int64  `json:"duration_ms"`
	FindingsCount int    `json:"findings_count"`
}

// CIFinding is a single finding in the CI artifact.
type CIFinding struct {
	RuleID       string `json:"rule_id"`
	Severity     string `json:"severity"`
	Category     string `json:"category"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	Message      string `json:"message"`
	Explanation  string `json:"explanation"`
	SuggestedFix string `json:"suggested_fix"`
	Redacted     bool   `json:"redacted"`

	// Kern 2.0 Evidence provenance (P2-4): mirrors domain.Finding's additive
	// provenance fields so the CI artifact carries the same shared-findings
	// format. All omitempty — zero values stay absent.
	RuleVersion    string  `json:"rule_version,omitempty"`
	KernVersion    string  `json:"kern_version,omitempty"`
	IndexFreshness string  `json:"index_freshness,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	Scope          string  `json:"scope,omitempty"`
}

// emitCIResult writes the JSON artifact to file and optionally prints a
// human-readable summary to stderr.
func emitCIResult(artifact CIArtifact, artifactFile string, noHuman bool, jsonToStdout bool) {
	// Always write the JSON artifact file.
	b, _ := json.MarshalIndent(artifact, "", "  ")
	os.WriteFile(artifactFile, b, 0o644)

	// Optionally emit JSON to stdout.
	if jsonToStdout {
		fmt.Println(string(b))
	}

	// Human-readable summary on stderr (unless suppressed).
	if !noHuman {
		fmt.Fprintln(os.Stderr, "━━━ Blueprint CI ━━━")
		fmt.Fprintf(os.Stderr, "Status:        %s\n", artifact.Status)
		fmt.Fprintf(os.Stderr, "Base:          %s\n", artifact.Base)
		fmt.Fprintf(os.Stderr, "Head:          %s\n", artifact.Head)
		fmt.Fprintf(os.Stderr, "Files changed: %d\n", artifact.FilesChanged)
		fmt.Fprintf(os.Stderr, "Findings:      %d\n", artifact.FindingsCount)
		fmt.Fprintf(os.Stderr, "Duration:      %dms\n", artifact.DurationMs)
		if artifact.CacheStatus != "" {
			fmt.Fprintf(os.Stderr, "Cache:         %s\n", artifact.CacheStatus)
		}
		if artifact.Error != "" {
			fmt.Fprintf(os.Stderr, "Error:         %s\n", artifact.Error)
		}
		for _, f := range artifact.Findings {
			fmt.Fprintf(os.Stderr, "  • %s [%s] %s:%d — %s\n", f.Severity, f.RuleID, f.File, f.Line, f.Message)
		}
		fmt.Fprintf(os.Stderr, "Artifact:      %s\n", artifactFile)
	}
}

// verifyRef checks that a git ref exists in the repo.
func verifyRef(repoRoot, ref string) error {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", ref)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// discoverDiffChanges returns the files changed between base and head revisions.
func discoverDiffChanges(repoRoot, base, head string) ([]domain.FileChange, error) {
	cmd := exec.Command("git", "-C", repoRoot, "diff", "--name-status", base+"..."+head)
	out, err := cmd.Output()
	if err != nil {
		// Fall back to base..head if base...head fails (no merge base).
		cmd = exec.Command("git", "-C", repoRoot, "diff", "--name-status", base+".."+head)
		out, err = cmd.Output()
		if err != nil {
			return nil, err
		}
	}
	var changes []domain.FileChange
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		status, path := parts[0], parts[1]
		op := domain.OpWrite
		switch {
		case strings.HasPrefix(status, "D"):
			op = domain.OpDelete
		case strings.HasPrefix(status, "R"):
			op = domain.OpRename
		case strings.HasPrefix(status, "A"):
			op = domain.OpWrite
		case strings.HasPrefix(status, "M"):
			op = domain.OpEdit
		}
		changes = append(changes, domain.FileChange{Path: path, Op: op})
	}
	return changes, nil
}

// --- Verdict cache (keyless CI replay) ---
//
// The verdict cache stores the domain.ValidationResult of a CI run keyed by a
// SHA-256 over every input that could change the verdict. A later run with
// byte-identical inputs replays the cached verdict (exit code, findings,
// per-check breakdown) instead of re-running the full architecture/secrets/
// duplication/sandbox scan — CI passes or fails identically in milliseconds.
//
// Cache layout (all gitignored via `.blueprint/`):
//
//	.blueprint/verdict-cache/<key>.json  — the cached ValidationResult
//	.blueprint/verdict-cache/meta.json   — kern binary fingerprint + version
//
// CI must preserve `.blueprint/` between runs (GitHub Actions `actions/cache`
// keyed on the checkout, Jenkins workspace persistence, etc.) for cross-run
// hits. If it is not preserved the cache simply misses: no speedup, no
// correctness change.

// verdictCacheSchemaVersion is bumped whenever the cache entry format or the
// key composition changes, invalidating all prior entries.
const verdictCacheSchemaVersion = 1

// verdictCacheEntry is one cached validation result.
type verdictCacheEntry struct {
	Schema    int                     `json:"schema"`
	Key       string                  `json:"key"`
	WrittenAt string                  `json:"written_at"`
	Result    domain.ValidationResult `json:"result"`
}

// kernVersionMeta records the kern binary fingerprint observed when a verdict
// was cached, so a later run can reuse the cached `kern --version` probe
// without spawning kern. If the binary (path/size/mtime) changes, the
// fingerprint no longer matches and the version is re-probed, which changes
// the cache key and forces a re-validation.
type kernVersionMeta struct {
	Schema        int    `json:"schema"`
	Version       string `json:"kern_version"`
	BinaryPath    string `json:"binary_path,omitempty"`
	BinarySize    int64  `json:"binary_size,omitempty"`
	BinaryMtimeNS int64  `json:"binary_mtime_ns,omitempty"`
	WrittenAt     string `json:"written_at,omitempty"`
}

// repoIdentity identifies the repository for the cache key so verdicts are
// never shared across different repos. It prefers the origin remote URL (stable
// across checkouts of the same repo) and falls back to the absolute path.
func repoIdentity(absRoot string) string {
	if out, err := exec.Command("git", "-C", absRoot, "remote", "get-url", "origin").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	return absRoot
}

// resolveRefSHA resolves a git ref to its commit SHA for the cache key, falling
// back to the raw ref string so a resolution hiccup can never fail validation.
func resolveRefSHA(repoRoot, ref string) string {
	if out, err := exec.Command("git", "-C", repoRoot, "rev-parse", ref).Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	return ref
}

// computeFileSetHash hashes the staged file set: every changed file's path,
// operation, and content hash. Content is read from the validation root (the
// working tree when head is HEAD, else the throwaway worktree) so it reflects
// exactly what Validate will see; deleted files are hashed by their base blob
// SHA. Entries are sorted by path so the hash is canonical.
func computeFileSetHash(repoRoot, valRoot, baseSHA, headSHA string, changes []domain.FileChange) string {
	type entry struct {
		Path string `json:"path"`
		Op   string `json:"op"`
		Sha  string `json:"sha"`
	}
	entries := make([]entry, 0, len(changes))
	for _, ch := range changes {
		e := entry{Path: ch.Path, Op: string(ch.Op)}
		switch {
		case ch.Op == domain.OpDelete:
			// The file is gone from the head tree; hash its base blob instead.
			if out, err := exec.Command("git", "-C", repoRoot, "rev-parse", baseSHA+":"+ch.Path).Output(); err == nil {
				e.Sha = "blob:" + strings.TrimSpace(string(out))
			} else {
				e.Sha = "deleted"
			}
		default:
			if data, err := os.ReadFile(filepath.Join(valRoot, filepath.FromSlash(ch.Path))); err == nil {
				sum := sha256.Sum256(data)
				e.Sha = "content:" + hex.EncodeToString(sum[:])
			} else if out, err := exec.Command("git", "-C", repoRoot, "rev-parse", headSHA+":"+ch.Path).Output(); err == nil {
				e.Sha = "blob:" + strings.TrimSpace(string(out))
			} else {
				e.Sha = "unreadable"
			}
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	b, _ := json.Marshal(entries)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// computePolicyHash hashes every policy/config input that can change the
// verdict: .blueprint/config.yaml (mode, enforcement, source overrides),
// .blueprint/suppressions.yaml (P1-2), .blueprint/owners.yaml (P1-2), and
// .kern/boundaries.json (architecture rules). Absent files hash as "absent"
// so adding one also changes the key.
func computePolicyHash(valRoot string) string {
	type entry struct {
		Path string `json:"path"`
		Sha  string `json:"sha"`
	}
	candidates := []string{
		filepath.Join(".blueprint", "config.yaml"),
		filepath.Join(".blueprint", "suppressions.yaml"),
		filepath.Join(".blueprint", "owners.yaml"),
		filepath.Join(".kern", "boundaries.json"),
	}
	entries := make([]entry, 0, len(candidates))
	for _, p := range candidates {
		data, err := os.ReadFile(filepath.Join(valRoot, filepath.FromSlash(p)))
		switch {
		case err == nil:
			sum := sha256.Sum256(data)
			entries = append(entries, entry{Path: p, Sha: hex.EncodeToString(sum[:])})
		case os.IsNotExist(err):
			entries = append(entries, entry{Path: p, Sha: "absent"})
		default:
			entries = append(entries, entry{Path: p, Sha: "unreadable"})
		}
	}
	b, _ := json.Marshal(entries)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// computeFlagsHash folds the check flags that can affect the verdict or exit
// code into the cache key.
func computeFlagsHash(jsonOut, noHuman, strictLatency bool) string {
	b, _ := json.Marshal(map[string]bool{
		"json":           jsonOut,
		"no_human":       noHuman,
		"strict_latency": strictLatency,
	})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// computeVerdictKey derives the cache key: SHA-256 over the canonical JSON of
// every validation input — repo identity, resolved base/head SHAs (diff
// context), the staged file set hash, the policy hash, the kern version, the
// blueprint version, and the check flags.
func computeVerdictKey(repo, baseSHA, headSHA, fileHash, policyHash, kernVersion, blueprintVersion, flagsHash string) string {
	payload := struct {
		Schema    int    `json:"v"`
		Repo      string `json:"repo"`
		Base      string `json:"base"`
		Head      string `json:"head"`
		Files     string `json:"files"`
		Policy    string `json:"policy"`
		Kern      string `json:"kern"`
		Blueprint string `json:"blueprint"`
		Flags     string `json:"flags"`
	}{
		Schema:    verdictCacheSchemaVersion,
		Repo:      repo,
		Base:      baseSHA,
		Head:      headSHA,
		Files:     fileHash,
		Policy:    policyHash,
		Kern:      kernVersion,
		Blueprint: blueprintVersion,
		Flags:     flagsHash,
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// kernBinaryFingerprint returns the path/size/mtime of the kern binary that
// would be resolved for this process (KERN_BINARY, then $PATH). An empty path
// means no binary could be resolved (cache version reuse then simply does not
// apply and the version is probed as usual).
func kernBinaryFingerprint() (string, int64, int64) {
	p := ""
	if env := os.Getenv("KERN_BINARY"); env != "" {
		p = env
	} else if lp, err := exec.LookPath("kern"); err == nil {
		p = lp
	}
	if p == "" {
		return "", 0, 0
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return "", 0, 0
	}
	return p, info.Size(), info.ModTime().UnixNano()
}

// cachedKernVersion returns the kern version recorded at the last cache write
// when the binary fingerprint still matches — a cache hit path therefore runs
// zero kern subprocesses. It returns ok=false when there is no meta, the
// fingerprint no longer matches (kern upgraded/replaced), or the binary cannot
// be resolved; the caller then probes `kern version` afresh.
func cachedKernVersion(cacheDir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(cacheDir, "meta.json"))
	if err != nil {
		return "", false
	}
	var meta kernVersionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", false
	}
	if meta.Schema != verdictCacheSchemaVersion || meta.Version == "" || meta.BinaryPath == "" {
		return "", false
	}
	info, err := os.Stat(meta.BinaryPath)
	if err != nil || info.IsDir() || info.Size() != meta.BinarySize || info.ModTime().UnixNano() != meta.BinaryMtimeNS {
		return "", false
	}
	return meta.Version, true
}

// writeKernVersionMeta records the kern version and binary fingerprint after a
// full validation. Only written when a binary fingerprint is available, so a
// later run can skip the version probe on a cache hit.
func writeKernVersionMeta(cacheDir, kernVersion string) {
	binaryPath, size, mtime := kernBinaryFingerprint()
	if binaryPath == "" {
		return
	}
	meta := kernVersionMeta{
		Schema:        verdictCacheSchemaVersion,
		Version:       kernVersion,
		BinaryPath:    binaryPath,
		BinarySize:    size,
		BinaryMtimeNS: mtime,
		WrittenAt:     time.Now().UTC().Format(time.RFC3339),
	}
	b, _ := json.Marshal(meta)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(cacheDir, "meta.json"), b, 0o644)
}

// writeVerdictEntry stores a validation result under its key. Best-effort: a
// failed write only costs the next run a full re-validation.
func writeVerdictEntry(cacheDir, key string, result domain.ValidationResult) {
	entry := verdictCacheEntry{
		Schema:    verdictCacheSchemaVersion,
		Key:       key,
		WrittenAt: time.Now().UTC().Format(time.RFC3339),
		Result:    result,
	}
	b, _ := json.Marshal(entry)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(cacheDir, key+".json"), b, 0o644)
}

// loadVerdictEntry reads and validates a cached verdict. Corrupt, torn, or
// wrong-key files are treated as a miss (the cache is an optimization).
func loadVerdictEntry(cacheDir, key string) (verdictCacheEntry, bool) {
	data, err := os.ReadFile(filepath.Join(cacheDir, key+".json"))
	if err != nil {
		return verdictCacheEntry{}, false
	}
	var entry verdictCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return verdictCacheEntry{}, false
	}
	if entry.Schema != verdictCacheSchemaVersion || entry.Key != key {
		return verdictCacheEntry{}, false
	}
	return entry, true
}
