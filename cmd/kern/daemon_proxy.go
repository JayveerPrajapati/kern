// daemon_proxy.go decides which CLI invocations are answered by the warm
// daemon instead of a fresh process.
//
// The rules, in order:
//
//  1. Only READ-ONLY commands are proxied (daemonProxyCommands). A mutating
//     command must never run against the daemon's shared index.
//  2. The proxy is skipped inside a daemon child (KERN_DAEMON_CHILD) — a
//     proxied command re-entering the proxy would loop.
//  3. KERN_DAEMON=0 disables it for one shell; unsupported platforms (Windows)
//     and any transport failure fall back to the in-process path silently.
//  4. A binary-identity mismatch retires the stale daemon and retries once;
//     a second failure falls back in-process rather than failing the command.
//
// The contract the callers rely on: daemonDispatch returns (response, true)
// ONLY when the daemon produced the command's real output. Anything else is
// (zero, false) and main() proceeds exactly as it would without the daemon.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/daemon"
)

// daemonProxyCommands is the read-only whitelist. Each entry must (a) answer
// from the index/graph only, (b) never write the store or the working tree,
// and (c) not read stdin. Adding a command here is a security decision: check
// (a)-(c) before adding it.
//
// The analysis family is included because those commands are pure reads of the
// loaded index/graph — and they are exactly the ones agents call repeatedly
// while exploring (measured: `kern refs` 0.70s cold vs ~0.05s warm on this
// repo, because the AST reference scan is memoized per index generation).
var daemonProxyCommands = map[string]bool{
	"search":       true,
	"context":      true,
	"compact":      true,
	"explore":      true,
	"graph":        true,
	"impact":       true,
	"dependencies": true,
	"why":          true,
	"refs":         true,
	"ast":          true,
	"fts":          true,
	"path":         true,
	"near":         true,
	"probe":        true,
	"hubs":         true,
	"dead":         true,
	"cycles":       true,
	"communities":  true,
	"flows":        true,
	"churn":        true,
	"cochange":     true,
	"trace":        true,
	"arch":         true,
	"project":      true,
	"project-map":  true,
}

// daemonCallTimeout bounds one proxied command. Reads are sub-second once
// hydrated; a longer wait means the command is doing real work (a rebuild on a
// cold index) and the caller deserves its output rather than a fallback.
const daemonCallTimeout = 120 * time.Second

// daemonProjectRoot resolves the project root by walking up from cwd to the
// nearest .kern store, bounded by the git worktree root. The daemon is
// per-project, so a command run from a subdirectory must reach the same socket
// as one run from the root.
//
// The .git boundary matters: a stale ancestor store (e.g. /private/tmp/.kern
// left by another project) must never capture every scratch project beneath
// it. Below a git root, only stores at or under that root are considered; with
// no git root, only the starting directory's store is — never an ancestor's.
// Falls back to cwd when nothing is found (in-process, no daemon).
func daemonProjectRoot() (string, bool) {
	start, err := os.Getwd()
	if err != nil {
		return "", false
	}
	gitRoot := nearestGitRoot(start)
	if gitRoot == "" {
		if hasKernStore(start) {
			return start, true
		}
		return start, true
	}
	for dir := start; ; dir = filepath.Dir(dir) {
		if hasKernStore(dir) {
			return dir, true
		}
		if dir == gitRoot {
			break
		}
	}
	return gitRoot, true
}

// nearestGitRoot returns the closest ancestor of dir (inclusive) carrying a
// .git entry, or "" when none is found within the walk bound.
func nearestGitRoot(dir string) string {
	for i := 0; i < 40; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func hasKernStore(dir string) bool {
	for _, probe := range []string{
		filepath.Join(dir, ".kern", "index.sqlite"),
		filepath.Join(dir, ".kern", "index.json"),
	} {
		if _, err := os.Stat(probe); err == nil {
			return true
		}
	}
	return false
}

// daemonDispatch tries to answer cmd via the daemon. ok=false means "run it
// in-process".
func daemonDispatch(cmd string, rest []string) (daemon.Response, bool) {
	if os.Getenv("KERN_DAEMON_CHILD") != "" || os.Getenv("KERN_DAEMON") == "0" {
		return daemon.Response{}, false
	}
	if !daemon.Supported() || !daemonProxyCommands[cmd] {
		return daemon.Response{}, false
	}
	root, ok := daemonProjectRoot()
	if !ok {
		return daemon.Response{}, false
	}
	identity := daemon.BinaryIdentity(version)
	argv := append([]string{cmd}, rest...)
	req := daemon.Request{Op: daemon.OpRun, Identity: identity, Argv: argv}

	if err := daemon.Ensure(root, version); err != nil {
		return daemon.Response{}, false
	}
	resp, err := daemon.Call(root, req, daemonCallTimeout)
	if err != nil {
		return daemon.Response{}, false
	}
	if strings.HasPrefix(resp.Fatal, "identity-mismatch") {
		// The binary was rebuilt (make install) while a daemon was live.
		_ = daemon.Stop(root)
		if err := daemon.Ensure(root, version); err != nil {
			return daemon.Response{}, false
		}
		resp, err = daemon.Call(root, req, daemonCallTimeout)
		if err != nil {
			return daemon.Response{}, false
		}
	}
	if resp.Fatal != "" {
		return daemon.Response{}, false
	}
	return resp, true
}
