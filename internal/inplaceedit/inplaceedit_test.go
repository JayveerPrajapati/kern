package inplaceedit

import (
	"path/filepath"
	"testing"
)

func TestTargetsCodeFile(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	yes := []string{
		"sed -i 's/a/b/' internal/x.go",
		"sed -i.bak 's/a/b/' internal/x.go",
		"sed --in-place 's/a/b/' internal/x.go",
		"sed -i -e 's/a/b/' cmd/kern/main.go",
		"perl -pi -e 's/a/b/' internal/x.go",
		"ruby -i -pe 'sub a, b' lib/x.rb",
		"sed -i 's/a/b/' " + filepath.Join(root, "internal/x.go"),
	}
	for _, cmd := range yes {
		if !TargetsCodeFile(cmd, root) {
			t.Errorf("TargetsCodeFile(%q) = false, want true", cmd)
		}
	}
	no := []string{
		"sed -i 's/a/b/' docs/notes.md",        // non-code
		"sed -i 's/a/b/' config/settings.yaml", // non-code
		"sed -n '1,5p' internal/x.go",          // not in place
		"sed 's/a/b/' internal/x.go",           // not in place
		"perl -e 'print 1' internal/x.go",      // not in place
		"sed -i 's/a/b/' ../outside/x.go",      // escapes the root
		"sed -i 's/a/b/' /tmp/x.go",            // scratch outside the root
		"cat internal/x.go",                    // not an editor
		"sed -i 's/x.go/y.go/' docs/a.md",      // script text is not a target
	}
	for _, cmd := range no {
		if TargetsCodeFile(cmd, root) {
			t.Errorf("TargetsCodeFile(%q) = true, want false", cmd)
		}
	}
}

func TestIsInPlace(t *testing.T) {
	yes := []string{"sed -i 's/a/b/' f", "sed -i.bak 's/a/b/' f", "perl -pi -e 's/a/b/' f", "ruby -i -pe 's/a/b/' f"}
	for _, cmd := range yes {
		if !IsInPlace(cmd) {
			t.Errorf("IsInPlace(%q) = false, want true", cmd)
		}
	}
	no := []string{"sed -n '1p' f", "perl -e 'print 1' f", "grep -i x f", "cat f"}
	for _, cmd := range no {
		if IsInPlace(cmd) {
			t.Errorf("IsInPlace(%q) = true, want false", cmd)
		}
	}
}
