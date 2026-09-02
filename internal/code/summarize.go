// Package code produces compact, symbolic summaries of source files and
// projects so agents get the structure without re-reading every line.
package code

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Symbol is a single named declaration in a file.
type Symbol struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Line   int    `json:"line"`
	Params string `json:"params,omitempty"`
}

// Summary is the compact representation of one file.
type Summary struct {
	Path      string   `json:"path"`
	Hash      string   `json:"hash"`
	Language  string   `json:"language"`
	Symbols   []Symbol `json:"symbols"`
	Lines     int      `json:"lines"`
	Truncated bool     `json:"truncated,omitempty"`
}

var patterns = map[string][]struct {
	kind *regexp.Regexp
	name *regexp.Regexp
}{
	"go": {
		{regexp.MustCompile(`^func\s+`), regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)(?:\s*\(|\s*\{|\s+[A-Za-z_])`)},
		{regexp.MustCompile(`^type\s+`), regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`)},
		{regexp.MustCompile(`^const\s*\(`), nil},
		{regexp.MustCompile(`^var\s*\(`), nil},
		{regexp.MustCompile(`^package\s+`), regexp.MustCompile(`^package\s+([A-Za-z_]\w*)`)},
	},
	"python": {
		{regexp.MustCompile(`^\s*(?:async\s+)?def\s+`), regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`)},
		{regexp.MustCompile(`^\s*class\s+`), regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)},
	},
	"js": {
		{regexp.MustCompile(`(?:^|\s)(?:export\s+)?(?:function|async\s+function)\s+`), regexp.MustCompile(`(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$]\w*)`)},
		{regexp.MustCompile(`(?:^|\s)(?:export\s+)?class\s+`), regexp.MustCompile(`(?:export\s+)?class\s+([A-Za-z_$]\w*)`)},
		{regexp.MustCompile(`const\s+[A-Za-z_$]\w*\s*=\s*(?:async\s*)?\(`), regexp.MustCompile(`const\s+([A-Za-z_$]\w*)\s*=`)},
		{regexp.MustCompile(`^import\s+`), nil},
	},
	"java": {
		{regexp.MustCompile(`^\s*(?:public|private|protected|static|final|abstract|synchronized|\s)*[A-Za-z_][\w<>\[\], ]*\s+[A-Za-z_]\w*\s*\(`), regexp.MustCompile(`([A-Za-z_]\w*)\s*\([^)]*\)\s*(?:throws[^{]*)?\{`)},
		{regexp.MustCompile(`^\s*(?:public|abstract|final|private|protected)?\s*(?:class|interface|enum)\s+`), regexp.MustCompile(`(?:class|interface|enum)\s+([A-Za-z_]\w*)`)},
	},
	"c": {
		{regexp.MustCompile(`^[A-Za-z_][\w\s\*\[\]]+\([^;]*\)\s*\{`), regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\([^;]*\)\s*\{`)},
		{regexp.MustCompile(`^(?:typedef|struct|enum|union)\s+`), regexp.MustCompile(`^(?:typedef\s+)?(?:struct|enum|union)\s+([A-Za-z_]\w*)`)},
	},
	"rust": {
		{regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+`), regexp.MustCompile(`(?:pub\s+)?fn\s+([A-Za-z_]\w*)`)},
		{regexp.MustCompile(`^\s*(?:pub\s+)?(?:struct|enum|trait|impl)\s+`), regexp.MustCompile(`(?:pub\s+)?(?:struct|enum|trait|impl)\s+([A-Za-z_]\w*)`)},
	},
	"ruby": {
		{regexp.MustCompile(`^\s*def\s+`), regexp.MustCompile(`^\s*def\s+(?:self\.)?([A-Za-z_]\w*[!?]?)`)},
		{regexp.MustCompile(`^\s*class\s+`), regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)},
	},
	"shell": {
		{regexp.MustCompile(`^\s*(?:function\s+)?[A-Za-z_]\w*\s*\(\)`), regexp.MustCompile(`(?:function\s+)?([A-Za-z_]\w*)\s*\(\)`)},
		{regexp.MustCompile(`^\s*[A-Za-z_]\w*=\(\)`), regexp.MustCompile(`^\s*([A-Za-z_]\w*)=\(\)`)},
	},
	"yaml": {
		{regexp.MustCompile(`^[A-Za-z_][\w-]*:`), regexp.MustCompile(`^([A-Za-z_][\w-]*):`)},
	},
	"css": {
		{regexp.MustCompile(`^@(?:keyframes|media|font-face|supports|page)\s+`), regexp.MustCompile(`^@(?:keyframes|media|font-face|supports|page)\s+([A-Za-z_-][\w-]*)`)},
		{regexp.MustCompile(`^\.[A-Za-z_-]`), regexp.MustCompile(`^\.([A-Za-z_-][\w-]*)`)},
		{regexp.MustCompile(`^#[A-Za-z_-]`), regexp.MustCompile(`^#([A-Za-z_-][\w-]*)`)},
	},
}

// DetectLanguage returns the language key for a file extension.
func DetectLanguage(path string) string {
	switch strings.ToLower(strings.TrimPrefix(filepathExt(path), ".")) {
	case "go":
		return "go"
	case "py":
		return "python"
	case "js", "mjs", "cjs", "jsx", "ts", "tsx":
		return "js"
	case "java":
		return "java"
	case "c", "h", "cc", "cpp", "cxx", "hpp":
		return "c"
	case "rs":
		return "rust"
	case "rb":
		return "ruby"
	case "sh", "bash":
		return "shell"
	case "yaml", "yml":
		return "yaml"
	case "css":
		return "css"
	case "html", "htm":
		return "html"
	}
	return ""
}

func filepathExt(path string) string {
	i := strings.LastIndexByte(path, '.')
	if i < 0 {
		return ""
	}
	return path[i:]
}

// Summarize extracts symbols from file content. maxSymbols caps output.
func Summarize(path string, content []byte, maxSymbols int) Summary {
	if maxSymbols <= 0 {
		maxSymbols = 200
	}
	lang := DetectLanguage(path)
	lines := strings.Split(string(content), "\n")
	sum := Summary{
		Path:     path,
		Hash:     hashBytes(content),
		Language: lang,
		Lines:    len(lines),
	}
	if lang == "" || len(patterns[lang]) == 0 {
		return sum
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, p := range patterns[lang] {
			if p.kind.MatchString(trimmed) {
				if p.name == nil {
					sum.Symbols = append(sum.Symbols, Symbol{Kind: "block", Name: trimmed, Line: i + 1})
				} else if m := p.name.FindStringSubmatch(line); m != nil {
					kind := "decl"
					if strings.HasPrefix(trimmed, "func") || strings.Contains(trimmed, "function") {
						kind = "func"
					} else if strings.Contains(trimmed, "class") || strings.Contains(trimmed, "struct") || strings.Contains(trimmed, "type") {
						kind = "type"
					}
					sum.Symbols = append(sum.Symbols, Symbol{Kind: kind, Name: m[1], Line: i + 1, Params: paramSnippet(line)})
				}
				break
			}
		}
		if len(sum.Symbols) >= maxSymbols {
			sum.Truncated = true
			break
		}
	}
	sort.SliceStable(sum.Symbols, func(a, b int) bool {
		if sum.Symbols[a].Kind != sum.Symbols[b].Kind {
			return sum.Symbols[a].Kind < sum.Symbols[b].Kind
		}
		return sum.Symbols[a].Line < sum.Symbols[b].Line
	})
	return sum
}

// Render renders a summary as compact text for agent consumption.
func (s Summary) Render() string {
	if s.Language == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(s.Path)
	b.WriteString(" [")
	b.WriteString(s.Language)
	b.WriteString(", ")
	b.WriteString(strconv.Itoa(s.Lines))
	b.WriteString(" lines, ")
	b.WriteString(strconv.Itoa(len(s.Symbols)))
	b.WriteString(" symbols]\n")
	for _, sym := range s.Symbols {
		b.WriteString("  ")
		b.WriteString(sym.Kind)
		b.WriteString(" ")
		b.WriteString(sym.Name)
		b.WriteString(" :")
		b.WriteString(strconv.Itoa(sym.Line))
		if sym.Params != "" {
			b.WriteString(" ")
			b.WriteString(sym.Params)
		}
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func paramSnippet(line string) string {
	idx := strings.IndexByte(line, '(')
	if idx < 0 {
		return ""
	}
	end := strings.IndexByte(line[idx:], ')')
	if end < 0 {
		return ""
	}
	p := strings.TrimSpace(line[idx : idx+end+1])
	if len(p) > 80 {
		p = p[:77] + "..."
	}
	return p
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ReadFile reads a file for summarization.
func ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// ReadAllLines is a small helper used by the project walker.
func ReadAllLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}
