// Package sec scans source files for common security anti-patterns: hardcoded
// secrets, dynamic SQL, shell command injection, weak crypto, insecure
// randomness and unsafe deserialization. It is 100% local, deterministic and
// line-scoped, so findings map to a single file:line.
package sec

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/ignore"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/pii"
)

// Severity of a finding, ordered error > warning > info.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Rule describes one detection rule. RE is matched byte-wise against the whole
// file; matches are mapped to line numbers so every finding is line-scoped.
type Rule struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Summary  string   `json:"summary"`
	RE       *regexp.Regexp
	Label    string `json:"label,omitempty"`
}

// Finding is one detected issue at a concrete file:line.
type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Snippet  string `json:"snippet,omitempty"`
}

var (
	reDynamicSQL = regexp.MustCompile(`(?i)(?:\.(?:Query|QueryRow|Exec|Prepare|Execute))\(\s*(?:fmt\.Sprintf|fmt\.Fprintf|fmt\.Errorf|strings\.Join|strings\.ReplaceAll|map\[|\[\]any\{|"(?:[^"\\]|\\.)*"\s*\+)`)

	reCommandInjection = regexp.MustCompile(`(?i)(?:exec\.Command(?:Context)?|CmdContext|spawn)\s*\([^)]*["']?(?:sh|bash|zsh|cmd|powershell|pwsh)["']?\s*,\s*["']-c["']?[^)]*?\+|(?:system\(|popen\(|os\.system\(|exec\()\s*["'][^"']*["']\s*\+`)

	reWeakCrypto = regexp.MustCompile(`(?i)\b(?:md5\.(?:New|Sum|NewHash)|sha1\.(?:New|Sum|NewHash)|DES\.(?:NewCipher)|RC4|digest\.MD5)\b`)

	reInsecureRandom = regexp.MustCompile(`(?i)\b(?:rand\.(?:Intn|Int|Int31n|Uint32|Uint64|Float64)|Math\.random|Random\.randrange|numpy\.random\.rand)\s*\(`)

	reUnsafeDeserialization = regexp.MustCompile(`(?is)\b(?:json\.Unmarshal|yaml\.Unmarshal)\s*\(\s*[^)]*?(?:\binterface\{\}|map\[string\](?:interface\{\}|any)\s*\{)[^)]*\)`)

	reCodeEval = regexp.MustCompile(`(?i)\b(?:pickle\.loads?|yaml\.load|yaml\.unsafe_load|eval\s*\(|new\s+Function\s*\()`)

	reUnsafeReflection = regexp.MustCompile(`\b(?:unsafe\.Pointer|unsafe\.StringData|reflect\.UnsafePointer|unsafe\.Slice)\s*\(`)

	// Config-aware rules: detect misconfigurations in .properties/.yml/.yaml
	// files. These are not source-code patterns — they are config-level bugs
	// that pattern-based source scanners miss entirely.

	// rePlaceholderBug matches $VAR without braces in Spring properties.
	// Spring's ${VAR} syntax resolves env variables; $VAR injects the literal
	// string "$VAR", silently breaking config in production.
	// Matches: key=$VAR  key=$VAR_PATH  url=jdbc://$HOST:$PORT/db
	// Does NOT match: ${VAR} (the '{' after '$' fails [A-Z_]), $$, $ (alone)
	// Note: Go's RE2 has no lookahead; greedy [A-Z0-9_]* already captures the
	// full variable name, and ${VAR} can't match because '{' isn't [A-Z_].
	// The key/prefix class excludes newlines ([^\n=]) so a match never crosses
	// line boundaries — without that guard the regex could start at one line
	// (e.g. "name: foo") and greedily consume newlines until an "=" appears on
	// a much later line (e.g. a shell "run:" step in a GitHub Actions YAML),
	// flagging a CI workflow as a Spring config bug.
	rePlaceholderBug = regexp.MustCompile(`(?m)^[^#\s][^\n=]*=\s*.*\$([A-Z_][A-Z0-9_]*)`)

	// reHardcodedConfigCred matches password/secret/token/key assignments
	// with a literal (non-placeholder) value in config files.
	// Matches: password=secret123  spring.datasource.password=admin
	// Does NOT match: password=${DB_PASSWORD}  password=$ENV
	reHardcodedConfigCred = regexp.MustCompile(`(?i)^[^#=\s]*(?:password|passwd|secret|api[_-]?key|auth[_-]?token|access[_-]?key|private[_-]?key)\s*[=:]\s*([^\s${][^\s#]+)`)

	// reDisabledSsl matches SSL/TLS verification disabled in config files.
	// Matches: feign.client.ssl.verification.enabled=false
	// spring.ssl.bundle.*.keystore.type=NONE (false-positive risk; keep narrow)
	reDisabledSsl = regexp.MustCompile(`(?im)^[^#=\s]*(?:ssl|tls|certificate)[^=]*(?:verify|verification|validation|enabled)\s*=\s*false\b`)
)

// Rules is the deterministic rule set, sorted by ID then summary. The
// hardcoded-secret rule is expanded per pii label so messages stay precise.
var Rules []Rule

func init() {
	for _, p := range pii.DefaultPatterns {
		Rules = append(Rules, Rule{
			ID:       "hardcoded-secret",
			Severity: SeverityError,
			Summary:  "hardcoded secret: " + p.Label,
			RE:       p.RE,
			Label:    p.Label,
		})
	}
	Rules = append(Rules,
		Rule{ID: "sql-injection", Severity: SeverityError, Summary: "dynamic SQL built from variables", RE: reDynamicSQL},
		Rule{ID: "command-injection", Severity: SeverityError, Summary: "shell command built from variables", RE: reCommandInjection},
		Rule{ID: "unsafe-deserialization", Severity: SeverityWarning, Summary: "untrusted input deserialized into untyped/weak types", RE: reUnsafeDeserialization},
		Rule{ID: "code-eval", Severity: SeverityInfo, Summary: "dynamic code execution (eval, pickle, unsafe yaml)", RE: reCodeEval},
		Rule{ID: "unsafe-reflection", Severity: SeverityInfo, Summary: "unsafe memory access via reflect/unsafe", RE: reUnsafeReflection},
		Rule{ID: "insecure-random", Severity: SeverityWarning, Summary: "non-cryptographic randomness for security-relevant data", RE: reInsecureRandom},
		Rule{ID: "weak-crypto", Severity: SeverityWarning, Summary: "deprecated or weak cryptographic primitive", RE: reWeakCrypto},
	)
	sort.SliceStable(Rules, func(i, j int) bool {
		if Rules[i].ID != Rules[j].ID {
			return Rules[i].ID < Rules[j].ID
		}
		return Rules[i].Summary < Rules[j].Summary
	})
}

// ScanFile runs every rule against one source file. rel is a root-relative
// path used only for reporting.
func ScanFile(rel string, src []byte) []Finding {
	var findings []Finding
	for _, r := range Rules {
		for _, idx := range r.RE.FindAllIndex(src, -1) {
			if pii.IsNonSecretIP(r.Label, string(src[idx[0]:idx[1]])) {
				continue
			}
			// PHONE is PII, not a credential: mask it in prompts, but do not
			// report it as a hardcoded secret.
			if r.Label == "PHONE" {
				continue
			}
			// CDNs use pkg@version URLs (e.g. boxicons@2.1.4) whose "@2.1.4"
			// suffix matches the EMAIL pattern. A domain part that is a
			// semantic-version string is not an email address.
			if r.Label == "EMAIL" {
				hit := string(src[idx[0]:idx[1]])
				if at := strings.IndexByte(hit, '@'); at >= 0 && pii.IsVersionLike(hit[at+1:]) {
					continue
				}
			}
			// Deterministic false-positive filters: a scanner that flags its
			// own detector regexes or schema introspection is noise.
			if isRegexLiteral(src, idx[0]) {
				continue
			}
			// Skip matches inside source-code comment lines (// or # after
			// optional whitespace). Documentation examples like
			// "// Matches: password=secret123" are not real credentials.
			if isCommentLine(src, idx[0]) {
				continue
			}
			// EMAIL false-positive guards: emails in HTML placeholder
			// attributes ("placeholder=xyz@gmail.com"), CSS comments
			// (/* email here */) and HTML comments (<!-- email here -->)
			// are not hardcoded secrets — they are UX hints or documentation.
			if r.Label == "EMAIL" && isInertEmailContext(src, idx[0], idx[1]) {
				continue
			}
			// insecure-random false-positive guard: Math.random / rand.Intn
			// used for visual/animation/game effects (fireworks, particles,
			// dice rolls) is not security-relevant. Only flag when a security
			// keyword (token, password, secret, key, session, nonce, csrf,
			// salt, otp, auth) appears within a context window around the
			// match — that is the case the rule's own summary targets.
			if r.ID == "insecure-random" && !hasSecurityContext(src, idx[0]) {
				continue
			}
			if r.ID == "sql-injection" && isPragmaTableInfo(src, idx[0]) {
				continue
			}
			line := lineAt(src, idx[0])
			findings = append(findings, Finding{
				File:     rel,
				Line:     line,
				Rule:     r.ID,
				Severity: string(r.Severity),
				Message:  r.Summary,
				Snippet:  snippet(src, idx[0], idx[1]),
			})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Rule < findings[j].Rule
	})
	return findings
}

// isRegexLiteral reports whether the match at pos sits inside a compiled regex
// literal (regexp.MustCompile / MustCompile on the same line): a scanner must
// not flag its own detector patterns (e.g. the weak-crypto regex mentions md5).
func isRegexLiteral(src []byte, pos int) bool {
	lineStart := bytes.LastIndexByte(src[:pos], '\n') + 1
	lineEnd := bytes.IndexByte(src[pos:], '\n')
	if lineEnd < 0 {
		lineEnd = len(src) // no newline after pos: end of file (absolute index)
	} else {
		lineEnd += pos // convert relative offset to absolute index
	}
	line := src[lineStart:lineEnd]
	return bytes.Contains(line, []byte("regexp.MustCompile"))
}

// isCommentLine reports whether the match at pos sits on a source-code comment
// line — a line whose first non-whitespace characters are // (Go/C/JS/...) or #
// (Shell/Python/Ruby/...). Documentation examples in comments (e.g.
// "// Matches: password=secret123" in this package's own source) are not real
// credentials or config, so findings inside comment lines are suppressed.
func isCommentLine(src []byte, pos int) bool {
	lineStart := bytes.LastIndexByte(src[:pos], '\n') + 1
	trimmed := bytes.TrimLeft(src[lineStart:pos], " \t")
	return bytes.HasPrefix(trimmed, []byte("//")) || bytes.HasPrefix(trimmed, []byte("#"))
}

// isInertEmailContext reports whether an EMAIL match at src[start:end] sits in
// a context where the email is not a hardcoded secret: an HTML placeholder
// attribute (placeholder="xyz@gmail.com"), a CSS comment (/* ... */), or an
// HTML comment (<!-- ... -->). These are UX hints or documentation, not
// credentials committed to source.
func isInertEmailContext(src []byte, start, end int) bool {
	// HTML placeholder attribute: the match is inside a quoted value of a
	// placeholder= attribute. Check for "placeholder" followed by = and a
	// quote before the match position, within a reasonable window.
	windowStart := start - 80
	if windowStart < 0 {
		windowStart = 0
	}
	before := src[windowStart:start]
	if bytes.Contains(bytes.ToLower(before), []byte("placeholder")) {
		// Verify it's an attribute assignment (placeholder= or placeholder =)
		// not just the word "placeholder" in prose. Look for = and a quote
		// in the 30 chars before the match.
		recent := before
		if len(recent) > 30 {
			recent = recent[len(recent)-30:]
		}
		if bytes.ContainsAny(recent, "=") && bytes.ContainsAny(recent, "\"'") {
			return true
		}
	}
	// CSS comment: the match is between /* and */ on the same or a nearby
	// preceding line. Check if there's an unclosed /* before the match
	// with no intervening */ .
	if isInBlockComment(src, start, end, "/*", "*/") {
		return true
	}
	// HTML comment: <!-- ... -->
	if isInBlockComment(src, start, end, "<!--", "-->") {
		return true
	}
	return false
}

// isInBlockComment reports whether src[start:end] sits inside an unclosed
// block comment delimited by openTok and closeTok. It scans backward from
// start looking for the nearest openTok or closeTok; if openTok is found
// first (i.e. more recently), the match is inside a comment.
func isInBlockComment(src []byte, start, end int, openTok, closeTok string) bool {
	// Scan a window backward from the match position. 512 bytes is enough
	// to cover multi-line comments in typical source without scanning the
	// entire file.
	windowStart := start - 512
	if windowStart < 0 {
		windowStart = 0
	}
	window := src[windowStart:start]
	// Find the last occurrence of either token in the backward window.
	lastOpen := bytes.LastIndex(window, []byte(openTok))
	lastClose := bytes.LastIndex(window, []byte(closeTok))
	// Inside a comment if the last opener is more recent than the last
	// closer (i.e. the comment hasn't been closed yet).
	return lastOpen > lastClose
}

// reSecurityContext matches security-relevant keywords that, when present
// near an insecure-random call, indicate the randomness is used for a
// security-sensitive purpose (tokens, session IDs, nonces, salts, OTPs). The
// rule's summary is "non-cryptographic randomness for security-relevant data"
// — without one of these keywords the call is likely visual/game logic.
// Substring matching (no \b) so camelCase identifiers like generateSessionToken
// or createNonce are caught; being liberal here is conservative for a security
// scanner — over-matching means we flag more, not fewer.
var reSecurityContext = regexp.MustCompile(`(?i)(?:token|password|passwd|secret|apikey|authtoken|sessionid|nonce|csrf|salt|otp|captcha|verify|challenge|bearer|jwt)`)

// hasSecurityContext reports whether a security-relevant keyword appears
// within a 200-byte window around pos (100 bytes before and after). This is
// the gate for the insecure-random rule: a Math.random() call with no
// security keyword nearby is visual/animation logic, not a crypto weakness.
func hasSecurityContext(src []byte, pos int) bool {
	from := pos - 100
	if from < 0 {
		from = 0
	}
	to := pos + 100
	if to > len(src) {
		to = len(src)
	}
	return reSecurityContext.Match(src[from:to])
}

// isPragmaTableInfo reports whether a dynamic-SQL match at pos is a SQLite
// schema-introspection call whose concatenated operands are bare identifiers
// (constant table names from the caller), not calls or field access.
// Trade-off: a variable holding user input would look identical, so pragma
// calls with bare identifiers are treated as constants; non-pragma SQL with
// the same shape is still flagged.
func isPragmaTableInfo(src []byte, pos int) bool {
	from := pos - 64
	if from < 0 {
		from = 0
	}
	window := src[from : pos+64]
	if !bytes.Contains(window, []byte("PRAGMA table_info(")) {
		return false
	}
	return rePragmaConcat.Match(src[pos : pos+64])
}

// rePragmaConcat matches " + <bare identifier>" operands in a PRAGMA
// table_info call: the identifier must be followed by a quote, a closing
// paren, another concat or the end — never a call, method or field access.
var rePragmaConcat = regexp.MustCompile(`\s*\+\s*[a-zA-Z_][a-zA-Z0-9_]*\s*(?:\)|"|'|\+|\s*$)`)

// isTestFile reports whether a file is a test fixture, matching the naming
// conventions across the indexed languages: *_test.go, foo_test.py,
// auth.test.js, *Test.java (JUnit/Maven), *Spec.java (Spock), test_*.py
// (pytest), *_spec.rb (RSpec), *.spec.ts (Jest). Files inside test
// directories (/test/, /tests/, src/test/) are also caught. Their fixtures
// routinely hold fake secrets that are not real findings.
func isTestFile(rel string) bool {
	base := filepath.Base(rel)
	// Substring patterns covering Go, Python, JS/TS, Ruby conventions.
	if strings.Contains(base, "_test.") || strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") || strings.Contains(base, "_spec.") {
		return true
	}
	// Java/Maven convention: *Test.java, *Tests.java, *IT.java, *Spec.java.
	// Check the stem (without extension) against common test suffixes.
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	for _, suffix := range []string{"Test", "Tests", "IT", "Spec", "Specs", "TestCase", "TestCases"} {
		if strings.HasSuffix(stem, suffix) {
			return true
		}
	}
	// Python pytest convention: test_*.py
	if strings.HasPrefix(base, "test_") {
		return true
	}
	// Directory-based test detection: files under /test/ or /tests/ paths
	// (Maven's src/test/java, Go's test dirs, etc.).
	lower := strings.ToLower(rel)
	if strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") {
		return true
	}
	return false
}

// maxLineLength returns the length of the longest line in src. Minified JS
// bundles pack entire libraries into single lines exceeding thousands of
// characters; legitimate source rarely exceeds 200.
func maxLineLength(src []byte) int {
	max, cur := 0, 0
	for _, b := range src {
		cur++
		if b == '\n' {
			if cur > max {
				max = cur
			}
			cur = 0
		}
	}
	if cur > max {
		max = cur
	}
	return max
}

// Scan walks root (mirroring the index walk: same extension filter, ignored
// directories, and .gitignore/.kernignore patterns) and returns every finding
// in the tree. Test files are skipped: their fixtures routinely hold fake
// secrets that are not real findings. Walk errors are returned so an unreadable
// tree can't silently produce a misleading "clean" result.
func Scan(root string) ([]Finding, error) {
	var findings []Finding
	ign := ignore.Load(root)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if path != root && index.IgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			// Honor .gitignore/.kernignore directory patterns so trees like
			// .venv_pdf/, node_modules/, dist/ are not scanned.
			if path != root && ign.Ignored(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		// Honor .gitignore/.kernignore file patterns.
		if ign.Ignored(rel) {
			return nil
		}
		if !index.QuickExt(rel) && !isConfigFile(rel) || isTestFile(rel) {
			return nil
		}
		src, serr := os.ReadFile(path)
		if serr != nil {
			// Skip unreadable files (binaries, broken symlinks, permission
			// denied) and continue the walk instead of aborting the entire
			// scan. A single unreadable file must not fail the whole check.
			return nil
		}
		// Skip minified JavaScript bundles (vendored libraries in static/
		// dirs): they pack entire libraries into single lines thousands of
		// characters long and routinely trip the hardcoded-secret detectors
		// with bogus findings.
		if strings.EqualFold(filepath.Ext(rel), ".js") && maxLineLength(src) > 2000 {
			return nil
		}
		findings = append(findings, ScanFile(rel, src)...)
		// Config files get an additional config-aware scan pass.
		if isConfigFile(rel) {
			findings = append(findings, ScanConfigFile(rel, src)...)
		}
		return nil
	})
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Rule < findings[j].Rule
	})
	return findings, err
}

// FilterBySeverity keeps only findings whose severity is in the allow list.
// An empty or absent list means all severities.
func FilterBySeverity(findings []Finding, allow []string) []Finding {
	if len(allow) == 0 {
		return findings
	}
	set := map[string]bool{}
	for _, s := range allow {
		set[strings.ToLower(strings.TrimSpace(s))] = true
	}
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if set[f.Severity] {
			out = append(out, f)
		}
	}
	return out
}

// Render formats findings as stable "severity rule file:line message" lines.
func Render(findings []Finding, max int) string {
	var b strings.Builder
	trimmed := findings
	if max > 0 && len(findings) > max {
		trimmed = findings[:max]
	}
	for _, f := range trimmed {
		b.WriteString(f.Severity)
		b.WriteString(" ")
		b.WriteString(f.Rule)
		b.WriteString(" ")
		b.WriteString(f.File)
		b.WriteString(":")
		b.WriteString(itoa(f.Line))
		b.WriteString(" ")
		b.WriteString(f.Message)
		if f.Snippet != "" {
			b.WriteString(" `")
			b.WriteString(f.Snippet)
			b.WriteString("`")
		}
		b.WriteString("\n")
	}
	if max > 0 && len(findings) > max {
		b.WriteString("... and ")
		b.WriteString(itoa(len(findings) - max))
		b.WriteString(" more findings\n")
	}
	return b.String()
}

// Counts tallies findings by severity.
func Counts(findings []Finding) map[string]int {
	c := map[string]int{}
	for _, f := range findings {
		c[f.Severity]++
	}
	return c
}

// isConfigFile reports whether a file is a configuration file that should
// receive the config-aware scan pass. These files are not source code but
// can harbor config-level bugs ($VAR vs ${VAR}, hardcoded credentials).
func isConfigFile(rel string) bool {
	ext := strings.ToLower(filepath.Ext(rel))
	switch ext {
	case ".properties", ".conf", ".ini", ".cfg", ".env":
		return true
	case ".yml", ".yaml":
		return true
	}
	// Spring Boot's application.properties / application.yml by name.
	base := strings.ToLower(filepath.Base(rel))
	if strings.HasPrefix(base, "application") {
		return true
	}
	return false
}

// ScanConfigFile runs config-specific rules against a configuration file.
// These rules detect misconfigurations that source-code scanners miss:
// $VAR without ${}, hardcoded credentials, disabled SSL verification.
func ScanConfigFile(rel string, src []byte) []Finding {
	var findings []Finding
	for _, r := range configRules {
		for _, idx := range r.RE.FindAllIndex(src, -1) {
			line := lineAt(src, idx[0])
			findings = append(findings, Finding{
				File:     rel,
				Line:     line,
				Rule:     r.ID,
				Severity: string(r.Severity),
				Message:  r.Summary,
				Snippet:  snippet(src, idx[0], idx[1]),
			})
		}
	}
	return findings
}

// configRules are the config-specific rules. They are separate from Rules
// (source-code rules) so they only fire on config files, not source code
// that might contain similar patterns (e.g. a Go string literal with $VAR).
var configRules = []Rule{
	{ID: "placeholder-bug", Severity: SeverityWarning, Summary: "property placeholder uses $VAR instead of ${VAR} — Spring injects the literal string, not the env value", RE: rePlaceholderBug},
	{ID: "hardcoded-config-cred", Severity: SeverityWarning, Summary: "hardcoded credential in config file (use ${ENV_VAR} instead)", RE: reHardcodedConfigCred},
	{ID: "disabled-ssl-verify", Severity: SeverityWarning, Summary: "SSL/TLS verification disabled in config", RE: reDisabledSsl},
}

func lineAt(src []byte, off int) int {
	if off < 0 || off > len(src) {
		return 0
	}
	return bytes.Count(src[:off], []byte("\n")) + 1
}

func snippet(src []byte, start, end int) string {
	lo := start
	for lo > 0 && src[lo-1] != '\n' {
		lo--
	}
	hi := end
	for hi < len(src) && src[hi] != '\n' {
		hi++
	}
	s := strings.TrimSpace(string(src[lo:hi]))
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
