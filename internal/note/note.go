// Package note implements governed decision records — the kern analogue of
// dsh's Agent Notes. A note is a committed markdown file at
// docs/notes/{lifecycle}/{class}/yyyy-mm-dd-topic-title.md whose path
// encodes its status (lifecycle folder) and kind (class folder), with a
// gate-enforced in-file format. Lifecycle folders: proposed → implemented →
// archived (rejected is terminal). Implemented notes stay current with
// shipped reality; archived notes are frozen.
package note

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Lifecycle is the path-encoded status of a note; it must agree with the
// top-level folder the file lives in.
type Lifecycle string

// Lifecycles are the supported lifecycle folders, in canonical order.
var Lifecycles = []Lifecycle{Proposed, Implemented, Rejected, Archived}

const (
	Proposed    Lifecycle = "proposed"
	Implemented Lifecycle = "implemented"
	Rejected    Lifecycle = "rejected"
	Archived    Lifecycle = "archived"
)

// Class is the kind of decision a note records; the folder set is closed.
type Class string

// Classes are the supported class folders, in canonical order.
var Classes = []Class{Feature, BugFix, Simplification, Architecture, Process, Testing}

const (
	Feature        Class = "feature"
	BugFix         Class = "bug-fix"
	Simplification Class = "simplification"
	Architecture   Class = "architecture"
	Process        Class = "process"
	Testing        Class = "testing"
)

// TreeRelPath is the docs/notes-relative path for a note.
const TreeRelPath = "docs/notes"

// filenameRe validates yyyy-mm-dd-topic-title.md.
var filenameRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-([a-z0-9-]+)\.md$`)

// Violation is one gate finding against a note or the tree.
type Violation struct {
	Path    string
	Message string
}

// Note is one parsed decision record.
type Note struct {
	Path      string
	Title     string
	Status    string
	Lifecycle Lifecycle
	Class     Class
	Date      string
	Slug      string
}

// ValidateLifecycle reports whether lc is a supported lifecycle.
func ValidateLifecycle(lc Lifecycle) error {
	for _, l := range Lifecycles {
		if l == lc {
			return nil
		}
	}
	return fmt.Errorf("unknown lifecycle %q (use proposed|implemented|rejected|archived)", lc)
}

// ValidateClass reports whether c is a supported class.
func ValidateClass(c Class) error {
	for _, k := range Classes {
		if k == c {
			return nil
		}
	}
	return fmt.Errorf("unknown class %q (use feature|bug-fix|simplification|architecture|process|testing)", c)
}

// Slug derives a filename slug from a title: lowercase, non-alphanumerics to
// dashes, runs collapsed, leading/trailing dashes trimmed.
func Slug(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// PathFor builds the docs/notes-relative path for a new note.
func PathFor(lc Lifecycle, c Class, date, title string) (string, error) {
	if err := ValidateLifecycle(lc); err != nil {
		return "", err
	}
	if err := ValidateClass(c); err != nil {
		return "", err
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", fmt.Errorf("date must be yyyy-mm-dd: %w", err)
	}
	slug := Slug(title)
	if slug == "" {
		return "", fmt.Errorf("title must contain at least one alphanumeric character")
	}
	return filepath.ToSlash(filepath.Join(TreeRelPath, string(lc), string(c), date+"-"+slug+".md")), nil
}

// Skeleton renders the gate-conformant file body for a new note.
func Skeleton(lc Lifecycle, title, body string) string {
	status := string(lc)
	problem := strings.TrimSpace(body)
	if problem == "" {
		problem = "Describe the problem this note records the decision for."
	}
	return fmt.Sprintf("# Agent Note: %s\nStatus: %s\n\n## Problem\n%s\n", title, status, problem)
}

// ParseFile validates one note file against its path-encoded lifecycle and
// class and the in-file format contract. Path layout: docs/notes/{lifecycle}/{class}/{filename}.
func ParseFile(path string) (*Note, []Violation) {
	rel := filepath.ToSlash(path)
	parts := strings.Split(rel, "/")
	n := &Note{Path: rel}
	var v []Violation

	if len(parts) >= 3 {
		n.Lifecycle = Lifecycle(parts[len(parts)-3])
		n.Class = Class(parts[len(parts)-2])
	} else {
		v = append(v, Violation{Path: rel, Message: "path must be docs/notes/{lifecycle}/{class}/{file}"})
	}

	if err := ValidateLifecycle(n.Lifecycle); err != nil {
		v = append(v, Violation{Path: rel, Message: err.Error()})
	}
	if err := ValidateClass(n.Class); err != nil {
		v = append(v, Violation{Path: rel, Message: err.Error()})
	}

	name := parts[len(parts)-1]
	m := filenameRe.FindStringSubmatch(name)
	if m == nil {
		v = append(v, Violation{Path: rel, Message: "filename must be yyyy-mm-dd-topic-title.md"})
	} else {
		n.Date, n.Slug = m[1], m[2]
	}

	data, err := os.ReadFile(path)
	if err != nil {
		v = append(v, Violation{Path: rel, Message: fmt.Sprintf("read: %v", err)})
		return n, v
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "# Agent Note: ") {
		v = append(v, Violation{Path: rel, Message: "first line must be `# Agent Note: <title>`"})
	} else {
		n.Title = strings.TrimPrefix(lines[0], "# Agent Note: ")
	}
	if !strings.HasPrefix(lines[1], "Status: ") {
		v = append(v, Violation{Path: rel, Message: "second line must be `Status: <status>`"})
	} else {
		n.Status = strings.TrimSpace(strings.TrimPrefix(lines[1], "Status: "))
	}
	if strings.TrimSpace(lines[2]) != "" {
		v = append(v, Violation{Path: rel, Message: "third line must be blank"})
	}
	switch n.Lifecycle {
	case Archived:
		if n.Status != "implemented" {
			v = append(v, Violation{Path: rel, Message: fmt.Sprintf("archived status must be \"implemented\", got %q", n.Status)})
		}
	case Rejected:
		if !strings.HasPrefix(n.Status, "rejected — ") {
			v = append(v, Violation{Path: rel, Message: "rejected status must be `rejected — <reason, in one line>`"})
		}
	default:
		if n.Status != string(n.Lifecycle) {
			v = append(v, Violation{Path: rel, Message: fmt.Sprintf("status %q does not agree with lifecycle folder %q", n.Status, n.Lifecycle)})
		}
	}
	problemFound := false
	for _, l := range lines[3:] {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "## Problem") {
			problemFound = true
			break
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "Archived: ") {
			break // first non-blank, non-marker content must be the Problem heading
		}
	}
	if !problemFound {
		v = append(v, Violation{Path: rel, Message: "body must open with `## Problem`"})
	}
	if n.Lifecycle == Archived {
		found := false
		for _, l := range lines[2:] {
			if strings.HasPrefix(l, "Archived: ") {
				found = true
				break
			}
		}
		if !found {
			v = append(v, Violation{Path: rel, Message: "archived note must carry an `Archived: YYYY-MM-DD` line"})
		}
	}
	return n, v
}

// WalkNotes returns all note files under root/docs/notes, sorted by path.
func WalkNotes(root string) ([]string, error) {
	base := filepath.Join(root, filepath.FromSlash(TreeRelPath))
	var out []string
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		if strings.EqualFold(d.Name(), "README.md") {
			return nil // directory index/navigation file, not a decision note
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// ValidateTree scans the docs/notes tree and returns every violation found.
func ValidateTree(root string) ([]Violation, error) {
	paths, err := WalkNotes(root)
	if err != nil {
		return nil, err
	}
	var out []Violation
	for _, p := range paths {
		_, v := ParseFile(p)
		out = append(out, v...)
	}
	return out, nil
}
