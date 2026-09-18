package fw

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WithGoStdlib guarantees a Go project reports the Go language itself, not
// just the frameworks layered on top of it ("No known frameworks
// detected" for a plain Go module whose go.mod has no framework
// dependencies). A Go module without gin/echo/grpc/... still runs on the
// standard library, so it gets a "Go (stdlib)" baseline entry alongside any
// framework entries the catalog already found. Signal-less projects keep the
// pre-existing "No known frameworks detected" message.
func WithGoStdlib(root string, det []Detected) []Detected {
	for _, d := range det {
		if d.ID == "go-stdlib" {
			return det
		}
	}
	if !isGoProject(root) {
		return det
	}
	out := append([]Detected(nil), det...)
	out = append(out, Detected{
		Framework: Framework{
			ID:      "go-stdlib",
			Name:    "Go (stdlib)",
			Lang:    "go",
			Summary: "Go standard library: modules and programs with no third-party framework.",
		},
		Signals: []string{"go.mod / *.go"},
	})
	// Match Detect's ordering contract (lang, then name) so Render groups
	// languages cleanly even when a Go framework was also detected.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lang != out[j].Lang {
			return out[i].Lang < out[j].Lang
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// DetectWithStdlib is the shared detect+enrich sequence every surface (CLI
// `kern fw`, MCP `kern_frameworks`) must use, so the Go stdlib baseline
// cannot drift between surfaces again.
func DetectWithStdlib(root string) ([]Detected, error) {
	det, err := Detect(root)
	if err != nil {
		return nil, err
	}
	return WithGoStdlib(root, det), nil
}

// isGoProject reports whether root contains Go source: a go.mod manifest or
// .go files. The walk is bounded and skips the same baseline dirs the
// detector skips (VCS, dependency, build output) so node_modules/vendor
// trees cannot masquerade as Go projects.
func isGoProject(root string) bool {
	if st, err := os.Stat(filepath.Join(root, "go.mod")); err == nil && !st.IsDir() {
		return true
	}
	const depth = 3
	skip := map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true, "dist": true, "build": true, "out": true, "bin": true, ".venv": true, "__pycache__": true, ".kern": true, "target": true}
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if path != root {
				rel, _ := filepath.Rel(root, path)
				if skip[rel] || strings.Count(rel, string(filepath.Separator)) >= depth {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "go.mod") {
			found = true
		}
		return nil
	})
	return found
}
