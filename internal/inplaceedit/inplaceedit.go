// Package inplaceedit decides whether an in-place stream editor (sed -i,
// perl/ruby -i…) targets a CODE file inside the project root. Such an edit is
// a refactor, not scratch bookkeeping: the guard gates it so the change goes
// through the native edit tool or kern_apply (journaled, verifiable) instead
// of an opaque in-place rewrite. Writes to non-code files (docs, config,
// data), temp scratch and out-of-root targets keep their existing policy
// (path-aware allow / out-of-root gate).
package inplaceedit

import (
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/execclassify"
	"github.com/JayveerPrajapati/kern/internal/guardpaths"
)

// codeExts marks source files. Data/config formats (.md, .json, .yaml, .txt)
// are deliberately absent: rewriting them in place is routine bookkeeping.
var codeExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".py": true, ".rb": true, ".php": true, ".java": true, ".kt": true, ".kts": true,
	".rs": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".cxx": true, ".hpp": true,
	".cs": true, ".swift": true, ".m": true, ".mm": true, ".scala": true, ".sh": true, ".bash": true,
	".zsh": true, ".lua": true, ".pl": true, ".pm": true, ".dart": true, ".ex": true, ".exs": true,
	".erl": true, ".hs": true, ".clj": true, ".sql": true, ".vue": true, ".svelte": true,
}

// IsInPlace reports whether the segment is an in-place stream editor: sed with
// an in-place flag, or perl/ruby with a short -i flag (-i, -pi, -i.bak).
func IsInPlace(seg string) bool {
	toks := execclassify.StripPrefixes(execclassify.ShellTokens(seg))
	if len(toks) < 2 {
		return false
	}
	switch toks[0] {
	case "sed":
		return guardpaths.HasSedinPlaceFlag(toks[1:])
	case "perl", "ruby":
		for _, t := range toks[1:] {
			if strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.ContainsRune(t, 'i') {
				return true
			}
		}
	}
	return false
}

// TargetsCodeFile reports whether an in-place editor segment names a code
// file inside root. A relative path that escapes the root is not ours (the
// out-of-root write arm gates it); an absolute path counts only under root.
func TargetsCodeFile(seg, root string) bool {
	if !IsInPlace(seg) || root == "" {
		return false
	}
	toks := execclassify.StripPrefixes(execclassify.ShellTokens(seg))
	root = filepath.Clean(root)
	for _, a := range toks[1:] {
		if strings.HasPrefix(a, "-") || a == "" || a == "-" {
			continue
		}
		if !codeExts[strings.ToLower(filepath.Ext(a))] {
			continue
		}
		if filepath.IsAbs(a) {
			if strings.HasPrefix(filepath.Clean(a), root+string(filepath.Separator)) {
				return true
			}
			continue
		}
		if clean := filepath.Clean(a); clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
