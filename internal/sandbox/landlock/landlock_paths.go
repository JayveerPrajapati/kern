// Package landlock implements the Linux Stage-1 filesystem read confinement
// for the kern sandbox: an allowlist-based confinement subsystem extracted
// from internal/sandbox (the package was crossing its LOC cap; extraction is
// the repo's prescribed remedy, not cap-raising). It is a leaf package —
// stdlib + golang.org/x/sys only, no kern-internal imports — so
// internal/sandbox may use it freely.
//
// Landlock is Linux's unprivileged, allowlist-based filesystem sandbox (ABI
// added in kernel 5.13). Unlike the macOS Seatbelt path — a deny-list on top
// of (allow default) — Landlock grants ONLY what is listed: a rule on a
// directory allows access to that directory's inode subtree as bound at
// rule-add time, and everything else is denied for the handled rights.
//
// The allowlist builder is platform-neutral ON PURPOSE: it must be testable
// on macOS dev machines (mirroring the seatbeltProfileFor pure-function
// convention). The Linux-only apply path lives in landlock_linux.go; the
// non-Linux availability stub in landlock_other.go.
package landlock

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// fsAccess* mirror the kernel uapi access-right bits (stable ABI values,
// independent of any Go binding) so this file builds on every platform.
// Values are authoritative from include/uapi/linux/landlock.h.
const (
	fsAccessExecute    uint64 = 1 << 0
	FsAccessWriteFile  uint64 = 1 << 1
	fsAccessReadFile   uint64 = 1 << 2
	fsAccessReadDir    uint64 = 1 << 3
	fsAccessRemoveDir  uint64 = 1 << 4
	fsAccessRemoveFile uint64 = 1 << 5
	fsAccessMakeChar   uint64 = 1 << 6
	fsAccessMakeDir    uint64 = 1 << 7
	fsAccessMakeReg    uint64 = 1 << 8
	fsAccessMakeSock   uint64 = 1 << 9
	fsAccessMakeFifo   uint64 = 1 << 10
	fsAccessMakeBlock  uint64 = 1 << 11
	fsAccessMakeSym    uint64 = 1 << 12
	fsAccessRefer      uint64 = 1 << 13 // ABI v2 (kernel >= 5.19)
	fsAccessTruncate   uint64 = 1 << 14 // ABI v3 (kernel >= 6.2)
)

// fsAccessRead is the read tier: what a sandboxed build/test command needs to
// READ or EXECUTE anywhere (system dirs, toolchains, the non-sensitive parts
// of $HOME).
const fsAccessRead = fsAccessExecute | fsAccessReadFile | fsAccessReadDir

// FsAccessWrite is the full handled tier: read + the write/remove/make
// rights. Grants are still ABI-masked at apply time (REFER/TRUNCATE only
// exist on kernels >= 5.19/6.2).
const FsAccessWrite = fsAccessRead | FsAccessWriteFile | fsAccessRemoveDir |
	fsAccessRemoveFile | fsAccessMakeChar | fsAccessMakeDir | fsAccessMakeReg |
	fsAccessMakeSock | fsAccessMakeFifo | fsAccessMakeBlock | fsAccessMakeSym |
	fsAccessRefer | fsAccessTruncate

// AllowRule grants one Landlock access mask beneath one directory.
type AllowRule struct {
	Path   string
	Access uint64
}

// ChildSpecEnv carries the trampoline contract to the re-exec'd kern binary.
// It is present ONLY in the immediate sandbox child's environment and is
// stripped before the target command runs.
const ChildSpecEnv = "KERN_SANDBOX_CHILD_SPEC"

// ChildSpec is the JSON contract between the sandbox parent (runGuarded) and
// the trampoline init in landlock_linux.go (child): everything the child
// needs to rebuild the exact same isolation chain and exec the target.
type ChildSpec struct {
	Root   string   `json:"root"` // absolute workspace root
	Cmd    string   `json:"cmd"`
	Args   []string `json:"args"`
	Env    []string `json:"env"`              // final sanitized env; the marker var is stripped before exec
	Prefix []string `json:"prefix,omitempty"` // network-isolation argv prefix (unshare chain); empty = exec the target directly
}

// ChildSpecJSON encodes the trampoline contract. Kept in the neutral file so
// the sandbox package does not need the json import. prefix is the argv
// prefix of the network-isolation chain computed by the PARENT (the child
// cannot re-derive it: netIsolationPrefix lives in internal/sandbox, and this
// leaf package must not import it back).
func ChildSpecJSON(root, cmd string, args, env, prefix []string) (string, error) {
	b, err := json.Marshal(ChildSpec{Root: root, Cmd: cmd, Args: args, Env: env, Prefix: prefix})
	return string(b), err
}

// envValue returns the value of name in an environ slice ("KEY=value"), or "".
func envValue(env []string, name string) string {
	prefix := name + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return ""
}

// StripEnvVar removes every name=... entry from env (used to strip the
// trampoline marker before the target command sees it).
func StripEnvVar(env []string, name string) []string {
	prefix := name + "="
	out := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return out
}

// lookupPathDir resolves cmdName against an explicit PATH (from the child
// env, not the operator's) and returns the directory containing it, or ""
// when not found. Deterministic for tests.
func lookupPathDir(cmdName string, env []string) string {
	path := envValue(env, "PATH")
	if path == "" {
		path = os.Getenv("PATH")
	}
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, cmdName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return dir
		}
	}
	return ""
}

// homeSensitiveNames are the $HOME entries a sandboxed command must never be
// able to read (file-borne secrets). Mirrors sensitivePathDirs minus the
// nested entries (".config/gcloud", ".docker/config.json" — those are
// enumerated separately one level deep below) and the darwin-only
// "Library/Cookies". Keep in sync with sensitivePathDirs.
var homeSensitiveNames = map[string]bool{
	".ssh":   true,
	".aws":   true,
	".gnupg": true,
	".kube":  true,
	".netrc": true,
}

// LandlockAllowPaths builds the allow-rule set for a Linux sandboxed run.
//
// Landlock is allowlist-only, so the macOS deny-list intent is inverted:
// grant everything a build/test legitimately needs (system dirs, $HOME minus
// the sensitive entries, cache roots, the workspace) and never grant the
// sensitive paths. A rule binds the inode referenced at rule-add time, so a
// file/dir created inside $HOME after enumeration gets no grant (denied).
//
// writeConfined enables the write tier (workspace root, tmp, caches). When
// false only the read tier is enforced (pure parity with the macOS Seatbelt
// deny-list scope).
func LandlockAllowPaths(root, cmdName string, env []string, writeConfined bool) ([]AllowRule, error) {
	var rules []AllowRule
	add := func(p string, access uint64) {
		if p == "" {
			return
		}
		for i := range rules {
			if rules[i].Path == p {
				rules[i].Access |= access
				return
			}
		}
		rules = append(rules, AllowRule{Path: p, Access: access})
	}

	// Read tier: system directories a toolchain needs.
	for _, p := range []string{"/usr", "/usr/local", "/lib", "/lib64", "/bin", "/sbin", "/opt", "/etc", "/proc", "/sys", "/dev"} {
		add(p, fsAccessRead)
	}
	// The workspace root is always readable (it is the project being operated
	// on); the write tier below upgrades it to full access.
	add(root, fsAccessRead)
	// The target binary's directory (custom toolchains in ~/.cargo/bin etc.)
	// and the unshare binary's directory (normally covered; explicit for
	// custom PATHs).
	if dir := lookupPathDir(cmdName, env); dir != "" {
		add(dir, fsAccessRead)
	}
	if dir := lookupPathDir("unshare", env); dir != "" {
		add(dir, fsAccessRead)
	}

	// $HOME one level deep, minus the sensitive entries. A rule on $HOME
	// itself would grant everything beneath it, so each entry is enumerated.
	home := envValue(env, "HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	if home != "" {
		entries, err := os.ReadDir(home)
		if err != nil {
			// Unreadable directory: DAC already blocks same-UID reads; grant
			// it wholesale so exotic permission layouts keep builds working.
			add(home, fsAccessRead)
		} else {
			for _, e := range entries {
				if homeSensitiveNames[e.Name()] {
					continue
				}
				add(filepath.Join(home, e.Name()), fsAccessRead)
			}
		}
		// ~/.config minus gcloud and ~/.docker minus config.json: a rule on
		// the parent would also grant the sensitive child, so enumerate
		// inside those two.
		enumerateMinus(home, ".config", []string{"gcloud"}, add)
		enumerateMinus(home, ".docker", []string{"config.json"}, add)
	}

	// Write tier.
	if writeConfined {
		add(root, FsAccessWrite) // upgrades the always-on read grant
		add(os.TempDir(), FsAccessWrite)
		add("/dev", FsAccessWrite) // /dev/null, /dev/shm redirects
		for _, v := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT", "npm_config_cache"} {
			if p := envValue(env, v); p != "" {
				add(p, FsAccessWrite)
			}
		}
	}
	return rules, nil
}

// enumerateMinus grants read access to each entry of home/subdir except the
// named ones. An unreadable directory is granted wholesale (DAC already
// blocks same-UID reads).
func enumerateMinus(home, sub string, minus []string, add func(string, uint64)) {
	dir := filepath.Join(home, sub)
	entries, err := os.ReadDir(dir)
	if err != nil {
		add(dir, fsAccessRead)
		return
	}
	for _, e := range entries {
		skip := false
		for _, m := range minus {
			if e.Name() == m {
				skip = true
				break
			}
		}
		if !skip {
			add(filepath.Join(dir, e.Name()), fsAccessRead)
		}
	}
}

// writeConfinementEnabled reports whether the write tier of the Landlock
// allowlist is active (KERN_SANDBOX_WRITE_CONFINEMENT, default ON). It can be
// demoted independently of KERN_SANDBOX_FS_CONFINEMENT so read vs write
// confinement can be bisected.
func writeConfinementEnabled() bool {
	switch strings.TrimSpace(os.Getenv("KERN_SANDBOX_WRITE_CONFINEMENT")) {
	case "0", "false", "FALSE", "False", "no", "NO", "No", "off", "OFF", "Off":
		return false
	}
	return true
}
