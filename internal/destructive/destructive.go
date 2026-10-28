// Package destructive classifies the predicate-forwarding forms of otherwise
// read-only commands: find is a read tool by default, but -delete/-exec/-fprint
// destroy files, and xargs/tee/shred forward destruction from a harmless head.
// Extracted from internal/execclassify to respect that subsystem's
// ARCHITECTURE.md LOC cap. Kept deterministic and stdlib-only.
package destructive

import "strings"

// PredicateForwarding reports whether a find/xargs/tee/shred invocation
// deletes or overwrites files, with a human-readable reason. name is the
// resolved command name and rest its arguments (prefixes stripped).
func PredicateForwarding(name string, rest []string) (bool, string) {
	switch name {
	case "find":
		for i, t := range rest {
			switch t {
			case "-delete":
				return true, "find -delete removes matched entries"
			case "-exec", "-execdir", "-ok", "-okdir":
				if i+1 < len(rest) && IsDestructiveExecutable(rest[i+1]) {
					return true, "find " + t + " " + rest[i+1] + " removes/overwrites entries"
				}
			case "-fprint", "-fprintf", "-fls":
				return true, "find " + t + " writes files"
			}
		}
	case "xargs":
		for _, t := range rest {
			if !strings.HasPrefix(t, "-") && IsDestructiveExecutable(t) {
				return true, "xargs " + t + " removes/overwrites entries"
			}
		}
	case "tee":
		for _, t := range rest {
			if !strings.HasPrefix(t, "-") && t != "/dev/null" {
				return true, "tee overwrites " + t
			}
		}
	case "shred":
		return true, "shred overwrites files"
	case "dd":
		for _, t := range rest {
			if strings.HasPrefix(t, "of=") && !strings.HasPrefix(t, "of=/dev/null") {
				return true, "dd writes " + strings.TrimPrefix(t, "of=")
			}
		}
	case "git":
		if len(rest) == 0 {
			return false, ""
		}
		switch rest[0] {
		case "clean":
			for _, t := range rest[1:] {
				if strings.HasPrefix(t, "-") && (strings.Contains(t, "f") || strings.Contains(t, "d") || strings.Contains(t, "x")) {
					return true, "git clean removes untracked files"
				}
			}
		case "reset":
			for _, t := range rest[1:] {
				if t == "--hard" {
					return true, "git reset --hard discards working-tree changes"
				}
			}
		case "push":
			for _, t := range rest[1:] {
				if t == "--force" || t == "-f" {
					return true, "git push --force rewrites remote history"
				}
			}
		}
	case "python", "python3", "node", "ruby", "perl", "php":
		// Inline interpreter code is the documented residual risk of a lexical
		// classifier, but the common deletion forms are cheap to catch — and
		// leaving them out made `python3 -c "shutil.rmtree(...)"` runnable via
		// the governed-exec path while the bash guard gated it (a parity
		// bypass). A bar-raiser, not a sandbox.
		for _, t := range rest {
			switch {
			case strings.Contains(t, "rmtree("),
				strings.Contains(t, "os.remove("),
				strings.Contains(t, "os.unlink("),
				strings.Contains(t, "unlink("),
				strings.Contains(t, "rmSync("),
				strings.Contains(t, "rm -rf"):
				return true, name + " inline code removes files"
			}
		}
	}
	return false, ""
}

// IsDestructiveExecutable reports whether a find -exec / xargs target is a
// tool that removes or overwrites files. Path prefixes are stripped so
// /bin/rm matches.
func IsDestructiveExecutable(s string) bool {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	switch s {
	case "rm", "shred", "truncate", "dd", "mv", "tee":
		return true
	}
	return false
}
