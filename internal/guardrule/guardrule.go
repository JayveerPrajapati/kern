// Package guardrule is the guard's classification engine: the single Go
// source of truth for the bash-arm risk verdicts that the plugin (TS) and
// the shell guard used to re-implement separately (P3 of the guard-steer
// campaign; extracted from internal/contextcli when the classifier outgrew
// the CLI family's ARCHITECTURE.md cap). It returns the same risk-axis
// verdict the plugin's bash arm applies: write/destructive/network/installer
// segments GATE (deny the whole command), simple content-free commands ALLOW
// raw, and everything else is GOVERNED (kern build) unless the read half
// (internal/guardread) claims the pipeline as a serve. Deny/hint messages
// become byte-parity by construction (one source).
package guardrule

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/execclassify"
	"github.com/JayveerPrajapati/kern/internal/guardpaths"
	"github.com/JayveerPrajapati/kern/internal/guardread"
	"github.com/JayveerPrajapati/kern/internal/inplaceedit"
)

// The verdict vocabulary lives in guardread — the read half needs it to build
// a serve plan — and these aliases keep guardrule's public surface (and every
// caller's compile-time identity) unchanged across the extraction.
type ClassifyVerdict = guardread.ClassifyVerdict

type ClassifyResult = guardread.ClassifyResult

const (
	VerdictAllow    = guardread.VerdictAllow
	VerdictGate     = guardread.VerdictGate
	VerdictServe    = guardread.VerdictServe
	VerdictGoverned = guardread.VerdictGoverned
)

// The gate-side name sets — the single Go source for the plugin/guard lists
// (P2). GATE_CMDS deny the whole command.
var gateCMDs = map[string]bool{
	"rm": true, "mv": true, "cp": true, "dd": true, "mkfs": true, "wipefs": true,
	"diskutil": true, "shred": true, "truncate": true, "chmod": true, "chown": true,
	"curl": true, "wget": true, "fetch": true,
}

var installerCMDs = map[string]bool{
	"npm": true, "pnpm": true, "yarn": true, "pip": true, "pip3": true, "go": true,
	"make": true, "apt": true, "apt-get": true, "brew": true, "pacman": true,
}

var installActions = map[string]bool{
	"install": true, "add": true, "ci": true, "upgrade": true, "get": true,
}

// allSegmentsSimple reports whether a multi-segment command (joined by
// &&/||/;/|/newline) consists ONLY of simple content-free segments —
// IsSimpleCommand whitelists trivial shell, read-only git and kern
// diagnostics only, so such a compound has no write/network surface and
// runs raw like a single simple command instead of paying the governed
// wrapper (`ls a ; ls b`, `git log --oneline && git status`).
func allSegmentsSimple(cmd string) bool {
	segs := guardread.SplitStages(cmd)
	if len(segs) < 2 {
		return false
	}
	for _, seg := range segs {
		if !guardread.IsSimpleCommand(seg) {
			return false
		}
	}
	return true
}

// probeTokens are the argument forms that make a command a no-side-effect
// probe: version banners and help text (-version/-help: Java-style forms).
var probeTokens = map[string]bool{
	"version": true, "--version": true, "-version": true, "-v": true, "-V": true,
	"--help": true, "-h": true, "-help": true, "help": true,
}

// probeSegment reports whether one command segment is a version/help probe:
// the head plus only probe tokens (`go version`, `node --version`), or a
// short subcommand path ending in a help flag (`./kern guard --help`). Probes
// have no write/network/build surface, so they run raw instead of paying the
// governed round-trip. The caller must check gateHit first: a gate-class
// head stays gated even in probe form (`curl --help`).
func probeSegment(seg string) bool {
	if guardread.UnquotedMetachar(seg) != "" {
		return false
	}
	parts := strings.Fields(strings.TrimSpace(seg))
	if len(parts) < 2 {
		return false
	}
	args := parts[1:]
	// Form 1: every argument is a probe token.
	all := true
	for _, a := range args {
		if !probeTokens[a] {
			all = false
			break
		}
	}
	if all {
		return true
	}
	// Form 2: at most 3 plain subcommand tokens, then a trailing help flag.
	// Any flag in the middle (-m 'msg' --help) rejects: only known-safe
	// subcommand paths qualify.
	last := args[len(args)-1]
	if last != "--help" && last != "-h" {
		return false
	}
	mid := args[:len(args)-1]
	if len(mid) == 0 || len(mid) > 3 {
		return false
	}
	for _, m := range mid {
		if strings.HasPrefix(m, "-") {
			return false
		}
	}
	return true
}

// allSegmentsProbe reports whether every stage of a compound is a probe or a
// content-free simple command — e.g. `git log --oneline && go version`. No
// segment has a risky surface, so the compound runs raw.
func allSegmentsProbe(cmd string) bool {
	for _, seg := range guardread.SplitStages(cmd) {
		if guardread.IsSimpleCommand(seg) || probeSegment(seg) {
			continue
		}
		return false
	}
	return true
}

// FirstGateSegment returns the first compound segment that trips the gate
// classifier ("" when none does). Denial hints use it to name the offending
// segment of a compound so the agent sees WHICH segment gated, instead of a
// whole-command denial.
func FirstGateSegment(cmd string) string {
	for _, seg := range guardread.SplitStages(cmd) {
		if gateSegmentHit(seg, 0) {
			return seg
		}
	}
	return ""
}

// FirstUnsafeWriteSegment returns the first segment whose path-write targets
// failed the safe check ("" when none does) — the offending-segment name for
// the unsafe-write gate arm.
func FirstUnsafeWriteSegment(cmd, root string, extraRoots []string) string {
	for _, seg := range guardread.SplitStages(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if guardread.IsSimpleCommand(seg) {
			continue
		}
		if guardpaths.WriteSegment(seg) && !guardpaths.SafeWriteSegment(seg, root, extraRoots) {
			return seg
		}
	}
	return ""
}

// gateSegmentHit mirrors the plugin's gateSegmentHit: 0 iff the segment is a
// write/destructive/network/installer.
func gateSegmentHit(rawSeg string, depth int) bool {
	// A real write/append redirect makes the segment a gate; discarding
	// redirects (>/dev/null, 2>&1) and quoted '>' are not writes.
	if guardread.HasWriteRedirect(rawSeg) {
		return true
	}
	toks := execclassify.StripPrefixes(execclassify.ShellTokens(rawSeg))
	if len(toks) == 0 {
		return false
	}
	name := toks[0]
	rest := toks[1:]
	// Destructive/pipe-install classes are shared with the exec firewall, so
	// find -delete, xargs rm, tee, git clean/reset and forced kills gate here
	// exactly as they do in kern_exec. The path-aware in-tree write arm runs
	// BEFORE gateHit, so this only sees commands that already failed it.
	if cls, _ := execclassify.ClassifyDangerousCommand(rawSeg); cls == execclassify.DangerDestructive || cls == execclassify.DangerPipeInstall {
		return true
	}
	if depth < 2 && len(toks) > 2 && strings.HasPrefix(toks[1], "-c") && guardread.ShellWrapperCMDs[name] {
		inner := strings.Join(toks[2:], " ")
		if inner != "" && gateHit(inner, depth+1) {
			return true
		}
	}
	if gateCMDs[name] {
		return true
	}
	if installerCMDs[name] && len(rest) > 0 {
		if name == "pacman" {
			for _, t := range rest {
				if strings.HasPrefix(t, "-S") || t == "--sync" || t == "--upgrade" {
					return true
				}
			}
		} else if name == "go" || name == "make" {
			if rest[0] == "install" {
				return true
			}
		} else if installActions[rest[0]] {
			return true
		}
	}
	if name == "git" {
		// Any push publishes to a remote (network + irreversible history):
		// the operator exempted git reads/add/commit, not push. A dry run
		// writes nothing and stays out of the gate.
		push, dry := false, false
		for _, t := range rest {
			if t == "push" {
				push = true
			}
			if t == "--dry-run" || t == "-n" {
				dry = true
			}
		}
		if push && !dry {
			return true
		}
	}
	return false
}

// gateHit reports whether ANY &&/||/;/|/newline-separated segment is a gate.
func gateHit(cmd string, depth int) bool {
	// Whole-pipeline classification: a dangerous pipeline whose individual
	// segments look benign in isolation (printf | base64 -d | sh) must gate.
	if cls, _ := execclassify.ClassifyDangerousCommand(cmd); cls == execclassify.DangerDestructive || cls == execclassify.DangerPipeInstall {
		return true
	}
	for _, seg := range guardread.SplitStages(cmd) {
		if gateSegmentHit(seg, depth) {
			return true
		}
	}
	return false
}

// ClassifyCommand classifies one command into the risk-axis verdict.
// Root-less: no path-awareness (flat GATE for write commands) — kept for
// callers and tests that classify command text alone. The guard surfaces
// (Go hook, kern guard classify CLI, and through it the script and the
// opencode plugin) call ClassifyCommandInRoot with the real project root.
func ClassifyCommand(cmd string) ClassifyResult {
	return ClassifyCommandInRoot(cmd, "")
}

// ClassifyCommandInRoot classifies with a project root, enabling the
// path-aware write arm: local file-shape writes (rm/mv/cp/mkdir/touch,
// sed -i) whose every path argument resolves INSIDE the root (or the temp
// dir), away from .git internals and credential-shaped files, are allowed —
// the same intent the hosts' native edit tools already exercise ungated, with
// commit-time verification as the backstop. Destructive-class commands are
// NEVER path-aware-allowed (a recursive forced delete gates even in-tree);
// everything else (out-of-root targets, .git, credentials, recursive rm of
// top-level entries, disk and network commands) keeps the flat GATE verdict.
func ClassifyCommandInRoot(cmd, root string) ClassifyResult {
	return ClassifyCommandInRoots(cmd, root, nil)
}

// ClassifyCommandInRoots additionally accepts extra workspace roots
// (the operator's KERN_MCP_ROOTS list): absolute write targets under a
// second configured root classify as in-tree instead of falling to the
// flat out-of-root GATE. Relative paths stay bound to the primary root,
// and no target may resolve AT a root (rm -rf on a workspace root still
// gates).
func ClassifyCommandInRoots(cmd, root string, extraRoots []string) ClassifyResult {
	if strings.TrimSpace(cmd) == "" {
		return ClassifyResult{Verdict: VerdictGoverned}
	}
	sum := sha256.Sum256([]byte(cmd))
	hash := hex.EncodeToString(sum[:])
	// The risk classifier reasons over the command with output-discarding
	// redirects (>/dev/null, 2>&1) removed: they write nothing. The
	// allow-once hash stays bound to the exact original text.
	eff := guardread.StripNullRedirects(cmd)
	if eff == "" {
		eff = cmd
	}
	if guardread.IsSimpleCommand(eff) {
		return ClassifyResult{Verdict: VerdictAllow, Hash: hash,
			Hint: "content-free command — runs raw"}
	}
	if allSegmentsSimple(eff) {
		return ClassifyResult{Verdict: VerdictAllow, Hash: hash,
			Hint: "compound of content-free commands — runs raw"}
	}
	// In-place edits of CODE files are refactors, not scratch writes: gate
	// them so the change goes through the edit tool / kern_apply (journaled
	// and verifiable) instead of an opaque rewrite. Non-code files, temp
	// scratch and out-of-root targets keep their existing write policy.
	if root != "" && anyInPlaceCodeEdit(eff, root) {
		return ClassifyResult{Verdict: VerdictGate, Hash: hash,
			Hint: "GATE: in-place edit of a code file (sed -i / perl -pi) — use the edit tool or kern_apply"}
	}
	// Destructive commands only ride the path-aware arm when every target is
	// scratch space: `rm -rf internal/x` gates like `rm -rf .git`, while
	// /tmp scratch deletes stay raw (shared destructive class with exec).
	if root != "" && allSegmentsSafeWrites(eff, root, extraRoots) && guardpaths.DestructiveAllowed(eff) {
		return ClassifyResult{Verdict: VerdictAllow, Hash: hash, PathAware: true,
			Hint: "path-aware write — every target in-tree (or tmp), .git/credentials/disk/network excluded; audited"}
	}
	if root != "" && hasUnsafePathWrite(eff, root, extraRoots) {
		return ClassifyResult{Verdict: VerdictGate, Hash: hash,
			Hint: "GATE write — target outside the project root/temp, or .git/credential/recursive-top-level; triggering segment: " + FirstUnsafeWriteSegment(eff, root, extraRoots)}
	}
	if gateHit(eff, 0) {
		return ClassifyResult{Verdict: VerdictGate, Hash: hash,
			Hint: "GATE command (write/destructive/network/installer) — operator approval required; triggering segment: " + FirstGateSegment(eff)}
	}
	// Version/help probes: placed after gateHit so gate-class heads stay
	// gated (`curl --help`), and before the serve arms — a probe is a raw
	// run, cheaper than a served read.
	if probeSegment(eff) {
		return ClassifyResult{Verdict: VerdictAllow, Hash: hash,
			Hint: "version/help probe — runs raw"}
	}
	if allSegmentsProbe(eff) && len(guardread.SplitStages(eff)) > 1 {
		return ClassifyResult{Verdict: VerdictAllow, Hash: hash,
			Hint: "compound of probes and content-free commands — runs raw"}
	}
	if guardread.IsGitShowDiffRead(eff) {
		return ClassifyResult{Verdict: VerdictServe, Tier: 3, Hash: hash,
			Hint: "git history read — served from the readonly sandbox"}
	}
	if guardread.ReadOnlyPipeline(eff) {
		res := guardread.ReadPipeline(eff)
		res.Hash = hash
		res.Hint = "pure-read pipeline — served from the kern index / readonly sandbox"
		return res
	}
	return ClassifyResult{Verdict: VerdictGoverned, Hash: hash,
		Hint: "governed command — runs through kern build"}
}

// anyInPlaceCodeEdit reports whether any segment edits a code file in place
// (sed -i, perl/ruby -i) inside the root.
func anyInPlaceCodeEdit(cmd, root string) bool {
	for _, seg := range guardread.SplitStages(cmd) {
		if inplaceedit.TargetsCodeFile(strings.TrimSpace(seg), root) {
			return true
		}
	}
	return false
}

// allSegmentsSafeWrites reports whether every stage of the command is
// either content-free-simple or a path-aware in-tree write command
// (internal/guardpaths owns the per-segment path math).
func allSegmentsSafeWrites(cmd, root string, extraRoots []string) bool {
	segs := guardread.SplitStages(cmd)
	if len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		if guardread.IsSimpleCommand(seg) {
			continue
		}
		if !guardpaths.SafeWriteSegment(seg, root, extraRoots) {
			return false
		}
	}
	return true
}

// hasUnsafePathWrite reports whether any segment is a path-write command
// (rm/mv/cp/mkdir/touch, or sed in-place) whose paths FAILED the safe
// check: the command's whole point is a write to somewhere disallowed.
// It gates even though the bare command name is not in gateCMDs — a
// governed verdict would let the write run on hosts that execute
// governed commands.
func hasUnsafePathWrite(cmd, root string, extraRoots []string) bool {
	for _, seg := range guardread.SplitStages(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if guardread.IsSimpleCommand(seg) {
			continue
		}
		if guardpaths.WriteSegment(seg) && !guardpaths.SafeWriteSegment(seg, root, extraRoots) {
			return true
		}
	}
	return false
}
