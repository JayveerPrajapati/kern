// Git post-commit integration: installs a post-commit hook that compresses
// each new commit's diff into the project's cross-session memory, so agents
// inherit "what changed" without reading the full history.
package hook

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

const maxDiffLines = 200

// kernManagedMarker identifies hook files installed by kern (local
// .git/hooks scripts and the global ~/.kern/git-hooks scripts). Install and
// Uninstall both rely on it so a kern-managed hook can be overwritten or
// removed while a user-authored hook is never touched.
const kernManagedMarker = "# kern:"

// Install writes a post-commit hook into the repo at root.
func Install(root string) error {
	if !isGitRepo(root) {
		return fmt.Errorf("%s is not a git repository", root)
	}
	hookDir := filepath.Join(root, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return err
	}
	hook := filepath.Join(hookDir, "post-commit")
	// Resolve kern through PATH at runtime so the hook keeps working when the
	// installing binary is moved or upgraded (the recorded absolute path is
	// only a fallback). An explicit KERN_BINARY env override wins, matching
	// the global hook scripts' semantics.
	script := fmt.Sprintf(`#!/bin/sh
# kern: compress the new commit's diff into project memory (installed by kern hook install)
# Resolve kern via PATH at runtime (KERN_BINARY env override wins); fall back
# to the path recorded at install time only when PATH resolution fails, so
# the hook survives binary relocation.
kern_bin="${KERN_BINARY:-}"
if [ -z "$kern_bin" ]; then
  kern_bin=$(command -v kern 2>/dev/null) || kern_bin="%s"
fi
"$kern_bin" hook store --range "HEAD~1..HEAD" >/dev/null 2>&1 || true
`, kernBinPath())
	// Refuse to clobber an existing user-authored hook. A kern-installed hook
	// is identified by its kernManagedMarker, so re-running install (e.g.
	// after an upgrade) overwrites cleanly. The marker is on the second line
	// (after the #!/bin/sh shebang), so it is detected with Contains rather
	// than HasPrefix.
	if b, err := os.ReadFile(hook); err == nil {
		if !strings.Contains(string(b), kernManagedMarker) {
			return fmt.Errorf("post-commit hook already exists at %s and is not kern-managed; remove it first or merge manually", hook)
		}
	}
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		return err
	}
	return nil
}

// Uninstall removes kern-installed hooks from the repo's .git/hooks. Only
// hook files carrying a kern-installed marker are removed; a user-authored
// hook (or any other foreign script) is left untouched and reported as an
// error so the caller can refuse loudly. Returns the names of the hooks
// removed ("" slice when nothing kern-managed was present).
func Uninstall(root string) ([]string, error) {
	if !isGitRepo(root) {
		return nil, fmt.Errorf("%s is not a git repository", root)
	}
	hookDir := filepath.Join(root, ".git", "hooks")
	var removed []string
	// post-commit is what `kern hook install` writes; pre-commit/pre-push are
	// what `kern install hook` writes. Both carry kern markers.
	for _, name := range []string{"post-commit", "pre-commit", "pre-push"} {
		p := filepath.Join(hookDir, name)
		b, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		if !strings.Contains(string(b), kernManagedMarker) && !strings.Contains(string(b), "# Blueprint") {
			return removed, fmt.Errorf("%s exists and is not kern-managed; refusing to remove it — remove it manually if you intend to", p)
		}
		if err := os.Remove(p); err != nil {
			return removed, err
		}
		removed = append(removed, name)
	}
	return removed, nil
}

func isGitRepo(root string) bool {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func kernBinPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "kern"
	}
	// On Windows the binary is kern.exe; everywhere else it is extensionless.
	name := "kern"
	if runtime.GOOS == "windows" {
		name = "kern.exe"
	}
	abs := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	return "kern"
}

// Diff returns the compressed diff for from..to (defaults HEAD~1..HEAD),
// keeping only file headers, hunk headers and added/removed lines.
func Diff(from, to string) (string, error) {
	if from == "" {
		from = "HEAD~1"
	}
	if to == "" {
		to = "HEAD"
	}
	cmd := exec.Command("git", "diff", "--unified=0", from+".."+to)
	out, err := cmd.Output()
	if err != nil {
		// No commits yet: fall back to working-tree changes.
		out, err = exec.Command("git", "diff").Output()
		if err != nil {
			return "", fmt.Errorf("git diff failed: %w", err)
		}
	}
	return compressDiff(string(out)), nil
}

func compressDiff(d string) string {
	var b strings.Builder
	n := 0
	lines := strings.Split(d, "\n")
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "diff --git"):
			b.WriteString("## " + strings.TrimPrefix(l, "diff --git ") + "\n")
			n++
		case strings.HasPrefix(l, "@@"):
			b.WriteString(l + "\n")
			n++
		case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			b.WriteString(l + "\n")
			n++
		case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
			b.WriteString(l + "\n")
			n++
		}
		if n >= maxDiffLines {
			b.WriteString("… (diff truncated)\n")
			break
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// Store compresses the diff and records it in the project's memory. The diff
// content is untrusted (it can contain arbitrary commit data), so it is
// sanitized before persisting to prevent the learning extractor from promoting
// raw control characters or injected content into trusted constraints.
func Store(root, from, to string) error {
	d, err := Diff(from, to)
	if err != nil {
		return err
	}
	if strings.TrimSpace(d) == "" {
		return nil
	}
	return memory.AddAuto(root, "latest change:\n"+sanitize(d))
}

// maxStoredContent is the maximum length of persisted commit content.
const maxStoredContent = 500

// sanitize neutralizes untrusted commit diff content before it is stored in
// project memory. It collapses newlines to spaces, strips control characters,
// and truncates to a reasonable length so raw commit bytes cannot be promoted
// into a trusted constraint by the learning extractor. Mirrors the sanitization
// used for loop intents (internal/loop sanitizeIntent).
func sanitize(d string) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r':
			return ' '
		case r < 0x20 || r == 0x7f: // control characters
			return -1
		default:
			return r
		}
	}, d)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxStoredContent {
		s = s[:maxStoredContent]
	}
	return s
}
