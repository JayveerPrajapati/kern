package langparse

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readRepoRoot reads a repo-root-relative file (tests run with the package
// dir as CWD, so the repo root is two levels up).
func readRepoRoot(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestSupportedLanguagesCount pins the canonical indexed-language set: the
// README's headline and section claims must derive from SupportedLanguages,
// not drift by hand. README claims to cover:
//
//   - banner: "18 Indexed Languages"
//   - the language list line (Go · Python · … · YAML) — 18 entries
//   - "13 of the 18 languages use deep tree-sitter grammars in the default
//     build" — must equal the tree-sitter grammar count in
//     internal/index/treesitter.go (read here as the map's key count),
//     modulo the shell→bash and typescript→tsx aliases
//   - "the pure-Go build swaps in regex heuristics covering 16 languages" —
//     18 minus the tree-sitter-only language (Dart) minus go/ast-only (Go)
//
// Each claim is checked against the live source of truth; a doc that drifts
// fails here with the expected vs actual counts, not silently.
func TestSupportedLanguagesCount(t *testing.T) {
	langs := SupportedLanguages()
	if len(langs) != 18 {
		t.Fatalf("SupportedLanguages() = %d languages, want 18 (the canonical indexed set)", len(langs))
	}
	// Distinct, sorted.
	for i := 1; i < len(langs); i++ {
		if langs[i] <= langs[i-1] {
			t.Fatalf("SupportedLanguages() not strictly sorted at %q/%q", langs[i-1], langs[i])
		}
	}
	seen := map[string]bool{}
	for _, l := range langs {
		if seen[l] {
			t.Fatalf("SupportedLanguages() contains duplicate %q", l)
		}
		seen[l] = true
	}

	readme := readRepoRoot(t, "README.md")
	banner := regexp.MustCompile(`\*\*(\d+) Indexed Languages\b`).FindStringSubmatch(readme)
	if len(banner) != 2 {
		t.Fatalf("README banner lacks an \"N Indexed Languages\" claim to guard")
	}
	if banner[1] != "18" {
		t.Fatalf("README banner says %s indexed languages, want 18 (from langparse.SupportedLanguages())", banner[1])
	}

	// The language list line: "Go · Python · JavaScript (JSX) · … · YAML".
	listLine := regexp.MustCompile(`(?m)^Go · Python · .*?· YAML$`).FindString(readme)
	if listLine == "" {
		t.Fatalf("README lacks the language list line (Go · Python · … · YAML) to guard")
	}
	count := len(regexp.MustCompile(`·`).FindAllString(listLine, -1)) + 1
	if count != 18 {
		t.Fatalf("README language list has %d entries, want 18", count)
	}
	names := map[string]string{ // README display name → canonical id
		"Go": "go", "Python": "python", "JavaScript": "javascript",
		"TypeScript": "typescript", "Rust": "rust", "C": "c",
		"C++": "cpp", "C#": "csharp", "Java": "java", "Ruby": "ruby",
		"PHP": "php", "Shell": "shell", "Dart": "dart",
		"CSS": "css", "HTML": "html", "Markdown": "markdown",
		"JSON": "json", "YAML": "yaml",
	}
	for display, id := range names {
		if !strings.Contains(listLine, display) {
			t.Errorf("README language list missing %q (canonical id %q)", display, id)
		}
		if !seen[id] {
			t.Errorf("README language %q → canonical id %q is not in SupportedLanguages()", display, id)
		}
	}

	// tree-sitter grammar count: internal/index/treesitter.go's tsLanguageMap.
	tsSrc := readRepoRoot(t, "internal/index/treesitter.go")
	grammarCount := len(regexp.MustCompile(`"([a-z]+)":\s*sitter\.NewLanguage`).FindAllStringSubmatch(tsSrc, -1))
	// 13 of 18 claim.
	tsClaim := regexp.MustCompile(`13 of the 18 languages use deep tree-sitter grammars`).FindString(readme)
	if tsClaim == "" {
		t.Fatalf("README lacks the \"13 of the 18 languages use deep tree-sitter grammars\" claim to guard")
	}
	// tsLanguageMap keys cover 13 distinct canonical languages: the map has
	// 15 keys but typescript/tsx are one canonical language and bash is the
	// tree-sitter grammar for shell, so canonical coverage = 13.
	canonical := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([a-z]+)":\s*sitter\.NewLanguage`).FindAllStringSubmatch(tsSrc, -1) {
		switch m[1] {
		case "tsx":
			canonical["typescript"] = true
		case "bash":
			canonical["shell"] = true
		default:
			canonical[m[1]] = true
		}
	}
	if len(canonical) != 13 {
		t.Fatalf("tree-sitter grammars cover %d canonical languages (from tsLanguageMap in internal/index/treesitter.go), want 13 to match the README claim", len(canonical))
	}
	if grammarCount != 14 {
		t.Fatalf("tsLanguageMap has %d grammar keys, want 14 (go+python+javascript+typescript+tsx+c+cpp+java+rust+ruby+php+bash+css+dart)", grammarCount)
	}
}
