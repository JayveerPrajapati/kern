// Package guardpaths is the guard's path-aware write analysis: it decides
// whether one command segment is a local file-shape write (rm/mv/cp/
// mkdir/touch, sed in-place) whose every path argument resolves inside
// the project root or a temp dir — the same intent the hosts' native edit
// tools already exercise ungated. Extracted from internal/guardrule when
// the classifier outgrew its ARCHITECTURE.md LOC cap: guardrule owns the
// command-level verdict flow (splitStages, simple-command checks), this
// package owns the per-segment path math. Everything .git, credential
// shaped, recursive-at-top-level, glob-bearing or outside root/temp is
// rejected — those keep the flat GATE verdict.
package guardpaths

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/execclassify"
)

// pathWriteCMDs are the local file-shape writes whose risk is entirely
// path-scoped: where they write decides whether they are safe. Disk-level
// (dd/mkfs/shred/truncate), permission (chmod/chown) and network
// (curl/wget) commands are NOT here — they stay flat GATE.
var pathWriteCMDs = map[string]bool{
	"rm": true, "mv": true, "cp": true, "mkdir": true, "touch": true,
}

// credentialFragments mark paths that must never be an argument of an
// agent-run write command: ssh/aws/gnupg material, env files, keys.
var credentialFragments = []string{
	".ssh", ".aws", ".gnupg", ".env", ".pem", ".key", "id_rsa", "id_ed25519",
	".netrc", ".npmrc", ".pypirc", ".htpasswd", "credentials.json",
	".git-credentials", ".gitconfig", "service-account", "secrets.",
}

// WriteSegment reports whether the segment's command is a path-write at
// all (rm/mv/cp/mkdir/touch, or sed with an in-place flag). The caller
// pairs it with SafeWriteSegment to detect an UNSAFE write: a command
// whose whole point is a write to somewhere disallowed must gate, not
// ride a governed verdict.
func WriteSegment(seg string) bool {
	toks := execclassify.StripPrefixes(execclassify.ShellTokens(seg))
	if len(toks) == 0 {
		return false
	}
	return pathWriteCMDs[toks[0]] || (toks[0] == "sed" && HasSedinPlaceFlag(toks[1:]))
}

// ExtraRootsFromEnv returns additional workspace roots the operator
// configured (KERN_MCP_ROOTS, plus the historical KERN_ROOTS alias;
// ':'- or ','-separated, deduplicated, cleaned and made absolute
// against the process cwd). The guard threads them into the path-aware
// write arm so a command targeting a second configured root — the MCP
// server's documented confinement variable — classifies against a root
// it is actually inside, instead of falling to the flat out-of-root
// GATE. Deliberately env-only: this package stays stdlib-plus-execclassify;
// mcp.roots inside .kern/config.json is server-side state the guard
// process cannot see. Parity with internal/mcp's workspaceRootsFromEnv
// split semantics.
func ExtraRootsFromEnv(getenv func(string) string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		for _, r := range strings.FieldsFunc(v, func(r rune) bool { return r == ':' || r == ',' }) {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			abs, err := filepath.Abs(r)
			if err != nil {
				continue
			}
			abs = filepath.Clean(abs)
			if !seen[abs] {
				seen[abs] = true
				out = append(out, abs)
			}
		}
	}
	add(getenv("KERN_MCP_ROOTS"))
	add(getenv("KERN_ROOTS"))
	return out
}

// HasSedinPlaceFlag reports whether sed args request in-place editing.
func HasSedinPlaceFlag(args []string) bool {
	for _, a := range args {
		if a == "--in-place" || a == "-i" || strings.HasPrefix(a, "--in-place=") {
			return true
		}
		if len(a) > 1 && strings.HasPrefix(a, "-i") && !strings.HasPrefix(a, "--") {
			return true // -i, -i'', -ibak
		}
	}
	return false
}

// SafeWriteSegment reports whether ONE command segment is a pure
// path-write whose every path argument is safe: inside the project root
// or the temp dir, never the root itself, never under .git, never
// credential-shaped, no glob/expansion characters, and (for recursive rm)
// only nested paths — an agent must not be able to wipe a top-level
// directory in one verdict. extraRoots (KERN_MCP_ROOTS) are additional
// accepted roots for ABSOLUTE targets only: relative paths always belong
// to the primary root, and no path may resolve AT a configured root.
func SafeWriteSegment(seg, root string, extraRoots []string) bool {
	toks := execclassify.StripPrefixes(execclassify.ShellTokens(seg))
	if len(toks) == 0 {
		return false
	}
	name := toks[0]
	if name == "sed" {
		return safeSedInPlaceSegment(toks[1:], root, extraRoots)
	}
	if !pathWriteCMDs[name] {
		return false
	}
	rest := toks[1:]
	var paths []string
	var recursive bool
	sawDD := false
	for _, t := range rest {
		if t == "--" {
			sawDD = true
			continue
		}
		if !sawDD && strings.HasPrefix(t, "-") && t != "-" {
			// flag cluster: detect recursion for rm (-r/-R anywhere in a
			// short cluster, or --recursive)
			if t == "--recursive" || t == "-r" || t == "-R" {
				recursive = true
			} else if len(t) > 1 && !strings.HasPrefix(t, "--") {
				for _, c := range t[1:] {
					if c == 'r' || c == 'R' {
						recursive = true
					}
				}
			}
			continue
		}
		paths = append(paths, t)
	}
	if len(paths) == 0 {
		return false
	}
	// cp writes only its LAST path argument; the others are read sources.
	// mv moves — both ends must stay in scope. rm/mkdir/touch touch all.
	destCount := len(paths)
	if name == "cp" {
		destCount = 1
	}
	for i, p := range paths {
		if !safeWritePath(p, root, extraRoots) {
			return false
		}
		if i >= len(paths)-destCount {
			// destination side: additionally never a workspace root
			// itself and never a top-level entry (of the primary root)
			// for recursive rm.
			res, ok := ResolveInRoot(p, root, extraRoots)
			if !ok || isWorkspaceRoot(res, root, extraRoots) {
				return false
			}
			if name == "rm" && recursive && !strings.Contains(strings.TrimSuffix(p, "/"), "/") {
				return false // recursive rm of a top-level path — GATE
			}
		}
	}
	return true
}

// safeSedInPlaceSegment validates a `sed -i <script> <files>` argument
// list: the first non-flag argument is the sed program (not a target);
// every later non-flag argument is a file being rewritten in place.
func safeSedInPlaceSegment(args []string, root string, extraRoots []string) bool {
	if !HasSedinPlaceFlag(args) {
		return false
	}
	var paths []string
	sawDD := false
	sawProgram := false
	for _, t := range args {
		if t == "--" {
			sawDD = true
			continue
		}
		if !sawDD && strings.HasPrefix(t, "-") && t != "-" && !sawProgram {
			continue // sed flags (before the program argument)
		}
		if !sawProgram {
			sawProgram = true
			continue // the sed script itself — not a write target
		}
		paths = append(paths, t)
	}
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !safeWritePath(p, root, extraRoots) {
			return false
		}
		res, ok := ResolveInRoot(p, root, extraRoots)
		if !ok || isWorkspaceRoot(res, root, extraRoots) {
			return false
		}
	}
	return true
}

// safeWritePath checks ONE argument of a write command: no expansion
// characters, no credential shape, resolves inside root or the temp dir,
// and no .git path segment.
func safeWritePath(p, root string, extraRoots []string) bool {
	if p == "" || p == "-" {
		return false
	}
	if strings.HasPrefix(p, "~") {
		return false
	}
	for _, c := range "$`*?[{\\<" {
		if strings.ContainsRune(p, c) {
			return false
		}
	}
	low := strings.ToLower(p)
	for _, frag := range credentialFragments {
		if strings.Contains(low, frag) {
			return false
		}
	}
	res, ok := ResolveInRoot(p, root, extraRoots)
	if !ok {
		return false
	}
	for _, seg := range strings.Split(res, string(filepath.Separator)) {
		if seg == ".git" {
			return false
		}
	}
	return true
}

// isWorkspaceRoot reports whether res is the primary root or one of the
// configured extra roots: no write may target a workspace root itself.
func isWorkspaceRoot(res, root string, extraRoots []string) bool {
	if res == "" {
		return false
	}
	if res == filepath.Clean(root) {
		return true
	}
	for _, r := range extraRoots {
		if res == r {
			return true
		}
	}
	return false
}

// ResolveInRoot cleans p against root. Returns ok=false when the path
// escapes the root or a temp dir (absolute paths must already be inside
// one of them). /tmp, /var/tmp and os.TempDir() are all accepted: agents
// routinely use /tmp for scratch even when TMPDIR points elsewhere.
// extraRoots (KERN_MCP_ROOTS) are accepted for absolute targets only.
func ResolveInRoot(p, root string, extraRoots []string) (string, bool) {
	abs := p
	if !filepath.IsAbs(abs) {
		// A relative path may never climb out of the root with ".." — the
		// temp-dir allowance below is for explicit absolute /tmp targets,
		// not for escapes that happen to land under TMPDIR (test roots).
		// Relative paths belong to the primary root only: a relative path
		// cannot name a second workspace root, so extras stay
		// absolute-only.
		c := filepath.Clean(p)
		if c == ".." || strings.HasPrefix(c, ".."+string(filepath.Separator)) {
			return "", false
		}
		abs = filepath.Join(root, p)
	}
	clean := filepath.Clean(abs)
	rootClean := filepath.Clean(root)
	if clean == rootClean || strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
		return clean, true
	}
	for _, r := range extraRoots {
		if clean == r || strings.HasPrefix(clean, r+string(filepath.Separator)) {
			return clean, true
		}
	}
	for _, tmp := range tempRoots() {
		if strings.HasPrefix(clean, tmp+string(filepath.Separator)) {
			return clean, true
		}
	}
	return "", false
}

func tempRoots() []string {
	return []string{"/tmp", "/var/tmp", filepath.Clean(os.TempDir())}
}
