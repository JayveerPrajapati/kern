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

// isTestFile reports whether a file is a test fixture, matching the naming
// conventions across the indexed languages (*_test.go, foo_test.py,
// auth.test.js, ...). Their fixtures routinely hold fake secrets that are not
// real findings.
func isTestFile(rel string) bool {
	base := filepath.Base(rel)
	return strings.Contains(base, "_test.") || strings.Contains(base, ".test.")
}

// Scan walks root (mirroring the index walk: same extension filter and ignored
// directories) and returns every finding in the tree. Test files are skipped:
// their fixtures routinely hold fake secrets that are not real findings. Walk
// errors are returned so an unreadable tree can't silently produce a
// misleading "clean" result.
func Scan(root string) ([]Finding, error) {
	var findings []Finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && index.IgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil || !index.QuickExt(rel) || isTestFile(rel) {
			return nil
		}
		src, serr := os.ReadFile(path)
		if serr != nil {
			return serr
		}
		findings = append(findings, ScanFile(rel, src)...)
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
