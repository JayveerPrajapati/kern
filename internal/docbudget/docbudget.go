// Package docbudget enforces the "one home per fact" documentation budget
// (the kern analogue of dsh's verify-doc-budgets): a committed manifest
// lists each key document with a word ceiling, and the doc:budget gate
// BLOCKs when a listed document exceeds its ceiling or goes missing. Word
// counts exclude fenced code blocks so prose budgets measure prose.
package docbudget

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ManifestRelPath is the repo-relative manifest path (slash-separated).
const ManifestRelPath = "docs/doc-budgets.json"

// Rule is one document budget: a repo-relative path and its word ceiling.
type Rule struct {
	Path     string `json:"path"`
	MaxWords int    `json:"max_words"`
}

// Manifest is the ordered list of document budgets.
type Manifest struct {
	Rules []Rule `json:"rules"`
}

// Violation is one budget finding.
type Violation struct {
	Path    string
	Current int
	Max     int
	Message string
}

// Load reads the manifest from root/docs/doc-budgets.json. A missing file
// returns an empty (valid) manifest.
func Load(root string) (Manifest, error) {
	path := filepath.Join(root, filepath.FromSlash(ManifestRelPath))
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Manifest{}, nil
		}
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %v", path, err)
	}
	return m, nil
}

// CountWords counts whitespace-separated tokens in a markdown file,
// excluding content inside fenced code blocks (``` ... ```).
func CountWords(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	inFence := false
	n := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if line == "" {
			continue
		}
		n += len(strings.Fields(line))
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return n, nil
}

// Validate checks every manifest rule against the tree and returns all
// violations: missing documents and documents over their word ceiling.
func Validate(root string) ([]Violation, error) {
	m, err := Load(root)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for _, r := range m.Rules {
		if r.MaxWords <= 0 {
			out = append(out, Violation{Path: r.Path, Message: "max_words must be positive"})
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(r.Path))
		count, err := CountWords(abs)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				out = append(out, Violation{Path: r.Path, Message: "document listed in doc-budgets.json is missing"})
				continue
			}
			out = append(out, Violation{Path: r.Path, Message: err.Error()})
			continue
		}
		if count > r.MaxWords {
			out = append(out, Violation{
				Path:    r.Path,
				Current: count,
				Max:     r.MaxWords,
				Message: fmt.Sprintf("%d words exceeds the %d-word ceiling", count, r.MaxWords),
			})
		}
	}
	return out, nil
}
