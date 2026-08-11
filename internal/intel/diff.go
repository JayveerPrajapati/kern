package intel

import (
	"os/exec"
	"strconv"
	"strings"
)

// LineRange is an inclusive 1-based range of source lines.
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// FileChange pairs a changed file with the added-line ranges from its diff.
// An empty Ranges slice means "whole file changed" — used when no diff line
// information is available (working-tree edits, deletion-only hunks, --file).
type FileChange struct {
	File   string      `json:"file"`
	Ranges []LineRange `json:"ranges,omitempty"`
}

// FilesForRangeL returns the changed files with their added-line ranges.
// An empty range means the working tree (git diff HEAD).
func FilesForRangeL(root, from, to string) ([]FileChange, error) {
	var cmdArgs []string
	if from == "" && to == "" {
		cmdArgs = []string{"diff", "-U0", "HEAD"}
	} else {
		cmdArgs = []string{"diff", "-U0", from + ".." + to}
	}
	out, err := exec.Command("git", append([]string{"-C", root}, cmdArgs...)...).Output()
	if err != nil {
		// No usable diff line info (e.g. unborn branch): fall back to
		// whole-file changes via the name-only listing.
		files, ferr := FilesForRange(root, from, to)
		if ferr != nil {
			return nil, ferr
		}
		var changes []FileChange
		for _, f := range files {
			changes = append(changes, FileChange{File: f})
		}
		return changes, nil
	}
	return parseDiffOutput(string(out)), nil
}

// parseDiffOutput extracts per-file added-line ranges from `git diff -U0`
// output. Only added lines (the "+" side of each hunk) are kept: those are the
// lines whose symbols the change actually touched. Context lines widen the
// hunk header window but are not changes.
func parseDiffOutput(out string) []FileChange {
	var changes []FileChange
	last := -1
	newLine := 0
	inHunk := false
	curTo := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			_, curTo = diffPaths(line)
		case strings.HasPrefix(line, "Binary files "):
			// The new-side path comes from this line (`Binary files a/X and
			// b/Y differ`), not the diff --git header: a deletion shows the
			// old name there and /dev/null here. Deletions (to == /dev/null)
			// are dropped like their text counterparts.
			rest := strings.TrimSuffix(strings.TrimPrefix(line, "Binary files "), " differ")
			to := curTo
			if i := strings.LastIndex(rest, " and "); i >= 0 {
				to = unquotePath(rest[i+len(" and "):])
			}
			if to != "" && to != "/dev/null" {
				changes = append(changes, FileChange{File: to})
				last = len(changes) - 1
			}
			inHunk = false
		case strings.HasPrefix(line, "+++ "):
			path := unquotePath(strings.TrimPrefix(line, "+++ "))
			inHunk = false
			if path == "/dev/null" {
				last = -1 // deleted file: whole-file handled by caller fallback
				continue
			}
			changes = append(changes, FileChange{File: path})
			last = len(changes) - 1
		case strings.HasPrefix(line, "@@ "):
			start, count, ok := hunkNewRange(line)
			inHunk = ok && last >= 0
			if inHunk {
				newLine = start
				_ = count
			}
		case inHunk && last >= 0:
			if len(line) == 0 {
				continue
			}
			switch line[0] {
			case ' ':
				newLine++
			case '+':
				addRange(&changes[last], newLine)
				newLine++
			case '-', '\\':
				// no advance: deletions and "\ No newline at end of file"
			default:
				inHunk = false
			}
		}
	}
	return changes
}

// diffPaths parses the from/to paths out of a `diff --git a/X b/Y` header.
// Paths are matched from the right so a path containing " b/" is not split.
func diffPaths(line string) (from, to string) {
	rest := strings.TrimPrefix(line, "diff --git a/")
	i := strings.LastIndex(rest, " b/")
	if i < 0 {
		return "", ""
	}
	return unquotePath(rest[:i]), unquotePath(rest[i+3:])
}

// addRange records an added line, merging it with the previous range when
// contiguous.
func addRange(c *FileChange, line int) {
	if n := len(c.Ranges); n > 0 && c.Ranges[n-1].End+1 == line {
		c.Ranges[n-1].End = line
		return
	}
	c.Ranges = append(c.Ranges, LineRange{Start: line, End: line})
}

// hunkNewRange extracts the new-side start line and count from a
// `@@ -a,b +c,d @@` header. ok is false for pure-deletion hunks.
func hunkNewRange(line string) (start, count int, ok bool) {
	i := strings.IndexByte(line, '+')
	if i < 0 {
		return 0, 0, false
	}
	rest := line[i+1:]
	if end := strings.IndexByte(rest, ' '); end >= 0 {
		rest = rest[:end]
	}
	start = 1
	if p := strings.SplitN(rest, ",", 2); len(p) == 1 {
		start, _ = strconv.Atoi(p[0])
		count = 1
	} else {
		start, _ = strconv.Atoi(p[0])
		count, _ = strconv.Atoi(p[1])
	}
	return start, count, count > 0
}

func unquotePath(p string) string {
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			return strings.TrimPrefix(u, "b/")
		}
	}
	return strings.TrimPrefix(p, "b/")
}
