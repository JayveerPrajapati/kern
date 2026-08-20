// Package pack builds a single paste-ready bundle of a whole repository:
// project instructions, a directory tree with per-file token counts, and the
// file contents, sized to fit a token budget.
package pack

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/ignore"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// File is one packed repository file.
type File struct {
	Path    string `json:"path"`
	Bytes   int    `json:"bytes"`
	Tokens  int    `json:"tokens"`
	Content string `json:"content,omitempty"`
}

// Bundle is a paste-ready snapshot of a repository.
type Bundle struct {
	Root         string        `json:"root"`
	Instructions []File        `json:"instructions"`
	Files        []File        `json:"files"`
	TotalTokens  int           `json:"total_tokens"`
	Truncated    bool          `json:"truncated"`
	Dropped      int           `json:"dropped"`
	Ignored      int           `json:"ignored"`
	Security     []sec.Finding `json:"security,omitempty"`

	// budgetTooSmall is set when MaxTokens was too small to include ANY source
	// file (instructions consumed the budget). Render surfaces a warning so the
	// caller knows the bundle is essentially empty rather than silently
	// shipping an empty REPOSITORY FILES section.
	budgetTooSmall   bool
	budgetAfterInstr int
}

// Options controls a pack build.
type Options struct {
	Root             string
	MaxTokens        int  // 0 = unlimited
	MaxFiles         int  // 0 = unlimited
	MaxFileBytes     int  // per-file content cap (default 512KiB)
	SkipInstructions bool // default: include root-level docs as instructions
}

// instructionNames are root-level docs packed verbatim as project
// instructions so the agent knows the rules before it reads any source.
var instructionNames = []string{
	"AGENTS.md", "README.md", "CONTRIBUTING.md", "CODING_STANDARDS.md",
	"TODOS.md", "CHANGELOG.md",
}

const defaultMaxFileBytes = 512 << 10

// FilesTokens returns the total token count of the packed source files
// (excluding instructions). Used by the budget-too-small warning to report
// how many tokens instructions consumed.
func (b *Bundle) FilesTokens() int {
	sum := 0
	for _, f := range b.Files {
		sum += f.Tokens
	}
	return sum
}

// Build walks root and returns a paste-ready bundle. Files are packed in
// deterministic path order; when MaxTokens > 0 the largest files that do not
// fit are skipped (not truncated mid-file) and counted in Dropped, so the
// bundle always reads cleanly.
func Build(root string, opts Options) (*Bundle, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = defaultMaxFileBytes
	}
	b := &Bundle{Root: abs}
	ig := ignore.Load(abs)

	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(abs, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if path != abs && (code.IsIgnoredDir(d.Name()) || ig.Ignored(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if code.ShouldIgnore(rel) || ig.Ignored(rel) {
			b.Ignored++
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if opts.SkipInstructions && isInstruction(rel) {
			b.Ignored++
			return nil
		}
		if isInstruction(rel) {
			f, ok := readFile(rel, path, opts.MaxFileBytes)
			if ok {
				b.Instructions = append(b.Instructions, f)
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		// Check the size cap BEFORE reading so a multi-GB file is never
		// buffered into memory just to be discarded.
		if info.Size() > int64(opts.MaxFileBytes) {
			b.Ignored++
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil || len(content) > opts.MaxFileBytes {
			b.Ignored++
			return nil
		}
		if isBinary(content) {
			b.Ignored++
			return nil
		}
		f := File{
			Path:    rel,
			Bytes:   len(content),
			Tokens:  tokenize.Count(string(content)),
			Content: string(content),
		}
		b.Files = append(b.Files, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(b.Instructions, func(i, j int) bool { return b.Instructions[i].Path < b.Instructions[j].Path })
	sort.Slice(b.Files, func(i, j int) bool { return b.Files[i].Path < b.Files[j].Path })

	// Instruction docs are capped so a giant README cannot blow the budget the
	// pack was sized to fit; budget.Fit keeps the head and important lines.
	const instructionCap = 1000
	for i := range b.Instructions {
		if b.Instructions[i].Tokens > instructionCap {
			b.Instructions[i].Content = budget.Fit(b.Instructions[i].Content, instructionCap) +
				"\n\n… [trimmed to fit the pack; open the file or use kern_doc_search for the rest]"
			b.Instructions[i].Tokens = tokenize.Count(b.Instructions[i].Content)
		}
	}

	if opts.MaxFiles > 0 && len(b.Files) > opts.MaxFiles {
		b.Dropped += len(b.Files) - opts.MaxFiles
		b.Files = b.Files[:opts.MaxFiles]
		b.Truncated = true
	}

	// Apply the token budget at file granularity: reserve the instruction cost
	// plus a small allowance for the header/tree/stats sections, then
	// skip-and-continue so small files still make it in.
	if opts.MaxTokens > 0 {
		usedInstr := 0
		for _, f := range b.Instructions {
			usedInstr += f.Tokens
		}
		budget := opts.MaxTokens - usedInstr - 200 // header/tree/stats overhead
		kept := b.Files[:0]
		dropped := 0
		used := 0
		for _, f := range b.Files {
			if budget <= 0 || f.Tokens > budget-used {
				dropped++
				continue
			}
			kept = append(kept, f)
			used += f.Tokens
		}
		if dropped > 0 {
			b.Truncated = true
		}
		// Warn when the budget was too small to include ANY source file: the
		// bundle would ship only instructions (or nothing) with an empty
		// REPOSITORY FILES section, which is almost never what the caller
		// wanted. Surface it explicitly instead of returning a silent empty
		// tree.
		if len(b.Files) > 0 && len(kept) == 0 {
			b.budgetTooSmall = true
			b.budgetAfterInstr = budget
		}
		b.Dropped += dropped
		b.Files = kept
	}

	for _, f := range b.Files {
		b.TotalTokens += f.Tokens
	}
	for _, f := range b.Instructions {
		b.TotalTokens += f.Tokens
	}
	// Secrets can hide in instructions (READMEs, AGENTS.md) too — scan both
	// buckets so hardcoded tokens in docs ship flagged, not silently.
	var scan []File
	scan = append(scan, b.Instructions...)
	scan = append(scan, b.Files...)
	b.Security = scanFindings(scan)
	return b, nil
}

// maxFindings caps the security report so the bundle stays readable.
const maxFindings = 25

// scanFindings runs the secrets/injection rules over every packed file and
// returns up to maxFindings findings, so a bundle that ships secrets surfaces
// them instead of silently carrying them into an agent's context.
func scanFindings(files []File) []sec.Finding {
	var out []sec.Finding
	for _, f := range files {
		out = append(out, sec.ScanFile(f.Path, []byte(f.Content))...)
		if len(out) >= maxFindings {
			break
		}
	}
	if len(out) > maxFindings {
		out = out[:maxFindings]
	}
	return out
}

func isInstruction(rel string) bool {
	base := filepath.Base(rel)
	dir := filepath.ToSlash(filepath.Dir(rel))
	// Only promote instruction files (AGENTS.md and friends) when they sit at
	// the repo root or directly in docs/. A nested submodule/vendor AGENTS.md
	// is not authoritative project rules and must not be promoted.
	if dir != "." && dir != "docs" {
		return false
	}
	for _, n := range instructionNames {
		if base == n {
			return true
		}
	}
	return false
}

func readFile(rel, path string, maxBytes int) (File, bool) {
	// Check the size cap via Stat BEFORE reading so oversized files are
	// skipped without buffering their full contents into memory.
	if maxBytes > 0 {
		if info, err := os.Stat(path); err != nil || info.Size() > int64(maxBytes) {
			return File{}, false
		}
	}
	content, err := os.ReadFile(path)
	if err != nil || len(content) > maxBytes {
		return File{}, false
	}
	if isBinary(content) {
		return File{}, false
	}
	return File{
		Path:    rel,
		Bytes:   len(content),
		Tokens:  tokenize.Count(string(content)),
		Content: string(content),
	}, true
}

func isBinary(content []byte) bool {
	n := len(content)
	if n > 1024 {
		n = 1024
	}
	for i := 0; i < n; i++ {
		if content[i] == 0 {
			return true
		}
	}
	return false
}

// fence returns a code-fence delimiter (a run of backticks) that cannot be
// closed by any triple-backtick run inside content. It is one backtick longer
// than the longest backtick run in content, minimum 3, so a file containing
// ``` cannot break out of the fence and be interpreted as instructions.
func fence(content string) string {
	max := 0
	run := 0
	for _, r := range content {
		if r == '`' {
			run++
			if run > max {
				max = run
			}
		} else {
			run = 0
		}
	}
	n := max + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}

// Render returns the bundle as a paste-ready text document: header,
// instructions, tree with token counts, per-file contents, and stats.
func (b *Bundle) Render() string {
	var out strings.Builder
	fmt.Fprintf(&out, "This file is a merged representation of the codebase at %s, generated by kern pack.\n", b.Root)
	fmt.Fprintf(&out, "It contains 1) project instructions, 2) the repository structure, and 3) file contents, sized to fit the agent's context. Use it as the full working picture for edits.\n\n")

	fmt.Fprintf(&out, "================================================\nINSTRUCTIONS\n================================================\n\n")
	if len(b.Instructions) == 0 {
		out.WriteString("(no instruction files found)\n")
	} else {
		for _, f := range b.Instructions {
			delim := fence(f.Content)
			fmt.Fprintf(&out, "## %s (%d tokens)\n\n%s\n%s\n%s\n\n", f.Path, f.Tokens, delim, f.Content, delim)
		}
	}

	fmt.Fprintf(&out, "================================================\nREPOSITORY STRUCTURE\n================================================\n\n")
	out.WriteString(b.tree())

	fmt.Fprintf(&out, "\n================================================\nREPOSITORY FILES\n================================================\n\n")
	for _, f := range b.Files {
		fmt.Fprintf(&out, "## File: %s (%d tokens)\n\n", f.Path, f.Tokens)
		delim := fence(f.Content)
		fmt.Fprintf(&out, "%s%s\n%s\n%s\n\n", delim, fenceLang(f.Path), f.Content, delim)
	}

	if len(b.Security) > 0 {
		fmt.Fprintf(&out, "================================================\nSECURITY\n================================================\n\n")
		fmt.Fprintf(&out, "These may be secrets or risky patterns shipped in the packed files. Review before sharing the bundle:\n\n")
		for _, f := range b.Security {
			fmt.Fprintf(&out, "- %s:%d [%s] %s\n", f.File, f.Line, f.Severity, f.Message)
		}
		fmt.Fprintf(&out, "\n")
	}

	fmt.Fprintf(&out, "================================================\nSTATS\n================================================\n\n")
	fmt.Fprintf(&out, "- Files packed: %d\n", len(b.Files))
	fmt.Fprintf(&out, "- Instructions: %d\n", len(b.Instructions))
	fmt.Fprintf(&out, "- Total tokens: %d\n", b.TotalTokens)
	if b.budgetTooSmall {
		fmt.Fprintf(&out, "- WARNING: --max-tokens budget too small to include ANY source file\n")
		fmt.Fprintf(&out, "  (instructions consumed %d tokens; only %d remained after the reserved overhead).\n", b.TotalTokens-b.FilesTokens(), b.budgetAfterInstr)
		fmt.Fprintf(&out, "  Re-run with a larger --max-tokens (e.g. >=4000) or use `kern pack` without a budget.\n")
	}
	if b.Truncated {
		fmt.Fprintf(&out, "- Dropped to fit budget: %d\n", b.Dropped)
	}
	if b.Ignored > 0 {
		fmt.Fprintf(&out, "- Ignored (VCS/build/lock/binary/dotfiles/gitignore+kernignore): %d\n", b.Ignored)
	}
	return out.String()
}

// tree renders the packed file list as an indented directory tree with token
// counts, dirs and files interleaved in path order so the structure reads
// naturally.
func (b *Bundle) tree() string {
	type entry struct {
		depth int
		path  string
		tok   int
	}
	seen := map[string]bool{}
	var entries []entry
	add := func(depth int, path string, tok int) {
		if seen[path] {
			return
		}
		seen[path] = true
		entries = append(entries, entry{depth, path, tok})
	}
	for _, f := range b.Files {
		segs := strings.Split(filepath.ToSlash(f.Path), "/")
		for i := 1; i < len(segs); i++ {
			add(i, strings.Join(segs[:i], "/")+"/", 0)
		}
		add(len(segs), f.Path, f.Tokens)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	var out strings.Builder
	fmt.Fprintf(&out, "%s/\n", filepath.Base(b.Root))
	for _, e := range entries {
		// Render the basename at each depth: the indentation carries the
		// hierarchy, so printing the full path again would be a list, not a
		// tree.
		label := strings.TrimSuffix(e.path, "/")
		if i := strings.LastIndexByte(label, '/'); i >= 0 {
			label = label[i+1:]
		}
		if strings.HasSuffix(e.path, "/") {
			label += "/"
		}
		out.WriteString(strings.Repeat("  ", e.depth))
		out.WriteString("├── ")
		out.WriteString(label)
		if strings.HasSuffix(e.path, "/") {
			out.WriteString("\n")
		} else {
			fmt.Fprintf(&out, " (%d tokens)\n", e.tok)
		}
	}
	return out.String()
}

// JSON returns the bundle as machine-readable JSON.
func (b *Bundle) JSON() (string, error) {
	blob, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", err
	}
	return string(blob), nil
}

func fenceLang(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".md":
		return "markdown"
	case ".json":
		return "json"
	case ".sh", ".bash":
		return "bash"
	case ".yml", ".yaml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".sql":
		return "sql"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp", ".cc":
		return "cpp"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".lua":
		return "lua"
	case ".java":
		return "java"
	case ".kt":
		return "kotlin"
	default:
		return ""
	}
}
