package fw

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/ignore"
)

// manifestNames are the dependency files kern scans for Dep signals.
var manifestNames = map[string]bool{
	"package.json": true, "package-lock.json": true, "go.mod": true,
	"requirements.txt": true, "pyproject.toml": true, "setup.py": true,
	"setup.cfg": true, "Pipfile": true, "poetry.lock": true,
	"pom.xml": true, "build.gradle": true, "build.gradle.kts": true,
	"settings.gradle": true, "Gemfile": true, "composer.json": true,
	"composer.lock": true, "Cargo.toml": true, "mix.exs": true,
	"pubspec.yaml": true, "go.work": true,
}

// manifestLang maps a manifest file to the languages whose framework dep
// signals should be checked in it. This prevents a JS package-lock.json
// mentioning "tokio" from triggering the Rust Tokio framework, or a Java
// pom.xml mentioning "flask" from triggering Python Flask. JS and TS share
// package.json, so both are listed.
var manifestLang = map[string][]string{
	"package.json": {"js", "ts"}, "package-lock.json": {"js", "ts"},
	"go.mod": {"go"}, "go.work": {"go"},
	"requirements.txt": {"python"}, "pyproject.toml": {"python"},
	"setup.py": {"python"}, "setup.cfg": {"python"},
	"Pipfile": {"python"}, "poetry.lock": {"python"},
	"pom.xml": {"java"}, "build.gradle": {"java"}, "build.gradle.kts": {"java"},
	"settings.gradle": {"java"},
	"Gemfile":         {"ruby"},
	"composer.json":   {"php"}, "composer.lock": {"php"},
	"Cargo.toml":   {"rust"},
	"mix.exs":      {"elixir"},
	"pubspec.yaml": {"dart"},
}

// sourceExts are files scanned for Code signals.
var sourceExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".mjs": true, ".cjs": true,
	".jsx": true, ".ts": true, ".tsx": true, ".vue": true, ".svelte": true,
	".astro": true, ".rb": true, ".php": true, ".rs": true, ".cs": true,
	".java": true, ".c": true, ".h": true, ".cc": true, ".cpp": true,
	".cxx": true, ".hpp": true, ".hxx": true, ".csproj": true, ".fsproj": true,
}

// fwBaselineIgnoreDirs are directories that are virtually always build
// artifacts or dependencies and should never be scanned for framework
// signals, even when no .gitignore/.kernignore explicitly excludes them.
// This is a safety net on top of ignore.Load (which handles .gitignore and
// .kernignore patterns).
var fwBaselineIgnoreDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "vendor": true, "target": true,
	"dist": true, "build": true, "out": true, "bin": true,
	".venv": true, "venv": true, "__pycache__": true,
	".cache": true, ".idea": true, ".vscode": true, ".kern": true,
}

// Detect walks root and reports every catalog framework revealed by file
// markers, dependency-manifest entries or source-code markers. It honors
// .gitignore and .kernignore (via internal/ignore) so trees like .venv/,
// node_modules/, .opencode/ are not scanned. Dep signals are scoped to
// manifests of the same language as the framework, preventing a JS
// package-lock.json from triggering a Rust framework (e.g. "tokio").
func Detect(root string) ([]Detected, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	ign := ignore.Load(abs)

	// Precompute per-language code markers, dep markers and a path-marker index.
	codeByLang := map[string][]*Framework{}
	depByLang := map[string][]*Framework{}
	var all []*Framework
	for i := range catalog {
		f := &catalog[i]
		all = append(all, f)
		if len(f.Code) > 0 {
			codeByLang[f.Lang] = append(codeByLang[f.Lang], f)
		}
		if len(f.Deps) > 0 {
			depByLang[f.Lang] = append(depByLang[f.Lang], f)
		}
	}

	matched := map[string]map[string]bool{} // framework id -> signal -> true

	addSignal := func(id, sig string) {
		if matched[id] == nil {
			matched[id] = map[string]bool{}
		}
		matched[id][sig] = true
	}

	checkPath := func(relLower string) {
		for _, f := range all {
			for _, m := range f.Files {
				hit := false
				if strings.Contains(m, "/") {
					hit = strings.Contains(relLower, m)
				} else {
					hit = strings.HasSuffix(relLower, m)
				}
				if hit {
					addSignal(f.ID, "file: "+m)
				}
			}
		}
	}

	sourceCount := 0
	const sourceCap = 2000

	werr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(abs, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		relLower := strings.ToLower(rel)
		if d.IsDir() {
			if path == abs {
				return nil
			}
			// Baseline: always skip VCS/build/dependency dirs even when no
			// .gitignore/.kernignore exists.
			if fwBaselineIgnoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			// Honor .gitignore/.kernignore so .venv_pdf/, .opencode/,
			// node_modules/ etc. are not scanned.
			if ign.Ignored(rel) {
				return filepath.SkipDir
			}
			// A directory marker like "app/controllers" still counts as a path
			// signal, so check it before skipping into it.
			checkPath(relLower + "/")
			return nil
		}
		// Honor .gitignore/.kernignore for files too.
		if ign.Ignored(rel) {
			return nil
		}
		checkPath(relLower)

		base := filepath.Base(relLower)
		if manifestNames[base] {
			info, ierr := d.Info()
			if ierr != nil || info.Size() > 2<<20 {
				return nil
			}
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			low := strings.ToLower(string(body))
			// Only check dep signals for frameworks whose language matches this
			// manifest, so a JS package-lock.json can't trigger a Rust framework.
			for _, lang := range manifestLang[base] {
				for _, f := range depByLang[lang] {
					for _, dep := range f.Deps {
						if strings.Contains(low, dep) {
							addSignal(f.ID, "dep: "+dep)
						}
					}
				}
			}
			return nil
		}

		ext := filepath.Ext(relLower)
		if !sourceExts[ext] {
			return nil
		}
		if sourceCount >= sourceCap {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > 2<<20 {
			return nil
		}
		sourceCount++
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		// Skip minified/vendored bundles (e.g. redoc.standalone.js,
		// swagger-ui-bundle.js): their single-line-packed bodies match
		// generic code patterns like it(" anywhere, producing false
		// framework detections. A single line over 5000 chars is the
		// hallmark of minification.
		if isMinifiedSource(body) {
			return nil
		}
		low := strings.ToLower(string(body))
		lang := langForExt(ext)
		for _, f := range codeByLang[lang] {
			for _, code := range f.Code {
				if strings.Contains(low, code) {
					addSignal(f.ID, "code: "+code)
				}
			}
		}
		return nil
	})
	if werr != nil {
		return nil, werr
	}

	var out []Detected
	for _, f := range all {
		if sigs := matched[f.ID]; len(sigs) > 0 {
			var list []string
			for s := range sigs {
				list = append(list, s)
			}
			sort.Strings(list)
			out = append(out, Detected{Framework: *f, Signals: list})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lang != out[j].Lang {
			return out[i].Lang < out[j].Lang
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// isMinifiedSource reports whether a source file looks minified by checking
// for extremely long lines. It's a localized copy of the index package's
// isMinified heuristic so fw.Detect can skip vendored bundles (e.g.
// redoc.standalone.js, swagger-ui-bundle.js) whose packed bodies trip generic
// code patterns like it(" anywhere. A single line over 5000 chars, or 10+
// lines over 500 chars, is the hallmark of minification.
func isMinifiedSource(src []byte) bool {
	const (
		maxScanBytes = 1 << 20
		longLineLen  = 500
		singleLong   = 5000
		minLongLines = 10
	)
	if len(src) > maxScanBytes {
		src = src[:maxScanBytes]
	}
	longLines := 0
	lineStart := 0
	for i, b := range src {
		if b == '\n' {
			lineLen := i - lineStart
			if lineLen > singleLong {
				return true
			}
			if lineLen > longLineLen {
				longLines++
				if longLines >= minLongLines {
					return true
				}
			}
			lineStart = i + 1
		}
	}
	// Last line (no trailing newline).
	lastLen := len(src) - lineStart
	if lastLen > singleLong {
		return true
	}
	if lastLen > longLineLen {
		longLines++
	}
	return longLines >= minLongLines
}

// langForExt maps a source extension to a catalog language key.
func langForExt(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".rs":
		return "rust"
	case ".cs", ".csproj", ".fsproj":
		return "csharp"
	case ".java":
		return "java"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hxx":
		return "cpp"
	case ".vue", ".svelte", ".astro":
		return "js"
	}
	return "js" // .js, .ts, .tsx, .mjs, .cjs, .jsx
}
