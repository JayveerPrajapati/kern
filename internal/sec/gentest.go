package sec

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// TestScaffold is a deterministic go test frame for a tainted sink.
type TestScaffold struct {
	File string // suggested output file: <sink dir>/<sink base>_taint_test.go
	Pkg  string // package clause read from the sink file
	Code string // the scaffold source
}

// packageClauseRe matches the package clause of a Go source file.
var packageClauseRe = regexp.MustCompile(`(?m)^package\s+(\w+)`)

// probeComments maps a rule family to the deterministic probe instruction
// embedded in the generated test body.
var probeComments = map[string]string{
	"sql-injection":          "craft a malicious SQL input and assert the query path parameterizes it",
	"command-injection":      "assert shell input is validated before exec",
	"unsafe-deserialization": "assert untrusted payloads are rejected",
}

// ruleCamel maps a rule id to its CamelCase test suffix. Unknown rules fall
// back to a sanitized form (alphanumerics only, capitalized words).
func ruleCamel(rule string) string {
	switch rule {
	case "sql-injection":
		return "SQLInjection"
	case "command-injection":
		return "CommandInjection"
	case "unsafe-deserialization":
		return "UnsafeDeserialization"
	}
	var b strings.Builder
	up := true
	for _, r := range rule {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if up {
				b.WriteString(strings.ToUpper(string(r)))
				up = false
			} else {
				b.WriteString(string(r))
			}
		} else {
			up = true
		}
	}
	if b.Len() == 0 {
		return "Generic"
	}
	return b.String()
}

// GenTestScaffold builds the frame: package clause, "testing" import, one
// func TestTaint<RuleCamel><Line>(t *testing.T) with a rule-family probe
// comment and a TODO body. The probe body is intentionally left for the
// caller (LLM-assisted fill); the frame itself is deterministic.
func GenTestScaffold(t TaintFinding) TestScaffold {
	dir := filepath.Dir(t.File)
	base := filepath.Base(t.File)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	outFile := filepath.Join(dir, stem+"_taint_test.go")

	// Pkg: parse `package X` from root/<t.File> (resolved relative to the
	// working directory); fall back to "main" when unreadable or absent.
	pkg := "main"
	if data, err := os.ReadFile(filepath.FromSlash(t.File)); err == nil {
		if m := packageClauseRe.FindSubmatch(data); m != nil {
			pkg = string(m[1])
		}
	}

	rule := t.Rule
	if rule == "" {
		rule = "unknown"
	}
	camel := ruleCamel(rule)
	probe := probeComments[rule]
	if probe == "" {
		probe = "probe input validation at this sink"
	}
	via := "source"
	if t.EntryPoint != "" {
		via = t.EntryPoint
	}
	code := fmt.Sprintf("package %s\n\nimport \"testing\"\n\n// Generated scaffold for %s:%d (%s) — tainted via %s\nfunc TestTaint%s%d(t *testing.T) {\n\t// TODO: craft input that reaches %s:%d and assert safe handling\n\t// (%s)\n}\n",
		pkg, t.File, t.Line, rule, via, camel, t.Line, t.File, t.Line, probe)

	return TestScaffold{File: outFile, Pkg: pkg, Code: code}
}
