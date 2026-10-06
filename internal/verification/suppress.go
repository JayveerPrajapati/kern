package verification

// This file implements the security-finding triage layer: a reasoned
// suppression list that distinguishes known/accepted findings from real
// problems. It is consulted by VerifySecurity — a finding that matches a
// suppression is still reported (marked [suppressed] with its reason), but
// it no longer blocks the check and is excluded from the risk ladder.

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/secscan"
)

// Suppression triages one security finding as known/accepted. A finding
// matches when EITHER key matches:
//
//	file:line:rule  — the entry pins Line > 0 (line-specific)
//	file:rule       — the entry omits Line (Line == 0, file-wide)
//
// Reason is MANDATORY: it must be a concrete sentence explaining why the
// finding is accepted (a false positive, a test fixture, an intentional
// low-level syscall wrapper, ...). Suppressed findings stay visible in the
// report — marked [suppressed] with their reason — so the triage never hides
// a real problem in either direction.
//
// JSON schema for the optional user file .kern/verify-suppressions.json
// (same shape, merged additively over the compiled-in defaults):
//
//	{
//	  "suppressions": [
//	    {"file": "internal/bpcli/cli/check.go", "line": 625,
//	     "rule": "hardcoded-secret",
//	     "reason": "well-known git empty-tree object hash; a public constant, not a credential."},
//	    {"file": "sdk/python/kern_sdk/engine.py",
//	     "rule": "py-subprocess",
//	     "reason": "the SDK's own execution API; every call passes shell=False."}
//	  ]
//	}
//
// Line is optional; omit it (or set 0) for a file-wide suppression that
// matches every finding of Rule in File. File paths are repo-root-relative
// with forward slashes, exactly as the scanner reports them. A malformed
// file is logged and treated as absent — a broken triage file must never
// crash or fail a verification run.
type Suppression struct {
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"` // 0 = file-wide (matches any line of File)
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

// SuppressionRegistry is the merged, additive set of built-in and user
// suppressions consulted by VerifySecurity.
type SuppressionRegistry struct {
	Suppressions []Suppression `json:"suppressions"`
}

// Match reports whether f is suppressed, returning the suppression reason.
// A finding matches if any entry keys it by file:line:rule (line-specific)
// OR by file:rule (file-wide).
func (r *SuppressionRegistry) Match(f secscan.Finding) (reason string, ok bool) {
	for _, s := range r.Suppressions {
		if s.File == f.File && s.Rule == f.Rule && (s.Line == 0 || s.Line == f.Line) {
			return s.Reason, true
		}
	}
	return "", false
}

// defaultSuppressions are the findings triaged and accepted on kern's own
// repository. Each entry was verified against the CURRENT scanner output at
// implementation time (TestBuiltinSuppressionsMatchCurrentScan pins the set —
// it fails if the scanner stops producing any of them, so the defaults cannot
// silently drift). Entries are line-specific (Line > 0) where a single site is
// accepted and file-wide (Line == 0) where every current finding of the rule
// in the file is a false positive by construction: the regex unsafe-exec /
// unsafe-http rules flag non-literal (variable) arguments regardless of trust,
// and the suppressed sites all pass constants or trusted repo paths.
//
// Note: internal/bpcli/cli/check.go:625 hardcoded-secret (the git empty-tree
// object hash) was removed when it went stale — secscan's isWellKnownGitHash
// guard now classifies that constant as a well-known git object, so the
// scanner no longer produces the finding.
var defaultSuppressions = []Suppression{
	{
		File:   "internal/project/watch_inotify_linux.go",
		Line:   190,
		Rule:   "unsafe-reflection",
		Reason: "canonical inotify event-buffer decode: reinterpreting the kernel-packed InotifyEvent header in place (identical to fsnotify's inotify backend), bounds-checked against the bytes actually read in a goroutine-local buffer.",
	},
	{
		File:   "internal/sandbox/landlock/landlock_linux.go",
		Line:   127,
		Rule:   "unsafe-reflection",
		Reason: "documented raw-syscall ABI conversion: the kernel reads the local Landlock ruleset attribute through a pointer that lives only for the duration of the LANDLOCK_CREATE_RULESET call.",
	},
	{
		File:   "internal/sandbox/landlock/landlock_linux.go",
		Line:   149,
		Rule:   "unsafe-reflection",
		Reason: "documented raw-syscall ABI conversion: the kernel reads the local path-beneath attribute through a pointer that lives only for the duration of the LANDLOCK_ADD_RULE call.",
	},
	{
		File:   "sdk/python/kern_sdk/engine.py",
		Line:   58,
		Rule:   "py-subprocess",
		Reason: "the SDK's own execution API: runs the embedded kern binary via subprocess.Popen with an explicit shell=False and a fixed argv list — no shell interpolation.",
	},
	{
		File:   "sdk/python/kern_sdk/engine.py",
		Line:   78,
		Rule:   "py-subprocess",
		Reason: "the SDK's own execution API: runs the embedded kern binary via subprocess.Popen with an explicit shell=False and a fixed argv list — no shell interpolation.",
	},
	{
		File:   "sdk/python/kern_sdk/engine.py",
		Line:   97,
		Rule:   "py-subprocess",
		Reason: "the SDK's own execution API: runs the embedded kern binary via subprocess.Popen with an explicit shell=False and a fixed argv list — no shell interpolation.",
	},
	{
		File:   "sdk/python/kern_sdk/engine.py",
		Line:   118,
		Rule:   "py-subprocess",
		Reason: "the SDK's own execution API: runs the embedded kern binary via subprocess.Popen with an explicit shell=False and a fixed argv list — no shell interpolation.",
	},
	{
		File:   "cmd/kern/cmd_update.go",
		Rule:   "unsafe-exec",
		Reason: "kern update's one-shot installer legs: curl fetches install.sh/install.ps1 from a hardcoded const URL (installerScriptURL; the documented KERN_INSTALL_SCRIPT_URL override is config authorship) and sh/pwsh run it with a closed-set action string (upgrade|status) — fixed argv, no user-injectable input.",
	},
	{
		File:   "cmd/kern/helpers.go",
		Rule:   "unsafe-exec",
		Reason: "git helper wrappers: gitDiff/gitDiffC translate caller-passed literal arg strings (\"diff --cached\", \"diff HEAD\", \"rev-parse --short HEAD\") into git -C <root> argv — the only variables are the trusted repo root and literal args, no shell.",
	},
	{
		File:   "internal/app/surface_drift.go",
		Rule:   "unsafe-exec",
		Reason: "SurfaceTouchesFromGit's git log --name-status parity probe: fixed argv with the trusted repo root and a capped integer commit count as the only variables — no shell, no user-controlled command string.",
	},
	{
		File:   "internal/blueprint/adapters/kern/architecture.go",
		Rule:   "unsafe-exec",
		Reason: "git worktree list / git ls-files helpers: fixed argv where the only variable is the evaluated repo root path passed via git -C — no shell, no user-injectable argument.",
	},
	{
		File:   "internal/blueprint/adapters/kern/client.go",
		Rule:   "unsafe-exec",
		Reason: "the KernClient's own subprocess primitive: go install of a const module path (version.KernModulePath) and the commandRunner used with constant argv (kern guard/sec/...); argv is passed directly, never through a shell.",
	},
	{
		File:   "internal/sandbox/landlock/landlock_linux.go",
		Line:   196,
		Rule:   "unsafe-exec",
		Reason: "LandlockAvailable's self-confinement probe: re-execs the current binary (exe from os.Executable(), fixed at process start — never user input) with a trivial spec under a 3s timeout to exercise Landlock apply + the unshare chain end-to-end.",
	},
}

// loadSuppressionRegistry merges the compiled-in defaults with the optional
// user file <root>/.kern/verify-suppressions.json (same schema, additive).
// A malformed user file is logged and treated as absent — a broken triage
// file must never crash or fail a verification run.
func loadSuppressionRegistry(root string) *SuppressionRegistry {
	reg := &SuppressionRegistry{Suppressions: append([]Suppression(nil), defaultSuppressions...)}
	path := filepath.Join(root, ".kern", "verify-suppressions.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return reg // absent — built-ins only
	}
	var user SuppressionRegistry
	if err := json.Unmarshal(data, &user); err != nil {
		log.Printf("verification: ignoring malformed %s: %v", path, err)
		return reg
	}
	reg.Suppressions = append(reg.Suppressions, user.Suppressions...)
	return reg
}
