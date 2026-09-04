// Package kern implements Blueprint validation checks that shell out to the
// kern binary (subprocess adapters). It provides two checks:
//
//   - ArchitectureCheck ("architecture:guard") — enforces the boundaries
//     declared in .kern/boundaries.json via `kern guard check --json`.
//   - SecretCheck ("secret:scan") — scans changed files for hardcoded
//     secrets via `kern sec --json`.
//
// KernClient is the thin subprocess seam both checks share. It resolves the
// kern binary (KERN_BINARY env var, $PATH, then ../kern/bin/kern) and runs
// read-only kern subcommands, capturing stdout, stderr, and the exit code
// separately.
package kern

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/version"
)

// KernContractVersion is the version of the JSON contract Blueprint expects
// from `kern guard check --json` and `kern sec --json`. kern emits
// {"schema_version":2,"violations":[...]},
// {"schema_version":2,"findings":[...]}, and — when the change carries an
// agent identity — an "authz_verdict" object (P0.4) since this version.
// Blueprint fails closed on any missing/wrong/malformed contract so a
// version skew surfaces as an ERROR rather than a silent misparse.
const KernContractVersion = 2

// Violation is one architecture boundary violation reported by
// `kern guard check --json`.
type Violation struct {
	CallerFile string `json:"caller_file"`
	CalleeFile string `json:"callee_file"`
	Symbol     string `json:"symbol"`
	Line       int    `json:"line"`
	RuleFrom   string `json:"rule_from"`
	RuleTo     string `json:"rule_to"`
}

// SecFinding is one secret-scan finding reported by `kern sec --json`.
//
// Snippet is intentionally never mapped into a domain.Finding: secret text
// must not propagate into Blueprint results (spec Rule: "Do not send secrets
// to agents").
type SecFinding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // "error" | "warning" | "info"
	Message  string `json:"message"`
	Snippet  string `json:"snippet"`
}

// AuthzVerdict is kern's authorization verdict for an agent-sourced change
// (P0.4), carried in the v2 `kern guard check --json` payload under the
// "authz_verdict" key. Decision is "allowed" | "denied" | "unknown";
// PolicySource says which policy produced it ("default-scoped" |
// "task-scope" | "permissive-default"). DeniedFiles lists the paths the
// agent is not authorized to modify. The verdict is only emitted when the
// change carries an agent identity AND the installed kern speaks the v2
// contract; otherwise the key is absent.
type AuthzVerdict struct {
	SchemaVersion int      `json:"schema_version"`
	AgentID       string   `json:"agent_id"`
	Task          string   `json:"task"`
	Decision      string   `json:"decision"`
	PolicySource  string   `json:"policy_source"`
	DeniedFiles   []string `json:"denied_files"`
	Fingerprint   string   `json:"fingerprint"`
	DecidedAt     string   `json:"decided_at"`
}

// commandRunner executes a subprocess and returns its captured stdout, stderr,
// and exit code. err is non-nil only when the process failed to launch
// (binary missing, context canceled before start); a non-zero exit code is
// reported through exitCode, not err. Tests inject a fake runner so they never
// depend on the real kern binary.
type commandRunner func(ctx context.Context, name string, args []string, workdir string) (stdout, stderr string, exitCode int, err error)

// CommandRunner is the exported form of commandRunner. It is exposed so
// external packages (notably the duplication check and its tests) can inject
// a fake subprocess runner without depending on the real kern binary.
type CommandRunner = commandRunner

// KernClient wraps the kern binary as a subprocess.
type KernClient struct {
	binaryPath string
	runner     commandRunner
	// version/versionErr cache the first `kern version` probe so it runs at
	// most once per client lifetime (P2-4 provenance stamping).
	version    string
	versionErr error
	versionSet bool
}

// Option configures a KernClient.
type Option func(*KernClient)

// WithBinary overrides the resolved kern binary path. It is used by tests and
// embedders that know the exact location of the binary.
func WithBinary(path string) Option {
	return func(c *KernClient) { c.binaryPath = path }
}

// WithRunner overrides the subprocess runner. It is intended for tests and
// embedders that need to inject a fake command runner so no real kern binary
// is executed.
func WithRunner(r CommandRunner) Option {
	return func(c *KernClient) { c.runner = r }
}

// NewKernClient resolves the kern binary and returns a client.
//
// Resolution order:
//  1. KERN_BINARY environment variable
//  2. kern on $PATH
//  3. ../kern/bin/kern relative to the current working directory
//
// It returns an error if no candidate is found. The variadic options are
// optional; WithBinary forces a specific path and skips resolution.
func NewKernClient(opts ...Option) (*KernClient, error) {
	c := &KernClient{runner: execCommand}
	for _, o := range opts {
		o(c)
	}
	if c.binaryPath == "" {
		p, err := resolveKernBinary()
		if err != nil {
			return nil, err
		}
		c.binaryPath = p
	}
	return c, nil
}

// Version returns the installed kern binary's version, best-effort (P2-4). It
// runs `kern version` once per client lifetime and caches the result: later
// calls return the cached value without spawning another subprocess. The
// output ("kern dev", "kern v0.9.0", ...) has a leading "kern " prefix
// stripped, so the returned value is the bare version. A launch failure,
// non-zero exit, or empty output returns an error so callers can treat the
// whole probe as best-effort (an empty string must never fail validation).
func (c *KernClient) Version() (string, error) {
	if c.versionSet {
		return c.version, c.versionErr
	}
	c.versionSet = true
	out, errOut, code, runErr := c.runner(context.Background(), c.binaryPath, []string{"version"}, "")
	if runErr != nil {
		c.versionErr = fmt.Errorf("kern version: %w", runErr)
		return "", c.versionErr
	}
	if code != 0 {
		c.versionErr = fmt.Errorf("kern version failed (exit %d): %s", code, strings.TrimSpace(errOut))
		return "", c.versionErr
	}
	v := strings.TrimSpace(strings.TrimPrefix(out, "kern "))
	if v == "" {
		c.versionErr = fmt.Errorf("kern version: empty output")
		return "", c.versionErr
	}
	c.version = v
	return c.version, nil
}

// resolveKernBinary locates the kern executable per NewKernClient's order.
// When no candidate is found, it attempts to install kern via `go install`
// so the user does not need a separate manual step.
func resolveKernBinary() (string, error) {
	if p := os.Getenv("KERN_BINARY"); p != "" {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("kern binary not found at KERN_BINARY=%q", p)
	}
	if p, err := exec.LookPath("kern"); err == nil {
		return p, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("kern binary not found: %w", err)
	}
	candidates := []string{
		filepath.Join(cwd, "bin", "kern"),
		filepath.Join(cwd, "..", "kern", "bin", "kern"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	// Not found — attempt automatic install.
	if installErr := installKern(); installErr != nil {
		return "", fmt.Errorf("kern binary not found: set KERN_BINARY, add kern to $PATH, or place it at %s (auto-install failed: %v)", candidates[0], installErr)
	}
	// Re-resolve after install: go install puts binaries in $(go env GOPATH)/bin.
	if p, err := exec.LookPath("kern"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("kern binary not found after install: add $(go env GOPATH)/bin to $PATH")
}

// installKern installs the kern binary via `go install`. It is called
// automatically when resolveKernBinary cannot find an existing kern. The
// user sees progress on stderr; errors are returned but not fatal (the
// caller falls back to degraded mode).
func installKern() error {
	fmt.Fprintf(os.Stderr, "blueprint: kern not found — installing latest kern (go install %s) ...\n", version.KernModulePath)
	cmd := exec.Command("go", "install", version.KernModulePath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install kern: %w", err)
	}
	fmt.Fprintf(os.Stderr, "blueprint: kern installed successfully.\n")
	return nil
}

// EnsureMinVersion checks that the kern binary meets Blueprint's minimum
// version requirement. It returns an error with a clear upgrade message
// when the version is below the threshold.
func EnsureMinVersion(client *KernClient) error {
	v, err := client.Version()
	if err != nil {
		// Best-effort: if version probe fails, allow through (contract
		// mismatch will be caught by GuardCheck/SecCheck).
		return nil
	}
	if !version.VersionAtLeast(v, version.MinKernVersion) {
		return fmt.Errorf("kern %s is too old (Blueprint requires >= %s): upgrade with `go install %s`",
			v, version.MinKernVersion, version.KernModulePath)
	}
	return nil
}

// execCommand is the default commandRunner. It runs the process with the given
// working directory and returns the captured stdout, stderr, and exit code.
// err is non-nil only for launch failures, not for non-zero exits.
func execCommand(ctx context.Context, name string, args []string, workdir string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workdir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	if runErr == nil {
		return outBuf.String(), errBuf.String(), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		// The process ran; its exit code is a result, not a launch failure.
		return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
	}
	return outBuf.String(), errBuf.String(), -1, runErr
}

// GuardCheck runs `kern guard check --json` in workdir and returns the parsed
// violations, the raw stdout, and the process exit code.
//
// Exit codes 0 (clean) and 2 (violations found) are results, not errors: both
// parse their JSON output and return it. Any other exit code is a tool
// failure and is returned as an error. workdir must be the repository root so
// kern finds .kern/boundaries.json.
//
// With no files argument, kern uses ChangedFiles(root) which includes both
// staged and unstaged git changes. Use GuardCheckFiles to scope to specific
// files (e.g. only staged files).
func (c *KernClient) GuardCheck(ctx context.Context, workdir string) (violations []Violation, stdout string, exitCode int, err error) {
	out, errOut, code, runErr := c.runner(ctx, c.binaryPath, []string{"guard", "check", "--json"}, workdir)
	if runErr != nil {
		return nil, out, code, fmt.Errorf("kern guard check: %w", runErr)
	}
	if code != 0 && code != 2 {
		return nil, out, code, fmt.Errorf("kern guard check failed (exit %d): %s", code, strings.TrimSpace(errOut))
	}
	var payload struct {
		SchemaVersion int         `json:"schema_version"`
		Violations    []Violation `json:"violations"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return nil, out, code, fmt.Errorf("kern guard check: parse output: %w", err)
	}
	if payload.SchemaVersion != KernContractVersion {
		return nil, out, code, fmt.Errorf("kern guard check: contract mismatch: expected schema_version %d, got %d (missing schema_version means the installed kern is too old — upgrade kern or pin KERN_BINARY)", KernContractVersion, payload.SchemaVersion)
	}
	return payload.Violations, out, code, nil
}

// guardBatchSize caps how many files are passed to a single `kern guard check
// --file f1,f2,...` invocation. Joining the whole set into one --file argument
// explodes argv on large strict-baseline runs (every tracked file in the repo),
// so the set is chunked into batches of at most guardBatchSize files per exec.
const guardBatchSize = 64

// GuardCheckFiles runs `kern guard check --file f1,f2 --json` in workdir,
// scoping the boundary check to the given files. This implements the
// new-change principle: only violations in changed files are reported,
// pre-existing violations in unchanged files are not.
//
// Large file sets are chunked into batches of at most guardBatchSize files
// per exec to bound argv size; violations from all batches are merged.
// On any batch error, the accumulated stdout/exitCode so far are returned
// alongside the error (stdout = join of the batch outputs, exitCode = last
// non-zero batch exit code, or 0 when every batch exited 0).
//
// If files is empty, falls back to GuardCheck (which uses ChangedFiles).
func (c *KernClient) GuardCheckFiles(ctx context.Context, workdir string, files []string) (violations []Violation, stdout string, exitCode int, err error) {
	if len(files) == 0 {
		return c.GuardCheck(ctx, workdir)
	}
	var allViolations []Violation
	var stdoutParts []string
	for i := 0; i < len(files); i += guardBatchSize {
		end := i + guardBatchSize
		if end > len(files) {
			end = len(files)
		}
		batch := files[i:end]
		fileArg := strings.Join(batch, ",")
		out, errOut, code, runErr := c.runner(ctx, c.binaryPath, []string{"guard", "check", "--file", fileArg, "--json"}, workdir)
		stdoutParts = append(stdoutParts, out)
		if code != 0 {
			exitCode = code
		}
		if runErr != nil {
			return allViolations, strings.Join(stdoutParts, "\n"), exitCode, fmt.Errorf("kern guard check (batch %d-%d): %w", i+1, end, runErr)
		}
		if code != 0 && code != 2 {
			return allViolations, strings.Join(stdoutParts, "\n"), exitCode, fmt.Errorf("kern guard check failed (exit %d, batch %d-%d): %s", code, i+1, end, strings.TrimSpace(errOut))
		}
		var payload struct {
			SchemaVersion int         `json:"schema_version"`
			Violations    []Violation `json:"violations"`
		}
		if err := json.Unmarshal([]byte(out), &payload); err != nil {
			return allViolations, strings.Join(stdoutParts, "\n"), exitCode, fmt.Errorf("kern guard check: parse output (batch %d-%d): %w", i+1, end, err)
		}
		if payload.SchemaVersion != KernContractVersion {
			return allViolations, strings.Join(stdoutParts, "\n"), exitCode, fmt.Errorf("kern guard check (batch %d-%d): contract mismatch: expected schema_version %d, got %d (missing schema_version means the installed kern is too old — upgrade kern or pin KERN_BINARY)", i+1, end, KernContractVersion, payload.SchemaVersion)
		}
		allViolations = append(allViolations, payload.Violations...)
	}
	return allViolations, strings.Join(stdoutParts, "\n"), exitCode, nil
}

// AuthzVerdict returns kern's authorization verdict for an agent-sourced
// change (P0.4). It runs `kern guard check --file <files> --agent-id <id>
// --task <task> --json` in workdir and parses the "authz_verdict" object out
// of the v2 output.
//
// The verdict object is only present when the change carries an agent
// identity AND the installed kern speaks the v2 contract; a missing key
// means "no verdict available" and returns (nil, nil) — the caller proceeds
// without authz (backward compat with older kern builds and non-agent
// flows).
//
// Exit code 2 is a RESULT, not an error: kern exits 2 both for boundary
// violations and for a "denied" authz verdict. Only a launch failure, a
// non-zero-non-2 exit, or a JSON/contract parse error is an error. The
// contract is fail-closed: a missing or wrong schema_version errors rather
// than silently misparsing.
func (c *KernClient) AuthzVerdict(ctx context.Context, workdir, agentID, task string, files []string) (*AuthzVerdict, error) {
	if agentID == "" || task == "" || len(files) == 0 {
		// No agent identity, no task scope, or no files to authorize: kern's
		// guard requires --agent-id AND --task together (its P0.4 usage
		// rule), and with nothing to authorize there is no verdict. The
		// caller proceeds without authz.
		return nil, nil
	}
	fileArg := strings.Join(files, ",")
	args := []string{"guard", "check", "--file", fileArg, "--agent-id", agentID, "--task", task, "--json"}
	out, errOut, code, runErr := c.runner(ctx, c.binaryPath, args, workdir)
	if runErr != nil {
		return nil, fmt.Errorf("kern guard check authz: %w", runErr)
	}
	if code != 0 && code != 2 {
		return nil, fmt.Errorf("kern guard check authz failed (exit %d): %s", code, strings.TrimSpace(errOut))
	}
	var payload struct {
		SchemaVersion int           `json:"schema_version"`
		AuthzVerdict  *AuthzVerdict `json:"authz_verdict"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return nil, fmt.Errorf("kern guard check authz: parse output: %w", err)
	}
	if payload.SchemaVersion != KernContractVersion {
		return nil, fmt.Errorf("kern guard check authz: contract mismatch: expected schema_version %d, got %d (missing schema_version means the installed kern is too old — upgrade kern or pin KERN_BINARY)", KernContractVersion, payload.SchemaVersion)
	}
	if payload.AuthzVerdict == nil {
		// No authz_verdict key: kern too old or no authz policy for this
		// change — not an error, the caller proceeds without a verdict.
		return nil, nil
	}
	return payload.AuthzVerdict, nil
}

// IndexBuild runs `kern index <root>` in workdir to (re)build the kern symbol
// index. This must be called after files are staged/modified but before
// GuardCheck, so the index reflects the current file content and the boundary
// check sees up-to-date symbol edges.
func (c *KernClient) IndexBuild(ctx context.Context, workdir string) (stdout string, exitCode int, err error) {
	out, errOut, code, runErr := c.runner(ctx, c.binaryPath, []string{"index", "."}, workdir)
	if runErr != nil {
		return out, code, fmt.Errorf("kern index: %w", runErr)
	}
	if code != 0 {
		return out, code, fmt.Errorf("kern index failed (exit %d): %s", code, strings.TrimSpace(errOut))
	}
	return out, code, nil
}

// IndexStatus calls `kern index --status --json [--strict]` in root and
// returns the parsed status payload. strict=true requests the
// untracked-file-aware freshness proof: kern recomputes content_root over
// every file, so untracked proposed-new files count toward staleness too.
//
// The payload is schema-versioned (schema_version is the STRING "2"); a
// missing or mismatched version is a contract skew and fails closed, so an
// older kern without the P0.2 freshness contract surfaces as an ERROR rather
// than a silent misparse. In the payload, top-level "stale" is false exactly
// when freshness_proof.verdict is "fresh"; when built is false (no index),
// both freshness_proof and index_identity are omitted, and the caller treats
// the index as stale (fail-closed).
func (c *KernClient) IndexStatus(ctx context.Context, root string, strict bool) (map[string]any, error) {
	args := []string{"index", "--status", "--json"}
	if strict {
		args = append(args, "--strict")
	}
	out, errOut, code, runErr := c.runner(ctx, c.binaryPath, args, root)
	if runErr != nil {
		return nil, fmt.Errorf("kern index status: %w", runErr)
	}
	if code != 0 {
		return nil, fmt.Errorf("kern index status failed (exit %d): %s", code, strings.TrimSpace(errOut))
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return nil, fmt.Errorf("kern index status: parse: %w", err)
	}
	if v, _ := status["schema_version"].(string); v != "2" {
		return nil, fmt.Errorf("kern index status: schema_version %q, want 2", v)
	}
	return status, nil
}

// SecScan runs `kern sec --json <path>` in workdir and returns the parsed
// findings, the raw stdout, and the process exit code.
//
// Exit codes 0 (clean) and 1 (findings found) are results, not errors.
// Because kern also uses exit code 1 for tool errors, stderr presence
// distinguishes the two: exit 1 with non-empty stderr is a tool failure and
// is returned as an error.
func (c *KernClient) SecScan(ctx context.Context, workdir, path string) (findings []SecFinding, stdout string, exitCode int, err error) {
	out, errOut, code, runErr := c.runner(ctx, c.binaryPath, []string{"sec", "--json", path}, workdir)
	if runErr != nil {
		return nil, out, code, fmt.Errorf("kern sec: %w", runErr)
	}
	if code != 0 && code != 1 {
		return nil, out, code, fmt.Errorf("kern sec failed (exit %d): %s", code, strings.TrimSpace(errOut))
	}
	if code == 1 && strings.TrimSpace(errOut) != "" {
		// Tool error, not a findings result.
		return nil, out, code, fmt.Errorf("kern sec: %s", strings.TrimSpace(errOut))
	}
	if strings.TrimSpace(out) == "" {
		// Clean run with no JSON payload (defensive; --json normally prints []).
		return nil, out, code, nil
	}
	// Versioned contract check: kern >= this contract emits
	// {"schema_version":2,"findings":[...]}. A bare array is the legacy
	// unversioned shape from an older kern and must be rejected, not silently
	// misparsed.
	trimmed := bytes.TrimSpace([]byte(out))
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return nil, out, code, fmt.Errorf("kern sec: contract mismatch: unversioned array output (kern too old — upgrade kern or pin KERN_BINARY)")
	}
	var payload struct {
		SchemaVersion int          `json:"schema_version"`
		Findings      []SecFinding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return nil, out, code, fmt.Errorf("kern sec: parse output: %w", err)
	}
	if payload.SchemaVersion != KernContractVersion {
		return nil, out, code, fmt.Errorf("kern sec: contract mismatch: expected schema_version %d, got %d (missing schema_version means the installed kern is too old — upgrade kern or pin KERN_BINARY)", KernContractVersion, payload.SchemaVersion)
	}
	return payload.Findings, out, code, nil
}
