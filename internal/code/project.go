package code

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/ignore"
)

// Project is a compact representation of a whole repository.
type Project struct {
	Root     string    `json:"root"`
	Files    []Summary `json:"files"`
	Ignored  int       `json:"ignored"`
	CacheHit int       `json:"cache_hit"`
	Capped   bool      `json:"capped,omitempty"` // maxFiles cap reached during build
}

var ignoreDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"vendor": true, "dist": true, "build": true, "out": true, "target": true,
	".next": true, "__pycache__": true, ".venv": true, "venv": true,
	".cache": true, ".idea": true, ".vscode": true, "bin": true,
	".mvn": true, "coverage": true, "tmp": true, ".terraform": true,
	".kern": true,
}

var ignoreFiles = map[string]bool{
	"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"go.sum": true, "Cargo.lock": true, "Pipfile.lock": true, "poetry.lock": true,
	"Gemfile.lock": true, ".DS_Store": true, "*.min.js": true, "*.min.css": true,
}

// BuildProject walks root and returns a compact project summary, using the
// persistent cache so unchanged files cost nothing to re-summarize.
func BuildProject(root string, maxFiles, maxSymbolsPerFile int) (*Project, error) {
	if maxFiles <= 0 {
		maxFiles = 10000
	}
	p := &Project{Root: root}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	ig := ignore.Load(root)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if path != root && (ignoreDirs[d.Name()] || ig.Ignored(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(p.Files) >= maxFiles {
			p.Capped = true
			return filepath.SkipAll
		}
		if shouldIgnore(rel) || ig.Ignored(rel) {
			p.Ignored++
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > 2<<20 {
			if ierr == nil {
				p.Ignored++
			}
			return nil
		}
		sum, hit := summarizeCached(path, rel, maxSymbolsPerFile)
		if hit {
			p.CacheHit++
		}
		p.Files = append(p.Files, sum)
		return nil
	})
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	return p, err
}

func summarizeCached(abs, rel string, maxSymbols int) (Summary, bool) {
	content, err := ReadFile(abs)
	if err != nil {
		return Summary{Path: rel}, false
	}
	h := cache.Hash(content)
	var cached Summary
	if err := cache.Load("code/"+h, &cached); err == nil {
		cached.Path = rel
		return cached, true
	}
	sum := Summarize(abs, content, maxSymbols)
	sum.Path = rel
	// Cache under the content hash so the path-specific copy stays current.
	_ = cache.Store("code/"+h, sum)
	return sum, false
}

func shouldIgnore(rel string) bool {
	if ignoreFiles[rel] {
		return true
	}
	name := filepath.Base(rel)
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".min.js") || strings.HasSuffix(name, ".min.css") {
		return true
	}
	// rel is slash-normalized by the caller, so split on "/" rather than
	// os.PathSeparator.
	for _, seg := range strings.Split(rel, "/") {
		if ignoreDirs[seg] {
			return true
		}
	}
	return false
}

// ShouldIgnore reports whether a repository-relative path is excluded from
// project walks (VCS/build dirs, lockfiles, dotfiles, minified assets).
func ShouldIgnore(rel string) bool { return shouldIgnore(rel) }

// IsIgnoredDir reports whether a directory basename is excluded from project
// walks (e.g. .git, node_modules, vendor, dist).
func IsIgnoredDir(name string) bool { return ignoreDirs[name] }

// Render renders the project map as compact text.
func (p *Project) Render() string {
	var b strings.Builder
	b.WriteString("Project: ")
	b.WriteString(p.Root)
	b.WriteString(" (")
	b.WriteString(itoa(len(p.Files)))
	b.WriteString(" files")
	if p.Ignored > 0 {
		b.WriteString(", ")
		b.WriteString(itoa(p.Ignored))
		b.WriteString(" ignored")
	}
	if p.Capped {
		b.WriteString(", file cap reached (raise max_files)")
	}
	if p.CacheHit > 0 {
		b.WriteString(", ")
		b.WriteString(itoa(p.CacheHit))
		b.WriteString(" from cache")
	}
	b.WriteString(")\n")
	for _, f := range p.Files {
		if r := f.Render(); r != "" {
			b.WriteString(r)
			b.WriteString("\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
