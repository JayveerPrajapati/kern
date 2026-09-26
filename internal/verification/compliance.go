package verification

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Compliance check kinds (opt-in; never part of the default run):
//
//   - VerifyCVE     — shells out to `govulncheck -json` (binary on PATH,
//     override with KERN_GOVULNCHECK) and reports OSV-style vulnerabilities.
//     Advisory: findings never fail the verdict.
//   - VerifyLicense — deterministic, in-process license classification from
//     go.mod + vendor/modules.txt + local LICENSE files. No network.
//   - VerifySecrets — scans `git log --all -p` for committed secrets with a
//     high-precision regex set. Advisory: findings never fail the verdict.
//
// All three are SKIPPED (never FAIL) when their precondition cannot be met
// (binary absent, no manifest, not a git repo).

const (
	// cveSkipInstallHint is the SKIPPED detail stamped when govulncheck is not
	// installed or cannot start — a missing scanner is an operational gap, not
	// a verification failure.
	cveSkipInstallHint = "govulncheck not installed — go install golang.org/x/vuln/cmd/govulncheck@latest"

	// cveSummaryCap truncates a vulnerability summary to ~200 chars so a
	// rendered finding stays scannable.
	cveSummaryCap = 200
)

// ---- CVE (govulncheck) ----

// VerifyCVE runs `govulncheck -json` in the project root and parses its
// OSV-style output. The binary is resolved from KERN_GOVULNCHECK (when set)
// or PATH. govulncheck performs its own vulnerability-DB fetch; kern itself
// never touches the network. Findings are advisory (WARN), never blocking.
// When the binary is missing, cannot start, or its fetch fails, the check is
// SKIPPED with an actionable detail — never a hard failure.
func (e *Engine) VerifyCVE() *CVEResult {
	res := &CVEResult{OK: true}
	bin := os.Getenv("KERN_GOVULNCHECK")
	if bin == "" {
		bin = "govulncheck"
	}
	cmd := exec.Command(bin, "-json")
	cmd.Dir = e.root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		// govulncheck exits non-zero when it FINDS vulnerabilities (its own
		// contract: 0 = clean, 3 = findings, 1 = error). Only an empty stdout
		// with a start/fetch failure is a SKIPPED; findings still parse below.
		if len(bytes.TrimSpace(stdout.Bytes())) == 0 {
			res.Status = StatusSkipped
			res.Detail = cveSkipDetail(stderr.String(), runErr)
			return res
		}
	}
	vulns, err := parseGovulncheckJSON(stdout.Bytes())
	if err != nil {
		res.Status = StatusSkipped
		res.Detail = "govulncheck output could not be parsed: " + clip(err.Error(), 200)
		return res
	}
	for _, v := range vulns {
		res.Findings = append(res.Findings, v)
	}
	res.Count = len(res.Findings)
	return res
}

// cveSkipDetail renders the SKIPPED reason: the canonical install hint when
// the binary is missing, otherwise the govulncheck stderr tail (capped 500
// chars — e.g. a vulnerability-DB fetch failure surfaced by govulncheck).
func cveSkipDetail(stderr string, runErr error) string {
	if runErr != nil && errors.Is(runErr, exec.ErrNotFound) {
		return cveSkipInstallHint
	}
	tail := strings.TrimSpace(stderr)
	if tail == "" {
		if runErr != nil {
			return cveSkipInstallHint + " (govulncheck failed to start)"
		}
		return cveSkipInstallHint
	}
	if len(tail) > 500 {
		tail = tail[len(tail)-500:]
	}
	return "govulncheck failed: " + tail
}

// govulncheckJSON mirrors the subset of govulncheck's -json output the check
// consumes (OSV-style: top-level Vulnerabilities, each carrying ID, Details
// and a nested OSV.affected package/range description).
type govulncheckJSON struct {
	Error           string `json:"Error"`
	Vulnerabilities []struct {
		ID      string `json:"ID"`
		Details string `json:"Details"`
		OSV     struct {
			Affected []struct {
				Package struct {
					Name string `json:"name"`
				} `json:"package"`
				Ranges []struct {
					Events []struct {
						Introduced string `json:"introduced"`
						Fixed      string `json:"fixed"`
					} `json:"events"`
				} `json:"ranges"`
			} `json:"affected"`
		} `json:"OSV"`
	} `json:"Vulnerabilities"`
}

// parseGovulncheckJSON decodes govulncheck's JSON output into the ordered
// per-vulnerability details. A fetch failure surfaces as a top-level Error
// (exit 1) and is reported as an error so the caller marks the check SKIPPED.
func parseGovulncheckJSON(data []byte) ([]CVEFinding, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("empty govulncheck output")
	}
	var g govulncheckJSON
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, err
	}
	if g.Error != "" {
		return nil, errors.New(clip(g.Error, 500))
	}
	var out []CVEFinding
	for _, v := range g.Vulnerabilities {
		f := CVEFinding{ID: v.ID, Summary: clip(v.Details, cveSummaryCap)}
		for _, a := range v.OSV.Affected {
			if f.Module == "" {
				f.Module = a.Package.Name
			}
			for _, r := range a.Ranges {
				for _, ev := range r.Events {
					switch {
					case ev.Introduced != "" && f.Introduced == "":
						f.Introduced = ev.Introduced
					case ev.Fixed != "":
						f.Fixed = ev.Fixed
					}
				}
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// clip truncates s to n runes with an ellipsis marker.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---- License (deterministic local classifier) ----

// licenseSignatures is the high-precision classifier: a license is identified
// only when EVERY signature matches (AND semantics) after lower-casing, so
// near-misses and ambiguous texts classify as "unknown" instead of guessing.
// Ordering matters: more specific licenses (ISC, LGPL, BSD-3) are checked
// before their supersets (MIT, GPL, BSD-2).
var licenseSignatures = []struct {
	name string
	sigs []string
}{
	{"Unlicense", []string{"this is free and unencumbered software released into the public domain"}},
	{"ISC", []string{"permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted"}},
	{"MIT", []string{"permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files"}},
	{"Apache-2.0", []string{"apache license", "version 2.0"}},
	{"MPL-2.0", []string{"mozilla public license version 2.0"}},
	{"LGPL-3.0", []string{"lesser general public license", "version 3"}},
	{"LGPL-2.1", []string{"lesser general public license", "version 2.1"}},
	{"GPL-3.0", []string{"general public license", "version 3"}},
	{"GPL-2.0", []string{"general public license", "version 2, june 1991"}},
	{"BSD-3-Clause", []string{"redistribution and use in source and binary forms", "redistributions of source code must retain", "neither the name"}},
	{"BSD-2-Clause", []string{"redistribution and use in source and binary forms", "redistributions of source code must retain"}},
}

// copyleftLicenses are the copyleft families flagged WARN by the license
// check (GPL/LGPL/MPL); permissive licenses are informational only.
var copyleftLicenses = map[string]bool{
	"GPL-2.0": true, "GPL-3.0": true,
	"LGPL-2.1": true, "LGPL-3.0": true,
	"MPL-2.0": true,
}

// classifyLicense classifies a license text by the high-precision signatures.
// It returns "unknown" for any text that does not satisfy a full signature
// set — deterministic and false-positive-averse. Whitespace runs (including
// line breaks) are normalized to single spaces before matching so signatures
// survive arbitrary wrapping.
func classifyLicense(text string) string {
	low := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	for _, ls := range licenseSignatures {
		ok := true
		for _, sig := range ls.sigs {
			if !strings.Contains(low, sig) {
				ok = false
				break
			}
		}
		if ok {
			return ls.name
		}
	}
	return "unknown"
}

// knownLicenses is a deterministic best-effort fallback for modules whose
// LICENSE text is NOT available locally (the common case: a go.mod without a
// vendor/ tree, where every dependency would otherwise classify "unknown").
// It maps famous module paths / path prefixes to their well-known license.
// Deterministic and false-positive-averse: anything not in the map stays
// "unknown" rather than guessed (F7).
var knownLicenses = []struct {
	prefix string
	lic    string
}{
	{"golang.org/x/", "BSD-3-Clause"},
	{"google.golang.org/", "Apache-2.0"},
	{"github.com/aws/", "Apache-2.0"},
	{"github.com/docker/", "Apache-2.0"},
	{"github.com/google/uuid", "BSD-3-Clause"},
	{"github.com/stretchr/testify", "MIT"},
	{"github.com/sirupsen/logrus", "MIT"},
	{"github.com/spf13/cobra", "Apache-2.0"},
	{"github.com/spf13/pflag", "BSD-3-Clause"},
	{"github.com/spf13/viper", "MIT"},
	{"github.com/pkg/errors", "BSD-2-Clause"},
	{"github.com/prometheus/client_golang", "Apache-2.0"},
	{"github.com/gorilla/mux", "BSD-3-Clause"},
	{"github.com/golang/protobuf", "BSD-3-Clause"},
	{"github.com/gin-gonic/gin", "MIT"},
	{"github.com/labstack/echo", "MIT"},
	{"gopkg.in/yaml.v3", "MIT"},
	{"gopkg.in/yaml.v2", "Apache-2.0"},
}

// knownLicense resolves a module path against the known-license map: the
// first matching entry (longest-prefix sensitive via ordering) or "".
func knownLicense(path string) string {
	for _, k := range knownLicenses {
		if strings.HasPrefix(path, k.prefix) {
			return k.lic
		}
	}
	return ""
}

// VerifyLicense parses go.mod (module list) and, when present,
// vendor/modules.txt, then classifies each module's LICENSE text from the
// vendor tree (or the repo root for the main module). It is fully in-process
// and deterministic — no network, no subprocesses. A project with neither
// go.mod nor vendor/ is an honest SKIPPED ("no vendor/ or go.mod found").
// Unknown and copyleft licenses are WARN-level detail lines; the check itself
// never fails. Modules whose license text is not locally available fall back
// to the known-license map for famous module paths (F7).
func (e *Engine) VerifyLicense() *LicenseResult {
	res := &LicenseResult{OK: true}
	gomod := filepath.Join(e.root, "go.mod")
	vendorDir := filepath.Join(e.root, "vendor")
	modulesTxt := filepath.Join(vendorDir, "modules.txt")

	var mods []licenseModule
	seen := map[string]bool{}
	add := func(path string, main bool) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		mods = append(mods, licenseModule{Path: path, Main: main})
	}

	if data, err := os.ReadFile(gomod); err == nil {
		main := modulePath(data)
		add(main, true)
		for _, m := range parseRequires(gomod) {
			add(m, false)
		}
	}
	if data, err := os.ReadFile(modulesTxt); err == nil {
		for _, m := range parseModulesTxt(data) {
			add(m, false)
		}
	}
	if len(mods) == 0 {
		res.Skipped = "no vendor/ or go.mod found"
		return res
	}

	hasVendor := false
	if _, err := os.Stat(modulesTxt); err == nil {
		hasVendor = true
	}
	for _, m := range mods {
		lic := ""
		var licDir string
		switch {
		case m.Main:
			licDir = e.root
		case hasVendor:
			licDir = filepath.Join(vendorDir, m.Path)
		}
		if licDir != "" {
			lic = classifyLicense(findLicenseText(licDir))
		}
		if lic == "" || lic == "unknown" {
			// No local LICENSE text matched a signature. Fall back to the
			// known-license map before declaring "unknown" — a go.mod-only
			// project would otherwise flag every dependency.
			if k := knownLicense(m.Path); k != "" {
				lic = k
			}
		}
		entry := LicenseEntry{Module: m.Path, License: lic}
		if lic == "" || lic == "unknown" {
			entry.License = "unknown"
			res.Findings = append(res.Findings, "unknown license: "+m.Path)
		} else if copyleftLicenses[lic] {
			res.Findings = append(res.Findings, fmt.Sprintf("copyleft: %s (%s)", m.Path, lic))
		}
		res.Modules = append(res.Modules, entry)
	}
	return res
}

// licenseModule is one module whose license must be classified.
type licenseModule struct {
	Path string
	Main bool // the go.mod module itself — license looked up at the repo root
}

// parseModulesTxt extracts the module paths from a vendor/modules.txt:
// lines starting with a single "# " are module headers ("# <path> <version>").
func parseModulesTxt(data []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "# "))
		if len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return out
}

// findLicenseText locates the LICENSE text in dir — LICENSE*, LICENCE* or
// COPYING* files (case-insensitive, sorted for determinism) — and returns
// its content; "" when no license file exists.
func findLicenseText(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var names []string
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := strings.ToUpper(de.Name())
		if strings.HasPrefix(name, "LICENSE") || strings.HasPrefix(name, "LICENCE") || strings.HasPrefix(name, "COPYING") {
			names = append(names, de.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	data, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		return ""
	}
	return string(data)
}

// ---- Secrets (committed-secret history scanner) ----

// secretScanLineCap stops the git log stream after this many lines, and
// secretScanCommitCap after this many commits — the scan is deterministic-fast
// on large histories and the cap is surfaced in the result detail.
const (
	secretScanLineCap   = 200_000
	secretScanCommitCap = 2_000
)

// secretPatterns is the HIGH-PRECISION regex set. Generic JWT-shaped secrets
// are deliberately NOT matched unless clearly provider-prefixed
// (github_pat_), so plausible-but-generic strings never become noise.
var secretPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"aws-access-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"github-pat", regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`)},
	{"github-pat-fine-grained", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`)},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"private-key", regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`)},
}

// hunkHeaderRe parses a unified-diff hunk header so finding line numbers
// track the added-file side: "@@ -a,b +c,d @@" captures the new-side start.
var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// VerifySecrets scans the full commit history (`git log --all -p --no-color`)
// for committed secrets using the high-precision pattern set. The stream is
// capped at 200k lines / 2000 commits (reported in the detail). Findings carry
// the short commit hash, file path, line, and a MASKED snippet (first 4 chars
// + "…" + last 4) — never the raw secret. Binary blobs are naturally omitted
// by git log -p. A non-git root (or missing git) is an honest SKIPPED.
func (e *Engine) VerifySecrets() *SecretsResult {
	res := &SecretsResult{OK: true}
	cmd := exec.Command("git", "log", "--all", "-p", "--no-color")
	cmd.Dir = e.root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		res.Status = StatusSkipped
		res.Detail = "secrets scan could not run: " + err.Error()
		return res
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		res.Status = StatusSkipped
		res.Detail = secretsSkipDetail(stderr.String())
		return res
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	lines, commits := 0, 0
	curCommit, curFile := "", ""
	fileLine := 0
	capped := ""
scan:
	for sc.Scan() {
		line := sc.Text()
		lines++
		switch {
		case strings.HasPrefix(line, "commit "):
			commits++
			curCommit = shortHash(strings.TrimPrefix(line, "commit "))
			curFile = ""
			fileLine = 0
			if commits > secretScanCommitCap {
				capped = fmt.Sprintf("scan capped at %d commits", secretScanCommitCap)
				break scan
			}
		case strings.HasPrefix(line, "+++ b/"):
			curFile = strings.TrimPrefix(line, "+++ b/")
			fileLine = 0
		case strings.HasPrefix(line, "@@ "):
			if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
				fileLine = atoiOr(m[2], 1) - 1
			}
		case strings.HasPrefix(line, " "), strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"):
			fileLine++
			content := strings.TrimPrefix(strings.TrimPrefix(line, "+"), "-")
			for _, p := range secretPatterns {
				if loc := p.re.FindStringIndex(content); loc != nil {
					res.Findings = append(res.Findings, SecretFinding{
						Commit:  curCommit,
						File:    curFile,
						Line:    fileLine,
						Kind:    p.kind,
						Snippet: maskSecret(content[loc[0]:loc[1]]),
					})
					res.Count++
					break // one finding per line keeps the report scannable
				}
			}
		}
		if lines >= secretScanLineCap {
			capped = fmt.Sprintf("scan capped at %d lines", secretScanLineCap)
			break scan
		}
	}
	// Stop the subprocess once the cap is reached (it may still be writing).
	// On the normal path the scanner read to EOF, so Wait reaps the exit
	// status — a non-zero exit with nothing scanned (e.g. "not a git
	// repository") is an honest SKIPPED, never a fabricated clean scan.
	var waitErr error
	if capped != "" {
		_ = cmd.Process.Kill()
		waitErr = cmd.Wait()
	} else {
		waitErr = cmd.Wait()
	}
	if waitErr != nil && capped == "" && res.Count == 0 && strings.TrimSpace(stderr.String()) != "" {
		res.Status = StatusSkipped
		res.Detail = secretsSkipDetail(stderr.String())
		return res
	}
	switch {
	case capped != "":
		res.Detail = capped
	case res.Count > 0:
		res.Detail = fmt.Sprintf("found %d secret(s) in commit history", res.Count)
	default:
		res.Detail = "no secrets found in commit history"
	}
	return res
}

// secretsSkipDetail renders the SKIPPED reason for a secrets scan that could
// not start (e.g. the root is not a git repository).
func secretsSkipDetail(stderr string) string {
	tail := strings.TrimSpace(stderr)
	if tail == "" {
		return "git log could not run (is this a git repository?)"
	}
	if len(tail) > 500 {
		tail = tail[len(tail)-500:]
	}
	return "git log failed: " + tail
}

// shortHash renders the abbreviated commit hash (first 7 chars, like git's
// default --abbrev-commit).
func shortHash(h string) string {
	h = strings.TrimSpace(h)
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// maskSecret renders a secret as first 4 chars + "…" + last 4 — the raw value
// never leaves the scanner. Very short strings are fully masked.
func maskSecret(s string) string {
	const shown = 4
	if len(s) <= shown*2 {
		return "****"
	}
	return s[:shown] + "…" + s[len(s)-shown:]
}

// atoiOr parses n as an int, falling back to def.
func atoiOr(n string, def int) int {
	v := 0
	for _, r := range n {
		if r < '0' || r > '9' {
			return def
		}
		v = v*10 + int(r-'0')
	}
	if v == 0 {
		return def
	}
	return v
}
