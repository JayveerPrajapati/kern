// Package gitleaks implements a Blueprint service.Check that delegates secret
// detection to the gitleaks binary (https://github.com/gitleaks/gitleaks).
//
// gitleaks is deterministic (spec Rule 4): any finding is mapped to
// domain.SeverityBlock, matching the in-house kern secret check's severity.
//
// Redaction is absolute: the raw `Secret` and `Match` fields from gitleaks'
// JSON report are NEVER propagated into domain.Finding (same contract as the
// in-house kern secret check — "never echo the secret itself into agent
// feedback").
//
// When the gitleaks binary is unavailable (GITLEAKS_BINARY unset and no
// `gitleaks` on $PATH), the check degrades to the in-house kern secret
// scanner and adds a WARN finding so the fallback is never silent (tracker
// T2.1 acceptance).
package gitleaks

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

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// gitleaksFinding is one element of gitleaks' JSON report
// (`gitleaks dir --report-format json --report-path out.json <dir>`).
// Field names and types match the captured 8.30.1 output byte-for-byte.
type gitleaksFinding struct {
	RuleID      string   `json:"RuleID"`
	Description string   `json:"Description"`
	StartLine   int      `json:"StartLine"`
	EndLine     int      `json:"EndLine"`
	StartColumn int      `json:"StartColumn"`
	EndColumn   int      `json:"EndColumn"`
	Match       string   `json:"Match"`
	Secret      string   `json:"Secret"`
	File        string   `json:"File"`
	SymlinkFile string   `json:"SymlinkFile"`
	Commit      string   `json:"Commit"`
	Entropy     float64  `json:"Entropy"`
	Author      string   `json:"Author"`
	Email       string   `json:"Email"`
	Date        string   `json:"Date"`
	Message     string   `json:"Message"`
	Tags        []string `json:"Tags"`
	Fingerprint string   `json:"Fingerprint"`
}

// commandRunner executes a subprocess and returns its captured stdout, stderr,
// and exit code. err is non-nil only when the process failed to launch; a
// non-zero exit code is reported through exitCode, not err. Tests inject a
// fake runner so they never depend on the real gitleaks binary.
type commandRunner func(ctx context.Context, name string, args []string, workdir string) (stdout, stderr string, exitCode int, err error)

// Check implements service.Check by delegating secret detection to gitleaks.
type Check struct {
	client *kern.KernClient // used by the in-house fallback (may be nil)

	runner         commandRunner
	binary         string
	binaryExplicit bool

	version    string // cached `gitleaks version` output
	versionSet bool
}

// Option configures a Check. Options exist for tests and embedders that need
// to pin the binary path or inject a fake subprocess runner.
type Option func(*Check)

// WithBinary forces the gitleaks binary path. An empty string disables the
// incumbent, which makes Run fall back to the in-house kern secret check.
func WithBinary(path string) Option {
	return func(c *Check) {
		c.binary = path
		c.binaryExplicit = true
	}
}

// WithRunner injects the subprocess runner (tests).
func WithRunner(r commandRunner) Option {
	return func(c *Check) { c.runner = r }
}

// WithVersion pins the gitleaks version string stamped on findings, skipping
// the `gitleaks version` probe (tests).
func WithVersion(v string) Option {
	return func(c *Check) { c.version = v; c.versionSet = true }
}

// NewCheck constructs a gitleaks-backed secret check. client is used only for
// the in-house fallback when the gitleaks binary is unavailable (may be nil).
func NewCheck(client *kern.KernClient, opts ...Option) *Check {
	c := &Check{client: client, runner: execCommand}
	for _, o := range opts {
		o(c)
	}
	if !c.binaryExplicit {
		c.binary = resolveBinary()
	}
	return c
}

// Name returns the stable check identifier used for policy routing. The
// "secret:" prefix routes this check to the secret category
// (policy.categoryFromCheck splits on the first colon).
func (c *Check) Name() string { return "secret:gitleaks" }

// Run scans the changed files for secrets with gitleaks. Files with proposed
// content (Content != "", pre-write) and on-disk changed files are written
// into a temp mirror at their exact repo-relative paths and the mirror is
// scanned, so gitleaks only ever sees the changed files (new-change
// principle: only new secrets are reported). When the gitleaks binary is
// unavailable, Run degrades to the in-house kern secret scanner and flags the
// fallback with a WARN finding.
func (c *Check) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if req.RepositoryRoot == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}

	// No changed files => nothing to scan: PASS without invoking gitleaks
	// (observable behavior identical, no pointless subprocess).
	if len(req.Files) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}

	// Incumbent unavailable => degrade to the in-house kern secret scanner,
	// flagged with a WARN so the fallback is never silent.
	if c.binary == "" {
		return c.fallback(ctx, req)
	}

	scanDir, err := os.MkdirTemp("", "blueprint-gitleaks-*")
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "gitleaks: create scan dir: " + err.Error()}, nil
	}
	defer os.RemoveAll(scanDir)

	changedSet := make(map[string]bool, len(req.Files))
	for _, fc := range req.Files {
		if fc.Op == domain.OpDelete {
			continue
		}

		// Confine changed paths to the temp dir: reject anything that would
		// escape it via ".." or an absolute path (same pattern as the in-house
		// secret check's content path).
		rel := filepath.Clean(fc.Path)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: fmt.Sprintf("invalid path in change request: %q", fc.Path)}, nil
		}

		content := fc.Content
		if content == "" {
			b, err := os.ReadFile(filepath.Join(req.RepositoryRoot, fc.Path))
			if err != nil {
				return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: fmt.Sprintf("gitleaks: read %s: %v", fc.Path, err)}, nil
			}
			content = string(b)
		}

		dest := filepath.Join(scanDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: fmt.Sprintf("gitleaks: mkdir %s: %v", fc.Path, err)}, nil
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: fmt.Sprintf("gitleaks: write %s: %v", fc.Path, err)}, nil
		}
		changedSet[normalizePath(fc.Path)] = true
	}

	// The report file lives OUTSIDE the scanned dir so gitleaks never rescans
	// its own report (which embeds the raw secrets it found).
	reportFile, err := os.CreateTemp("", "blueprint-gitleaks-report-*.json")
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "gitleaks: create report file: " + err.Error()}, nil
	}
	reportPath := reportFile.Name()
	if err := reportFile.Close(); err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "gitleaks: close report file: " + err.Error()}, nil
	}
	defer os.Remove(reportPath)

	_, stderr, code, runErr := c.runner(ctx, c.binary,
		[]string{"dir", "--no-banner", "--report-format", "json", "--report-path", reportPath, scanDir},
		scanDir)
	if runErr != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "gitleaks: " + runErr.Error()}, nil
	}
	// gitleaks exits 0 (no leaks) and 1 (leaks found); both are results. Any
	// other exit code is a tool failure (gitleaks uses 2 for errors).
	if code != 0 && code != 1 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: fmt.Sprintf("gitleaks failed (exit %d): %s", code, strings.TrimSpace(stderr))}, nil
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "gitleaks: read report: " + err.Error()}, nil
	}
	var findings []gitleaksFinding
	if err := json.Unmarshal(data, &findings); err != nil {
		// Fail closed on malformed output (G14 contract): a report we cannot
		// parse is a tool failure, never a silent pass.
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "gitleaks: parse report: " + err.Error()}, nil
	}

	version := c.gitleaksVersion(ctx)

	var out []domain.Finding
	for _, gf := range findings {
		// gitleaks reports absolute paths; the mirror preserves the
		// repo-relative path by construction, so strip the scan-dir prefix
		// BEFORE normalizing.
		file := gf.File
		if dir := filepath.Clean(scanDir); file == dir || strings.HasPrefix(file, dir+"/") {
			file = strings.TrimPrefix(file, dir+"/")
		}
		file = normalizePath(file)

		// New-change principle: only report secrets in changed files.
		if !changedSet[file] {
			continue
		}

		// Test-fixture suppression: findings in allowlisted files (testdata/
		// dirs, _test.go files, *.test.js, etc.) are suppressed, matching the
		// in-house kern secret check (spec: "support allowlisted
		// placeholders/test fixtures explicitly"). Reuses kern's
		// DefaultAllowlist so the fixture semantics can never drift.
		if (kern.DefaultAllowlist{}).IsAllowed(file) {
			continue
		}

		out = append(out, domain.Finding{
			RuleID:       "secret:gitleaks:" + gf.RuleID,
			Severity:     domain.SeverityBlock, // gitleaks is deterministic (Rule 4)
			Category:     domain.CategorySecret,
			File:         file,
			Line:         gf.StartLine,
			Message:      fmt.Sprintf("Hardcoded secret detected by gitleaks (rule: %s)", gf.RuleID),
			Explanation:  fmt.Sprintf("%s: %s", gf.RuleID, gf.Description),
			SuggestedFix: "Remove the credential and move it to runtime secret storage (environment variable, vault, or secret manager).",
			Redacted:     true, // CRITICAL: the raw Secret/Match never propagate.
			RuleVersion:  version,
			Confidence:   1.0, // gitleaks is deterministic
			Scope:        "file",
			Evidence: []domain.Evidence{{
				Kind:        "gitleaks",
				Description: gf.Description,
				Location:    fmt.Sprintf("%s:%d", file, gf.StartLine),
			}},
		})
	}

	status := domain.StatusPass
	if len(out) > 0 {
		status = domain.StatusBlock
	}
	return domain.CheckResult{Name: c.Name(), Status: status, Findings: out}, nil
}

// fallback degrades to the in-house kern secret scanner when gitleaks is not
// installed, and flags the degradation with a WARN finding so it is never
// silent. An in-house BLOCK or ERROR wins over the fallback WARN (fail
// closed, monotonic); anything else surfaces the WARN fallback signal.
func (c *Check) fallback(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	res := domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}
	if c.client != nil {
		inner, err := kern.NewSecretCheck(c.client).Run(ctx, req)
		if err != nil {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "in-house fallback: " + err.Error()}, nil
		}
		res = inner
		res.Name = c.Name()
	}
	res.Findings = append(res.Findings, domain.Finding{
		RuleID:      "secret:incumbent-unavailable",
		Severity:    domain.SeverityWarn,
		Category:    domain.CategorySecret,
		Message:     "gitleaks not found; falling back to in-house check",
		Explanation: "The gitleaks binary is not installed (set GITLEAKS_BINARY or add gitleaks to $PATH). Results come from the in-house kern secret scanner instead. Install gitleaks to get its broader rule coverage.",
		RuleVersion: "1",
		Confidence:  1.0,
		Scope:       "repo",
		Evidence: []domain.Evidence{{
			Kind:        "fallback",
			Description: "gitleaks unavailable; in-house kern secret check used",
		}},
	})
	if res.Status != domain.StatusBlock && res.Status != domain.StatusError {
		res.Status = domain.StatusWarn
	}
	return res, nil
}

// gitleaksVersion returns the installed gitleaks version (best-effort, probed
// once per check instance via `gitleaks version`). An empty string on probe
// failure means findings are stamped without a version rather than failing
// the validation.
func (c *Check) gitleaksVersion(ctx context.Context) string {
	if c.versionSet {
		return c.version
	}
	c.versionSet = true
	if c.binary == "" {
		return ""
	}
	out, _, code, err := c.runner(ctx, c.binary, []string{"version"}, "")
	if err != nil || code != 0 {
		return ""
	}
	c.version = strings.TrimSpace(out)
	return c.version
}

// resolveBinary locates the gitleaks executable: GITLEAKS_BINARY env var,
// then `gitleaks` on $PATH. Returns "" when gitleaks is unavailable (the
// check then falls back to the in-house kern secret scanner).
func resolveBinary() string {
	if p := os.Getenv("GITLEAKS_BINARY"); p != "" {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
		return ""
	}
	if p, err := exec.LookPath("gitleaks"); err == nil {
		return p
	}
	return ""
}

// normalizePath canonicalizes a path for set membership and reporting:
// forward slashes, no leading "./" or "/".
func normalizePath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	return strings.TrimLeft(p, "/")
}

// execCommand is the default commandRunner. It runs the process with the
// given working directory and returns the captured stdout, stderr, and exit
// code. err is non-nil only for launch failures, not for non-zero exits.
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
