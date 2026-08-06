package fw

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// sourceExts are files scanned for Code signals.
var sourceExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".mjs": true, ".cjs": true,
	".jsx": true, ".ts": true, ".tsx": true, ".vue": true, ".svelte": true,
	".astro": true, ".rb": true, ".php": true, ".rs": true, ".cs": true,
	".java": true, ".c": true, ".h": true, ".cc": true, ".cpp": true,
	".cxx": true, ".hpp": true, ".hxx": true, ".csproj": true, ".fsproj": true,
}

var fwIgnoreDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"vendor": true, "dist": true, "build": true, "out": true, "target": true,
	".next": true, "__pycache__": true, ".venv": true, "venv": true,
	".cache": true, ".idea": true, ".vscode": true, "bin": true,
	".mvn": true, "coverage": true, "tmp": true, ".terraform": true,
}

// Detect walks root and reports every catalog framework revealed by file
// markers, dependency-manifest entries or source-code markers. It mirrors the
// indexer's ignore rules and caps how much source it reads.
func Detect(root string) ([]Detected, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	// Precompute per-language code markers and a path-marker index.
	codeByLang := map[string][]*Framework{}
	var all []*Framework
	for i := range catalog {
		f := &catalog[i]
		all = append(all, f)
		if len(f.Code) > 0 {
			codeByLang[f.Lang] = append(codeByLang[f.Lang], f)
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
		relLower := strings.ToLower(rel)
		if d.IsDir() {
			if path != abs && fwIgnoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			// A directory marker like "app/controllers" still counts as a path
			// signal, so check it before skipping into it.
			checkPath(relLower + "/")
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
			for _, f := range all {
				for _, dep := range f.Deps {
					if strings.Contains(low, dep) {
						addSignal(f.ID, "dep: "+dep)
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
	case ".cs":
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
