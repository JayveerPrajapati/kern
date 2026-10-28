package verification

// This file implements the security-finding triage layer: a reasoned
// suppression list that distinguishes known/accepted findings from real
// problems. It is consulted by VerifySecurity — a finding that matches a
// suppression is still reported (marked [suppressed] with its reason), but
// it no longer blocks the check and is excluded from the risk ladder.

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/secscan"
)

// Suppression triages one security finding as known/accepted. A finding
// matches when ANY key matches:
//
//	file:line:rule  — the entry pins Line > 0 (line-specific)
//	file:rule       — the entry omits Line (Line == 0, file-wide)
//	file:func:rule  — the entry pins Func (func-scoped; the finding's line
//	                  resolves to that enclosing Go function, so an edit
//	                  above the site no longer un-suppresses the class)
//
// Func wins over Line when both are set.
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
//	    {"file": "internal/config/config.go", "line": 42,
//	     "rule": "hardcoded-secret",
//	     "reason": "documented test-only token, not a credential."},
//	    {"file": "internal/execution/worktree.go", "func": "Diff",
//	     "rule": "unsafe-exec",
//	     "reason": "fixed-argv git plumbing; func-scoped so line edits don't un-suppress."},
//	    {"file": "sdk/python/kern_sdk/engine.py",
//	     "rule": "py-subprocess",
//	     "reason": "the SDK's own execution API; every call passes shell=False."}
//	  ]
//	}
//
// Line is optional; omit it (or set 0) for a file-wide suppression that
// matches every finding of Rule in File. Func is optional too and preferred
// over Line when set: the finding's line is resolved to its enclosing Go
// function, so the entry survives line drift (only .go files resolve; a
// Func on a non-Go file never matches). Label is optional as well; when set,
// only findings carrying that pii label (e.g. "EMAIL") match, so a
// file-wide suppression of a false-positive class never covers a real
// secret of a different class in the same file. File paths are
// repo-root-relative with forward slashes, exactly as the scanner reports
// them. A malformed file is logged and treated as absent — a broken triage
// file must never crash or fail a verification run.
type Suppression struct {
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"` // 0 = file-wide (matches any line of File)
	Func   string `json:"func,omitempty"` // optional: enclosing Go func name (preferred over Line when set)
	Rule   string `json:"rule"`
	Label  string `json:"label,omitempty"` // optional: narrow the rule to one pii label (e.g. "EMAIL")
	Reason string `json:"reason"`
}

// SuppressionRegistry is the merged, additive set of built-in and user
// suppressions consulted by VerifySecurity. resolver lazily maps a
// file:line to its enclosing Go function (go/parser) so Func-keyed entries
// survive line drift; nil when the registry is used without a root.
type SuppressionRegistry struct {
	Suppressions []Suppression `json:"suppressions"`
	resolver     *funcResolver
}

// Match reports whether f is suppressed, returning the suppression reason.
// A finding matches if any entry keys it by file:line:rule (line-specific),
// by file:rule (file-wide), or by file:func:rule (func-scoped — resolved
// lazily via the registry's funcResolver, surviving line drift within the
// named function). An entry may additionally narrow to one pii Label (e.g.
// "EMAIL"); when set, only findings carrying that label match — so a
// file-wide suppression of a false-positive class never covers a real
// secret of a different class in the same file.
func (r *SuppressionRegistry) Match(f secscan.Finding) (reason string, ok bool) {
	for _, s := range r.Suppressions {
		if s.File != f.File || s.Rule != f.Rule {
			continue
		}
		if s.Label != "" && s.Label != f.Label {
			continue
		}
		if s.Func != "" {
			if r.resolver == nil {
				continue
			}
			if fn := r.resolver.funcFor(f.File, f.Line); fn != s.Func {
				continue
			}
			return s.Reason, true
		}
		if s.Line == 0 || s.Line == f.Line {
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
// Note: the internal/bpcli/cli/check.go:625 hardcoded-secret suppression
// (the git empty-tree object hash the diff gate used to hardcode before the
// blueprint family was removed) was deleted when it went stale — secscan's
// isWellKnownGitHash guard (internal/secscan/sec.go) now classifies that
// constant as a well-known git object, so the scanner no longer produces
// the finding.
var defaultSuppressions = []Suppression{
	{
		File:   "cmd/kern/cmd_bench.go",
		Rule:   "unsafe-exec",
		Reason: "benchmark harness: re-executes the kern binary (exe from os.Executable()) with the fixed argument vectors declared in the harness's own query table plus the operator's --root — argv only, no shell, no user-controlled command string.",
	},
	{
		File:   ".github/workflows/ci.yml",
		Rule:   "hardcoded-secret",
		Label:  "EMAIL",
		Reason: "false positive: the EMAIL pattern matches pinned Go module version selectors (go install ...@vX.Y.Z / go run ...@X.Y.Z) in ci.yml, not an address or credential. Label-scoped to EMAIL so a REAL secret (GITHUB token, API key, ...) in any workflow file is never suppressed; file-wide within the label because the selector lines shift whenever the workflow env block changes — a line pin would un-suppress the same class on every workflow edit.",
	},
	{
		File:   "internal/langparse/compiler/KernJavaBindings.java",
		Line:   52,
		Rule:   "hardcoded-secret",
		Reason: "false positive: the IPV6 pattern matches the Java method-reference double-colon operator, not a network address or credential.",
	},
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
		File:   "internal/sandbox/landlock/landlock_linux.go",
		Line:   196,
		Rule:   "unsafe-exec",
		Reason: "LandlockAvailable's self-confinement probe: re-execs the current binary (exe from os.Executable(), fixed at process start — never user input) with a trivial spec under a 3s timeout to exercise Landlock apply + the unshare chain end-to-end.",
	},
	{
		File:   "evaluate/bench/main.go",
		Line:   503,
		Rule:   "unsafe-http",
		Reason: "benchmark fixture code, not a real HTTP flow: the Client and its variable base URL live inside the fixtureMicroservice raw-string const (evaluate/bench harness corpus) — synthetic sample code the scanner matches as file bytes, never compiled or executed.",
	},
	{
		File:   "internal/calibrate/calibrate.go",
		Line:   57,
		Rule:   "unsafe-exec",
		Reason: "calibration harness's git rev-list probe: fixed argv with the operator-supplied repo root (a git checkout path from a CLI flag/arg) and a capped integer commit count as the only variables — no shell, no user-controlled command string.",
	},
	{
		File:   "internal/commitmsg/commitmsgcmd.go",
		Line:   225,
		Rule:   "unsafe-exec",
		Reason: "kern commit's git wrappers (gitDiff/gitOutput/gitCommit/shortHash): fixed literal argv (diff --cached, add -A, commit -F -...) with no shell interpolation — the only inputs are the operator's own repo and the generated message fed over stdin; the operator CLI is the trusted principal (same class as cmd/kern/helpers.go:571).",
	},

	{
		File:   "internal/mcp/server.go",
		Func:   "commit",
		Rule:   "unsafe-exec",
		Reason: "fixed-argv git plumbing: the server resolves the short HEAD with literal args (git -C <root> rev-parse --short HEAD); the only variable is the trusted project root, with no shell and no user-controlled command string.",
	},
	{
		File:   "internal/setup/setup_claude.go",
		Func:   "wireClaude",
		Rule:   "unsafe-exec",
		Reason: "`claude mcp add kern -- <absolute kern-mcp path>` is a fixed literal argv (mcp/add/kern/--); path comes from exec.LookPath(\"claude\") and bin is the install-resolved absolute binary — no shell, no user-controlled command string. Func-scoped so line drift does not un-suppress the class.",
	},

	// 2026-10-21 dogfood triage: the remaining 67 unsafe-exec / unsafe-http
	// findings in kern's own tree, triaged class by class (fixed-argv git
	// plumbing, scanner probes, operator-configured LLM/SDK/webhook
	// endpoints, and the governed sandbox/exec machinery itself). Line pins
	// are intentional: an edit that moves a line un-suppresses its finding
	// and forces re-triage.
	{
		File:   "internal/context/git.go",
		Line:   19,
		Rule:   "unsafe-exec",
		Reason: "fixed-argv git plumbing: the only variables are the trusted repo root and internally derived paths/commit ranges — no shell, no user-controlled command string.",
	},
	{
		File:   "internal/deployment/deployment.go",
		Line:   111,
		Rule:   "unsafe-exec",
		Reason: "ShellDeployer runs the operator-authored KERN_DEPLOY_COMMAND (env/config), fail-closed behind KERN_ALLOW_DEPLOY=1 and the TaskService governance firewall (approval-gated, ErrApprovalRequired on CRITICAL/HIGH); NoopDeployer skips the gate — audited in the 2026-10-05 exec-firewall pass.",
	},
	{
		File:   "internal/diffgate/diffgate.go",
		Func:   "Run",
		Rule:   "unsafe-exec",
		Reason: "gofmt -l over internally collected file paths — fixed argv, no shell.",
	},
	{
		File:   "internal/doctor/doctor.go",
		Func:   "checkExec",
		Rule:   "unsafe-exec",
		Reason: "doctor exec probe: runs a resolved binary path with no arguments to test availability (deadline overridable via KERN_DOCTOR_EXEC_TIMEOUT_MS).",
	},
	{
		File:   "internal/doctor/doctor.go",
		Func:   "gitHead",
		Rule:   "unsafe-exec",
		Reason: "fixed-argv git plumbing: the only variables are the trusted repo root and internally derived paths/commit ranges — no shell, no user-controlled command string.",
	},
	{
		File:   "internal/doctor/global.go",
		Func:   "checkGlobalHealth",
		Rule:   "unsafe-exec",
		Reason: "fixed-argv git plumbing: the only variables are the trusted repo root and internally derived paths/commit ranges — no shell, no user-controlled command string.",
	},
	{
		File:   "internal/execution/worktree.go",
		Func:   "Diff",
		Rule:   "unsafe-exec",
		Reason: "fixed-argv git diff (both the worktree diff and the --no-index repo-vs-worktree form) against the execution worktree (the operator's isolated copy) — trusted paths, no shell.",
	},
	{
		File:   "internal/fetch/fetch.go",
		Line:   89,
		Rule:   "unsafe-http",
		Reason: "kern fetch / kern_doc action=fetch: the operator-named URL is the product's documented only-network fetch — not an injection sink.",
	},
	{
		File:   "internal/hook/hooks.go",
		Line:   100,
		Rule:   "unsafe-exec",
		Reason: "fixed-argv git plumbing: the only variables are the trusted repo root and internally derived paths/commit ranges — no shell, no user-controlled command string.",
	},
	{
		File:   "internal/hook/hooks.go",
		Line:   131,
		Rule:   "unsafe-exec",
		Reason: "git diff with an internally derived commit range (diffgate from..to hashes) — no user-controlled command string.",
	},
	{
		File:   "internal/index/identity.go",
		Rule:   "unsafe-exec",
		Reason: "index identity: git plumbing (add -A with a constant pathspec, write-tree, and the runGit helper) called with fixed literal subcommands — the trusted repo root is the only variable, no shell. File-wide so edits in the identity code cannot un-suppress the class.",
	},
	{
		File:   "internal/intel/churn.go",
		Line:   59,
		Rule:   "unsafe-exec",
		Reason: "intel churn: git argv assembled internally (rev-list/log with literal subcommands and internally derived ranges) — no user-controlled command string.",
	},
	{
		File:   "internal/intel/cochange.go",
		Line:   61,
		Rule:   "unsafe-exec",
		Reason: "intel co-change: git argv assembled internally — same class as churn.go.",
	},
	{
		File:   "internal/intel/intel.go",
		Line:   29,
		Rule:   "unsafe-exec",
		Reason: "fixed-argv git plumbing: the only variables are the trusted repo root and internally derived paths/commit ranges — no shell, no user-controlled command string.",
	},
	{
		File:   "internal/intel/intel.go",
		Line:   32,
		Rule:   "unsafe-exec",
		Reason: "fixed-argv git plumbing: the only variables are the trusted repo root and internally derived paths/commit ranges — no shell, no user-controlled command string.",
	},
	{
		File:   "internal/intel/intel.go",
		Line:   53,
		Rule:   "unsafe-exec",
		Reason: "git diff --name-only with an internally derived commit range — no user-controlled command string.",
	},
	{
		File:   "internal/langparse/java_compiler_bindings.go",
		Line:   77,
		Rule:   "unsafe-exec",
		Reason: "Java parser helper: javac over kern's own generated helper source with kern-computed manifest/classpath paths — internal build plumbing.",
	},
	{
		File:   "internal/llm/anthropic_provider.go",
		Line:   65,
		Rule:   "unsafe-http",
		Reason: "LLM client to the operator-configured provider endpoint (BaseURL/Base from env/config) — the endpoint IS configuration by design, not an injection sink.",
	},
	{
		File:   "internal/llm/google_provider.go",
		Line:   70,
		Rule:   "unsafe-http",
		Reason: "LLM client to the operator-configured provider endpoint (BaseURL/Base from env/config) — the endpoint IS configuration by design, not an injection sink.",
	},
	{
		File:   "internal/llm/llm.go",
		Line:   91,
		Rule:   "unsafe-http",
		Reason: "LLM client to the operator-configured provider endpoint (BaseURL/Base from env/config) — the endpoint IS configuration by design, not an injection sink.",
	},
	{
		File:   "internal/llm/llm.go",
		Line:   125,
		Rule:   "unsafe-http",
		Reason: "LLM client to the operator-configured provider endpoint (BaseURL/Base from env/config) — the endpoint IS configuration by design, not an injection sink.",
	},
	{
		File:   "internal/llm/llm.go",
		Line:   201,
		Rule:   "unsafe-http",
		Reason: "LLM client to the operator-configured provider endpoint (BaseURL/Base from env/config) — the endpoint IS configuration by design, not an injection sink.",
	},
	{
		File:   "internal/llm/llm.go",
		Line:   242,
		Rule:   "unsafe-http",
		Reason: "LLM client to the operator-configured provider endpoint (BaseURL/Base from env/config) — the endpoint IS configuration by design, not an injection sink.",
	},
	{
		File:   "internal/llm/localcli.go",
		Line:   92,
		Rule:   "unsafe-exec",
		Reason: "local CLI LLM provider: argv from the operator-configured provider command — operator config authorship.",
	},
	{
		File:   "internal/llm/ollama_provider.go",
		Line:   104,
		Rule:   "unsafe-http",
		Reason: "LLM client to the operator-configured provider endpoint (BaseURL/Base from env/config) — the endpoint IS configuration by design, not an injection sink.",
	},
	{
		File:   "internal/llm/ollama_provider.go",
		Line:   154,
		Rule:   "unsafe-http",
		Reason: "LLM client to the operator-configured provider endpoint (BaseURL/Base from env/config) — the endpoint IS configuration by design, not an injection sink.",
	},
	{
		File:   "internal/llm/openai_provider.go",
		Line:   175,
		Rule:   "unsafe-http",
		Reason: "LLM client to the operator-configured provider endpoint (BaseURL/Base from env/config) — the endpoint IS configuration by design, not an injection sink.",
	},
	{
		File:   "internal/llm/openai_provider.go",
		Line:   239,
		Rule:   "unsafe-http",
		Reason: "LLM client to the operator-configured provider endpoint (BaseURL/Base from env/config) — the endpoint IS configuration by design, not an injection sink.",
	},
	{
		File:   "internal/lspbridge/bridge.go",
		Line:   293,
		Rule:   "unsafe-exec",
		Reason: "launches the configured LSP server command — operator config authorship, no user-injectable input.",
	},
	{
		File:   "internal/mcp/provenance/provenance.go",
		Line:   291,
		Rule:   "unsafe-exec",
		Reason: "fixed-argv git plumbing: the only variables are the trusted repo root and internally derived paths/commit ranges — no shell, no user-controlled command string.",
	},
	{
		File:   "internal/mcp/sampling.go",
		Line:   201,
		Rule:   "unsafe-exec",
		Reason: "runHostSamplerCommand: the sampler command is operator configuration, executed behind governance.CheckExecCommand at the MCP boundary (2026-10-05 exec-firewall audit).",
	},
	{
		File:   "internal/mcpclient/client.go",
		Line:   146,
		Rule:   "unsafe-exec",
		Reason: "stdio MCP client launches the configured server command (s.Command/s.Args from the operator's MCP server config) — operator config authorship.",
	},
	{
		File:   "internal/mcpclient/client.go",
		Line:   297,
		Rule:   "unsafe-http",
		Reason: "MCP client JSON-RPC POST to the configured server URL — the transport's purpose.",
	},
	{
		File:   "internal/mutation/mutation.go",
		Line:   388,
		Rule:   "unsafe-exec",
		Reason: "compile/test step with internally assembled argv; the MCP boundary applies governance.CheckExecCommand + minimalExecEnv (2026-10-05 exec-firewall fix).",
	},
	{
		File:   "internal/optimize/optimize.go",
		Line:   528,
		Rule:   "unsafe-exec",
		Reason: "RunBuild IS the governed command wrapper: every invocation passes governance.CheckExecCommand (fail-closed without KERN_TOOLS/KERN_ALLOW_EXEC) and children run under the exec firewall — the command string is the operator/agent-authored build command by definition.",
	},
	{
		File:   "internal/project/fsevents.go",
		Line:   93,
		Rule:   "unsafe-exec",
		Reason: "file watcher launch: internally named binary with internally derived args — no user-controlled command string.",
	},
	{
		File:   "internal/prprovider/github.go",
		Line:   69,
		Rule:   "unsafe-http",
		Reason: "GitHub PR provider: posts to the operator-configured API endpoint with the operator's token — configuration by design.",
	},
	{
		File:   "internal/refactor/transaction.go",
		Line:   182,
		Rule:   "unsafe-exec",
		Reason: "compile step with internally assembled argv; the MCP boundary applies governance.CheckExecCommand + minimalExecEnv (2026-10-05 exec-firewall fix).",
	},
	{
		File:   "internal/apply/apply.go",
		Line:   233,
		Rule:   "unsafe-exec",
		Reason: "apply's runGo: `go test` with internally derived package paths (affectedPackages from the git-scoped change set, ./dir form, no shell) under fsutil.MinimalExecEnv — same class as the refactor/mutation compile steps; the MCP boundary carries the documented governance carve-out for the engine-derived default.",
	},
	{
		File:   "internal/runtime/live.go",
		Line:   411,
		Rule:   "unsafe-http",
		Reason: "runtime live-probe GET to an internally derived URL (version/status check surface).",
	},
	{
		File:   "internal/sandbox/network.go",
		Rule:   "unsafe-exec",
		Reason: "sandbox/script runtime: runs the isolation availability probe and the seatbelt/unshare wrap with fixed argv behind the governance gate (KERN_ALLOW_EXEC / KERN_TOOLS) — governed execution is the product's purpose. File-wide so probe edits cannot un-suppress the class.",
	},
	{
		File:   "internal/sandbox/sandbox.go",
		Rule:   "unsafe-exec",
		Reason: "sandbox/script runtime: spawns the child with a fixed argv inside the isolated sandbox (seatbelt profile / unshare netns) behind the governance gate (KERN_ALLOW_EXEC / KERN_TOOLS) — governed execution is the product's purpose, not a hazard. File-wide so line edits in the run path cannot un-suppress the class.",
	},
	{
		File:   "internal/script/script.go",
		Func:   "networkNSFor",
		Rule:   "unsafe-exec",
		Reason: "sandbox/script runtime: executes code inside the isolated sandbox (seatbelt profile / unshare netns) behind the governance gate (KERN_ALLOW_EXEC / KERN_TOOLS) — governed execution is the product's purpose, not a hazard. Func-scoped so profile edits above the call sites cannot un-suppress it.",
	},
	{
		File:   "internal/script/script.go",
		Func:   "RunScript",
		Rule:   "unsafe-exec",
		Reason: "sandbox/script runtime: executes code inside the isolated sandbox (seatbelt profile / unshare netns) behind the governance gate (KERN_ALLOW_EXEC / KERN_TOOLS) — governed execution is the product's purpose, not a hazard. Func-scoped so profile edits above the call sites cannot un-suppress it.",
	},
	{
		File:   "internal/sdk/client.go",
		Line:   217,
		Rule:   "unsafe-http",
		Reason: "kern SDK client: posts to the configured server base URL — the client's purpose.",
	},
	{
		File:   "internal/sdk/client.go",
		Line:   236,
		Rule:   "unsafe-http",
		Reason: "kern SDK client: GET against the configured server base URL — the client's purpose.",
	},
	{
		File:   "internal/sec/engine_gosec.go",
		Line:   73,
		Rule:   "unsafe-exec",
		Reason: "gosec -quiet -json ./...: fixed argv scanner invocation.",
	},
	{
		File:   "internal/setup/cli.go",
		Rule:   "unsafe-exec",
		Reason: "launches an operator-configured MCP server command (stdio transport) during setup — operator config authorship, same trust class as the CLI itself.",
	},
	{
		File:   "internal/setup/install_global_git.go",
		Line:   218,
		Rule:   "unsafe-exec",
		Reason: "git config --global core.hooksPath <hooksDir>: fixed argv, operator-invoked setup.",
	},
	{
		File:   "internal/setup/remove.go",
		Line:   199,
		Rule:   "unsafe-exec",
		Reason: "uninstall probe: fixed binary path with internally derived args — operator-invoked teardown surface.",
	},
	{
		File:   "internal/setup/setup_wrappers.go",
		Line:   15,
		Rule:   "unsafe-exec",
		Reason: "version probe of a resolved kern binary: fixed argv, read-only.",
	},
	{
		File:   "internal/synthtest/synthesize.go",
		Line:   1191,
		Rule:   "unsafe-exec",
		Reason: "synthesize-test compile step: go argv assembled internally (build/test with fixed subcommands); the operator CLI is the trusted principal.",
	},
	{
		File:   "internal/validate/validate.go",
		Line:   497,
		Rule:   "unsafe-exec",
		Reason: "validation sandbox executor: runs the declared command inside the kern sandbox (seatbelt/unshare isolation) — isolation is the control.",
	},
	{
		File:   "internal/verification/compliance.go",
		Line:   58,
		Rule:   "unsafe-exec",
		Reason: "govulncheck -json: fixed argv scanner invocation.",
	},
	{
		File:   "internal/verification/manifests.go",
		Line:   442,
		Rule:   "unsafe-exec",
		Reason: "git show HEAD:<manifest>: the manifest path comes from kern's own resolved manifest list — no user-controlled command string.",
	},
	{
		File:   "internal/verifycmd/verify_command.go",
		Line:   216,
		Rule:   "unsafe-exec",
		Reason: "kern verify --command: the operator/agent-supplied command under the same governed wrapper; the CLI is the trusted principal and the MCP mirror gates at its boundary.",
	},
	{
		File:   "internal/webhook/webhook.go",
		Line:   224,
		Rule:   "unsafe-http",
		Reason: "webhook delivery: POSTs to the operator-configured webhook URL — the feature itself.",
	},
}

// LoadSuppressionRegistry merges the compiled-in defaults with the optional
// user file <root>/.kern/verify-suppressions.json (same schema, additive).
// The file is operator triage policy, so it is validated fail-closed: a
// missing file is the normal built-ins-only path, but an unreadable file,
// malformed JSON, or an entry lacking file/rule/reason is an error rather
// than being silently dropped. Exported so the `kern sec` CLI applies the
// SAME triage as VerifySecurity: a suppressed finding must not fail one
// surface while passing the other.
func LoadSuppressionRegistry(root string) (*SuppressionRegistry, error) {
	reg := &SuppressionRegistry{Suppressions: append([]Suppression(nil), defaultSuppressions...), resolver: newFuncResolver(root)}
	path := filepath.Join(root, ".kern", "verify-suppressions.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return reg, nil
		}
		return nil, fmt.Errorf("verification: read %s: %w", path, err)
	}
	var user SuppressionRegistry
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, fmt.Errorf("verification: malformed %s: %w", path, err)
	}
	for i, s := range user.Suppressions {
		if strings.TrimSpace(s.File) == "" || strings.TrimSpace(s.Rule) == "" || strings.TrimSpace(s.Reason) == "" {
			return nil, fmt.Errorf("verification: invalid %s entry %d: file, rule, and reason are required", path, i+1)
		}
	}
	reg.Suppressions = append(reg.Suppressions, user.Suppressions...)
	return reg, nil
}

// funcResolver maps a repo file:line to its enclosing Go function name,
// lazily and cached per file. It lets Func-keyed suppressions survive line
// drift (an edit above the site no longer un-suppresses the class). Only
// .go files are parsed, and only on first query for that file; a file that
// fails to parse or lies outside the root resolves to "" (no match) so a
// Func-keyed entry never silently suppresses on broken input.
type funcResolver struct {
	root  string
	cache map[string]map[int]string // rel path -> line -> enclosing func name
	mu    sync.Mutex
}

func newFuncResolver(root string) *funcResolver {
	return &funcResolver{root: root, cache: map[string]map[int]string{}}
}

func (r *funcResolver) funcFor(rel string, line int) string {
	if r == nil || r.root == "" || !strings.HasSuffix(rel, ".go") {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.cache[rel]; ok {
		return m[line]
	}
	m := map[int]string{}
	path := filepath.Join(r.root, filepath.FromSlash(rel))
	src, err := os.ReadFile(path)
	if err == nil {
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, src, 0)
		if perr == nil {
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				name := fd.Name.Name
				start := fset.Position(fd.Pos()).Line
				end := fset.Position(fd.End()).Line
				for l := start; l <= end; l++ {
					if _, exists := m[l]; !exists {
						m[l] = name
					}
				}
			}
		}
	}
	r.cache[rel] = m
	return m[line]
}
