package kern

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// SecretCheck scans changed files for hardcoded secrets by running
// `kern sec --json <repo-root>` once against the repository directory (kern
// sec does NOT scan individual files — it only works on directories) and
// filtering the findings to only the changed files in the ChangeRequest.
//
// The check is read-only and never mutates the repository.
//
// Phase 3 additions (spec lines 780-797):
//   - Secret category extracted from kern's message field ("hardcoded secret: AWS")
//     and surfaced as a typed Category in the Finding.
//   - Allowlist: files in testdata/ directories or with _test.go suffix are
//     suppressed (spec: "support allowlisted placeholders/test fixtures explicitly").
//   - Redaction: the raw snippet from kern is NEVER propagated into domain.Finding
//     (spec: "never echo the secret itself into agent feedback").
type SecretCheck struct {
	client    *KernClient
	allowlist Allowlist
}

// Allowlist decides whether a finding in a given file should be suppressed.
// The default allowlist suppresses test fixtures (testdata/ dirs, _test.go files).
type Allowlist interface {
	IsAllowed(filePath string) bool
}

// DefaultAllowlist suppresses findings in test fixtures: files under a
// testdata/ directory, files with a _test.go suffix, and files with a
// .test.{js,ts,py,rb} suffix.
type DefaultAllowlist struct{}

// IsAllowed returns true for test-fixture paths that should not be flagged.
func (DefaultAllowlist) IsAllowed(path string) bool {
	lower := strings.ToLower(path)
	// testdata/ directory convention (Go, and common across languages).
	if strings.Contains(lower, "/testdata/") || strings.HasPrefix(lower, "testdata/") {
		return true
	}
	// Go test files.
	if strings.HasSuffix(lower, "_test.go") {
		return true
	}
	// JS/TS/Python/Ruby test files.
	for _, suffix := range []string{".test.js", ".test.jsx", ".test.ts", ".test.tsx", ".spec.js", ".spec.ts", "_spec.rb"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// NewSecretCheck constructs a SecretCheck backed by client with the default
// allowlist.
func NewSecretCheck(client *KernClient) *SecretCheck {
	return &SecretCheck{client: client, allowlist: DefaultAllowlist{}}
}

// NewSecretCheckWithAllowlist constructs a SecretCheck with a custom allowlist.
func NewSecretCheckWithAllowlist(client *KernClient, allowlist Allowlist) *SecretCheck {
	return &SecretCheck{client: client, allowlist: allowlist}
}

// Name returns the stable check identifier used for policy routing.
func (c *SecretCheck) Name() string { return "secret:scan" }

// Run scans for secrets and filters to changed files. Files with proposed
// content (Content != "", pre-write) are written to a temp directory and
// scanned there, because they are not on disk yet; the rest keep the
// repository-directory scan. Findings in allowlisted (test fixture) files are
// suppressed. All findings are redacted: the raw snippet never propagates
// into Blueprint results.
// Phase 2 hardening: repeated validations over the same unchanged staged files
// replay cached findings instead of re-running the whole-repo scan. `kern sec`
// only scans directories, so a per-file cache keyed by (size, mtime) makes
// repeat runs on an unchanged staged set free.
func (c *SecretCheck) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if req.RepositoryRoot == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}

	// No changed files => nothing can be reported: PASS immediately without
	// scanning (observable behavior identical, but no pointless whole-repo
	// scan on an empty staged set).
	if len(req.Files) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}

	// Split proposed-content files (Content != "") from disk files (Content
	// == ""). Proposed content is not on disk, so it is scanned from a temp
	// dir; disk files keep the existing repo scan + cache behavior.
	var contentFiles, diskFiles []domain.FileChange
	for _, fc := range req.Files {
		if fc.Content != "" {
			contentFiles = append(contentFiles, fc)
		} else {
			diskFiles = append(diskFiles, fc)
		}
	}

	// Paths that carry proposed content: excluded from the disk-scan filter so
	// a file present on disk AND in the proposal is not double-reported (the
	// proposed content is the authoritative version being validated).
	contentSet := make(map[string]bool, len(contentFiles))
	for _, fc := range contentFiles {
		contentSet[normalizePath(fc.Path)] = true
	}

	var findings []domain.Finding

	// 1. Proposed content scan: never on disk, so scan from a temp dir.
	if len(contentFiles) > 0 {
		contentFindings, err := c.scanContent(ctx, contentFiles)
		if err != nil {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: err.Error()}, nil
		}
		findings = append(findings, contentFindings...)
	}

	// 2. Disk scan: unchanged behavior, minus the content-bearing paths.
	if len(diskFiles) > 0 {
		diskFindings, err := c.scanDisk(ctx, req, diskFiles, contentSet)
		if err != nil {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: err.Error()}, nil
		}
		findings = append(findings, diskFindings...)
	}

	// Dedupe by (normalized file, line), keeping the FIRST occurrence: kern
	// emits multiple rules for one line (e.g. STRIPE + TOKEN on the same
	// line) and one finding per line is the intended UX.
	findings = dedupeFindings(findings)

	status := domain.StatusPass
	for _, f := range findings {
		switch f.Severity {
		case domain.SeverityBlock:
			status = domain.StatusBlock
		case domain.SeverityWarn:
			if status == domain.StatusPass {
				status = domain.StatusWarn
			}
		}
	}
	return domain.CheckResult{Name: c.Name(), Status: status, Findings: findings}, nil
}

// scanContent writes proposed file content into a temp directory — under each
// file's exact repo-relative path — and runs one `kern sec` scan against it,
// so proposed content that is not yet on disk is still checked. Findings are
// filtered to the proposed file paths; finding.File is the temp-relative path,
// which equals the repo-relative path by construction, so it is reported
// unchanged. The temp dir is removed before returning (the check stays
// read-only with respect to the repository).
func (c *SecretCheck) scanContent(ctx context.Context, contentFiles []domain.FileChange) ([]domain.Finding, error) {
	tmpDir, err := os.MkdirTemp("", "blueprint-sec-content-*")
	if err != nil {
		return nil, fmt.Errorf("scan proposed content: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	for _, fc := range contentFiles {
		// Confine proposed paths to the temp dir: reject anything that would
		// escape it via ".." or an absolute path.
		rel := filepath.Clean(fc.Path)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("invalid path in proposed files: %q", fc.Path)
		}
		dest := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, fmt.Errorf("scan proposed content: mkdir: %w", err)
		}
		if err := os.WriteFile(dest, []byte(fc.Content), 0o644); err != nil {
			return nil, fmt.Errorf("scan proposed content: write %s: %w", fc.Path, err)
		}
	}

	// kern sec only scans directories; scan the temp dir once and filter to
	// the proposed file paths (same call shape as the repo scan).
	allFindings, _, _, scanErr := c.client.SecScan(ctx, tmpDir, ".")
	if scanErr != nil {
		return nil, fmt.Errorf("scan proposed content: %w", scanErr)
	}

	proposedSet := make(map[string]bool, len(contentFiles))
	for _, fc := range contentFiles {
		proposedSet[normalizePath(fc.Path)] = true
	}

	var findings []domain.Finding
	for _, sf := range allFindings {
		normFile := normalizePath(sf.File)

		// Only report findings in proposed files (new-change principle).
		if !proposedSet[normFile] {
			continue
		}

		// Suppress allowlisted test fixtures.
		if c.allowlist != nil && c.allowlist.IsAllowed(normFile) {
			continue
		}

		findings = append(findings, c.findingFromSec(sf))
	}
	return findings, nil
}

// scanDisk scans the repository directory once and filters findings to the
// given disk files (Content == ""), excluding any path in excludeSet (files
// that also carry proposed content). The per-file stat cache makes repeat
// runs over an unchanged staged set free.
func (c *SecretCheck) scanDisk(ctx context.Context, req domain.ChangeRequest, diskFiles []domain.FileChange, excludeSet map[string]bool) ([]domain.Finding, error) {
	diskReq := req
	diskReq.Files = diskFiles

	cachePath := secCachePath(req.RepositoryRoot)
	cache := loadSecCache(cachePath)

	// Fast path: every disk file still matches its cached stat -> replay
	// the cached findings without invoking kern at all.
	cachedFindings, allMatched := c.replayFromCache(cache, diskReq)
	if !allMatched {
		// Full scan: kern sec only works on directories, so scan the whole
		// repo once and filter to the disk files below.
		allFindings, _, _, scanErr := c.client.SecScan(ctx, req.RepositoryRoot, ".")
		if scanErr != nil {
			return nil, scanErr
		}
		cachedFindings = allFindings
		// Rebuild the cache from this scan's findings plus the scanned disk
		// files (every scanned file gets an entry keyed by its current stat;
		// clean files get an entry with zero findings so they are cache hits
		// on the next run; entries for files that no longer exist are
		// dropped) and persist it. Best-effort: a failed save must never fail
		// the validation.
		rebuildSecCache(cachePath, req.RepositoryRoot, allFindings, diskFiles, excludeSet)
	}

	// Build a set of disk file paths for fast lookup. We normalize to
	// forward slashes and relative paths.
	diskSet := make(map[string]bool, len(diskFiles))
	for _, fc := range diskFiles {
		diskSet[normalizePath(fc.Path)] = true
	}

	var findings []domain.Finding
	for _, sf := range cachedFindings {
		normFile := normalizePath(sf.File)

		// Only report findings in changed files (new-change principle).
		if !diskSet[normFile] {
			continue
		}

		// Skip paths that also carry proposed content: their authoritative
		// version was already scanned above (avoids double-reporting when a
		// file exists on disk AND has proposed content).
		if excludeSet[normFile] {
			continue
		}

		// Suppress allowlisted test fixtures.
		if c.allowlist != nil && c.allowlist.IsAllowed(normFile) {
			continue
		}

		findings = append(findings, c.findingFromSec(sf))
	}
	return findings, nil
}

// findingFromSec maps one kern sec finding into a domain.Finding. Redaction
// rule is absolute: the raw snippet from kern is NEVER propagated (spec:
// "never echo the secret itself into agent feedback").
func (c *SecretCheck) findingFromSec(sf SecFinding) domain.Finding {
	category := extractSecretCategory(sf.Message)
	return domain.Finding{
		RuleID:       "secret:" + sf.Rule,
		Severity:     severityFromSec(sf.Severity),
		Category:     domain.CategorySecret,
		File:         sf.File,
		Line:         sf.Line,
		Message:      sf.Message,
		Explanation:  fmt.Sprintf("Secret scanner detected a potential %s. The secret value has been redacted and must not be committed. Move the credential to runtime secret storage (env var, vault, or secret manager).", category),
		SuggestedFix: "Move the credential to runtime secret storage (environment variable, vault, or secret manager).",
		Redacted:     true, // CRITICAL: snippet never propagates.
		RuleVersion:  "1",
		Confidence:   0.95,
		Scope:        "file",
		Evidence: []domain.Evidence{{
			Kind:        "pattern-match",
			Description: fmt.Sprintf("rule: %s, category: %s", sf.Rule, category),
			Location:    fmt.Sprintf("%s:%d", sf.File, sf.Line),
		}},
	}
}

// replayFromCache returns the union of cached findings for the staged files
// and true when EVERY staged file has a cache entry whose size+mtime still
// match the file on disk (a missing file on disk counts as a mismatch). On
// any mismatch it returns nil, false and the caller must re-scan.
func (c *SecretCheck) replayFromCache(cache secCache, req domain.ChangeRequest) ([]SecFinding, bool) {
	var findings []SecFinding
	for _, fc := range req.Files {
		key := normalizePath(fc.Path)
		entry, ok := cache.Files[key]
		if !ok {
			return nil, false
		}
		info, err := os.Stat(filepath.Join(req.RepositoryRoot, fc.Path))
		if err != nil {
			return nil, false // file gone: cannot trust the cached identity
		}
		if info.Size() != entry.Size || info.ModTime().UnixNano() != entry.MTimeNS {
			return nil, false
		}
		findings = append(findings, entry.Findings...)
	}
	return findings, true
}

// rebuildSecCache rebuilds the cache from a full scan's findings plus the
// list of scanned files. Every scanned file gets a cache entry keyed by its
// normalized path with the file's current stat — including clean files, which
// get an entry with zero findings (negative caching), so an unchanged clean
// staged set replays from cache instead of forcing a whole-repo rescan.
// Findings for files outside the scanned set are still cached (prior
// behavior). Files that no longer exist are dropped. Save failures are
// ignored (best-effort).
func rebuildSecCache(path, root string, findings []SecFinding, scanned []domain.FileChange, excludeSet map[string]bool) {
	entries := make(map[string]secCacheEntry, len(scanned))
	// Negative caching: seed every scanned file with an empty-findings entry
	// so clean files are cache hits on the next run, not cache misses.
	for _, fc := range scanned {
		key := normalizePath(fc.Path)
		if excludeSet[key] {
			continue // proposed-content files are filtered at replay time
		}
		if _, ok := entries[key]; ok {
			continue
		}
		info, err := os.Stat(filepath.Join(root, fc.Path))
		if err != nil {
			continue // file gone since the scan: drop it
		}
		entries[key] = secCacheEntry{Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
	}
	for _, sf := range findings {
		key := normalizePath(sf.File)
		e, ok := entries[key]
		if !ok {
			info, err := os.Stat(filepath.Join(root, sf.File))
			if err != nil {
				continue // file gone since the scan: drop its findings
			}
			e = secCacheEntry{Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
		}
		e.Findings = append(e.Findings, sf)
		entries[key] = e
	}
	_ = saveSecCache(path, secCache{Version: secCacheVersion, Files: entries})
}

// dedupeFindings removes duplicate findings with the same normalized file and
// line, keeping the FIRST occurrence.
func dedupeFindings(findings []domain.Finding) []domain.Finding {
	if len(findings) < 2 {
		return findings
	}
	seen := make(map[string]bool, len(findings))
	out := make([]domain.Finding, 0, len(findings))
	for _, f := range findings {
		key := normalizePath(f.File) + ":" + strconv.Itoa(f.Line)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

// extractSecretCategory parses the secret category from kern's message field.
// kern messages have the form "hardcoded secret: <LABEL>" where LABEL is the
// pii pattern label (AWS, PASSWORD, PRIVATE_KEY, GITHUB, etc.). If the message
// doesn't match this pattern, returns "unknown".
func extractSecretCategory(message string) string {
	// kern's hardcoded-secret messages: "hardcoded secret: AWS"
	if strings.HasPrefix(message, "hardcoded secret:") {
		label := strings.TrimSpace(strings.TrimPrefix(message, "hardcoded secret:"))
		if label != "" {
			return humanizeSecretLabel(label)
		}
	}
	// Other security rules (sql-injection, etc.) — return the rule ID.
	return "unknown"
}

// humanizeSecretLabel converts a kern pii label (e.g. "AWS_SECRET",
// "PRIVATE_KEY") into a human-readable category (e.g. "AWS secret access key",
// "private key") for the spec's "Type:" field.
func humanizeSecretLabel(label string) string {
	switch label {
	case "AWS":
		return "AWS access key"
	case "AWS_SECRET":
		return "AWS secret access key"
	case "GITHUB":
		return "GitHub token"
	case "GITHUB_PAT":
		return "GitHub personal access token"
	case "SLACK":
		return "Slack token"
	case "STRIPE":
		return "Stripe API key"
	case "OPENAI":
		return "OpenAI API key"
	case "JWT":
		return "JWT"
	case "BEARER":
		return "Bearer token"
	case "KEY":
		return "API key or token"
	case "PASSWORD":
		return "password"
	case "TOKEN":
		return "token or secret"
	case "PRIVATE_KEY":
		return "private key"
	case "URL_CRED":
		return "URL with embedded credentials"
	case "EMAIL":
		return "email address (PII)"
	case "IP":
		return "IP address (PII)"
	case "PHONE":
		return "phone number (PII)"
	case "SSN":
		return "Social Security number (PII)"
	default:
		return strings.ToLower(label)
	}
}

// normalizePath normalizes a file path to forward-slash, relative form for
// comparison. It strips leading "./" and converts OS-specific separators.
func normalizePath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	return p
}

// severityFromSec maps kern's textual severities onto domain severities.
// Unknown severities map to Info so they can never escalate a result.
func severityFromSec(s string) domain.Severity {
	switch s {
	case "error":
		return domain.SeverityBlock
	case "warning":
		return domain.SeverityWarn
	case "info":
		return domain.SeverityInfo
	default:
		return domain.SeverityInfo
	}
}
