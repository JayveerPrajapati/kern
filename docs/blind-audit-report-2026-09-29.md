# Blind behavioral audit of kern — report 2026-09-29

**Scope & method.** kern was tested *with kern itself*, blindly: fresh binaries
built from HEAD `94e3ddf` (`/tmp/kern-audit-bin/{kern,kern-mcp,kern-server}`),
exercised against the kern repo as the test subject and against scratch fixtures.
Evidence = live command traces + Go source only. No docs/*.md, no ARCHITECTURE.md,
no *_test.go used as reference (consistent with the "codebase is source of truth"
rule). Six parallel probe lanes + orchestrator re-verification of every
HIGH/CRITICAL claim. Traces: `/tmp/kern-audit-{B1a,B1b,B2,D1,E1,E2}/trace.md`.
The audit was read-only against the repo tree (working tree left clean).

## Views (summary)

kern's core value — fast AST-indexed search/explore/impact/verify on a local
symbol graph — is real, fast (search 0.8s, explore ~3s, impact 2.5–8.6s on a
~17k-symbol repo), deterministic (byte-identical repeat runs), and robust under
concurrency (8 parallel searches identical; index rebuild racing searches is
safe). The MCP surface is exceptionally clean: 139 tools, no crashes across 239
calls, uniform error handling, out-of-root confinement enforced. The CLI catalog
is honest (all 198 command names resolve; 26 of 29 alias pairs behave
identically, the only divergence being diff/stats).

The gaps cluster in **write-path safety** (commands that mutate the tree without
consent or outside the current root), **fresh-user onboarding** (onboard does more
than it says; doctor contradicts it; hooks install machine-wide), **silent
misbehavior** (false PASS on invalid flags, no-op successes, silent failures with
exit 1 and zero output), and **name/behavior mismatches** (diff≠stats despite
alias claims; execute implies it applies to your tree but never does).

## CRITICAL — destructive / data-loss risk

### C1. `kern rename --apply` edits files OUTSIDE the current root
When `.kern/index.sqlite` carries stale absolute paths (e.g. the index was copied
along with a repo, or the repo was moved), `kern rename` resolves the target file
from the *index's* absolute path, not the current root. **Reproduced by the
orchestrator**: running rename from `/tmp/kern-audit-verify/r2` edited the original
`/Users/jayveer.prajapati/workspace/ConfigurationManagerInterface/appconfig/app_config.go`.
Rename output shows the confusion directly — definitions listed as relative
(`appconfig/app_config.go:9`) while edits apply at the absolute path. There is no
out-of-root guard on write commands; `kern index .` (rebuild) is only a manual
mitigation. Severity: **critical** — a moved/copied repo plus one rename edits the
wrong tree. (Reported E1; independently reproduced; damage reverted.)

### C2. `kern mutate` silently rewrites the working tree
`internal/mutation/mutation.go:314` writes each mutant to the **real file**
(`os.WriteFile(fullPath, mutSrc)`), runs the tests against it, and restores
best-effort (`_ =`) per mutant — no consent prompt, no sandbox, and no guarantee
the restore runs if the process is interrupted or two mutate runs interleave.
Bare `kern mutate` on the repo applied logic-inverting edits (`err != nil` →
`err == nil`) to real files (B1b observed two files modified; E2 independently
observed `kern mutate` rewriting `cmd/kern/cmd_agent.go` continuously during the
storm; D1 saw a mutated file mid-run). A controlled repro on a tiny repo
generated 0 mutants and stayed clean, so the tree-dirty state is tied to
interrupted or concurrent runs rather than normal completion. Severity:
**critical** — a "testing" command must never write to the user's source tree
without an explicit apply step.

## HIGH — use-case breakage / misleading

- **H1. `kern execute` never applies to your tree** — it runs in a sandbox and
  prints "verdict: PASS", which reads as success on your code, but no change
  lands (by design: `internal/execution` builds a worktree). Worse, during
  diffing it **moves the source repo's `.git` aside** to
  `.kern-git-aside-<pid>-<ts>` in the repo's *parent* directory
  (`internal/execution/worktree.go:118-137`, restored via `defer`), and prints
  `.git/config` including the remote URL with an embedded GitHub PAT into its
  output. On a crash the repo is left without its `.git` and the aside (which
  contains credentials) is stranded outside the repo, unignored. (E1 + code)
- **H2. kern's git-hook wiring is machine-wide with no uninstall** — `kern setup`
  / `kern install hook --global` sets `core.hooksPath=~/.kern/git-hooks`
  (verified present on this machine: pre-commit + post-commit), so every `git
  commit` in every repo is intercepted by kern's global pre-commit
  (`internal/setup/setup_git_hooks.go`, `internal/bpcli/cli/install_global_git.go:103`).
  The dispatch table lists 7 hook subcommands (install|diff|store|claude-post|
  claude-prompt|gemini-after|gemini-prompt) — **no `uninstall` anywhere**. And
  `kern hook install`'s local `.git/hooks/post-commit` is dead weight under
  `hooksPath` precedence while claiming "post-commit hook installed" — it also
  hardcodes the installing binary's path, silently breaking when the binary
  moves. (E1 + code + live check)
- **H3. `kern check` mutates the tree** — appends a "blueprint runtime state"
  block to `.gitignore` on a read-only check. Idempotent on the kern repo (block
  already present) but dirties every fresh repo, and runs inside every git commit
  via the global hook. (E1; orchestrator quiet re-run: unchanged on kern repo)
- **H4. `kern verify --types bogus` → false PASS** — invalid enum silently
  ignored → empty check set → exit 0, "verdict: PASS / insufficient data".
  **Reproduced by orchestrator.** A typo in `--types` looks like a green build in
  CI. (B2 + me)
- **H5. `kern ci` fails on non-`main` default branches** — `--base` defaults to
  `main` (`internal/bpcli/cli/ci.go:60`); a repo on `develop` fails "base
  revision main not found" (exit 2) with no default-branch auto-detection, yet
  still writes `.kern/blueprint-result.json`. A documented `--base` workaround
  exists, but the out-of-box experience breaks for any repo whose default branch
  isn't `main`. (E1 + code)
- **H6. `kern onboard` does more than it says, and doctor contradicts it** —
  onboard writes 8+ files (AGENTS.md, opencode.json, .mcp.json, .agents/,
  .opencode/, .gitignore edits) with no surface telling a fresh user that
  "register+index" also rewires agents (observable: E1 trace); then `kern doctor`
  immediately reports the index STALE (hypothesized cause: doctor's freshness
  probe vs the store onboard wrote — the json-vs-sqlite mechanism is the lane's
  interpretation, not verified). `kern setup --detect` separately adds CLAUDE.md
  + `.agents/hooks.json`. Fresh user sees "stale" right after a successful
  onboard. (E1)

## MEDIUM

- **M1. `kern diff` ≠ `kern stats` despite alias claim** — catalog and `--help`
  say diff is "(alias of stats)" but they render different reports (entry ledger
  vs summary). **Reproduced.** (B2 + me)
- **M2. `kern flows` arg handling** — a file arg exits 0 with an empty result and
  sqlite-persist log spam (`mkdir .../dispatch_table.go: not a directory`); a
  symbol arg exits 1 with a raw OS error and no hint that it wants a directory. (B1a)
- **M3. Per-invocation stale-index rebuild is not debounced** — under source
  churn, every command rebuilds the index (12–16s) instead of sharing one rebuild;
  a cold `kern impact` on a path exceeded 30s once during the storm (2.5s quiet).
  Not a crash, but a real multi-user bottleneck when files change. (B1a/E2/me)
- **M4. `kern_verify` MCP tool can exceed 30s under load** — 8–13s quiet, >30s in
  a loaded session, no progress signal; a client-side 30s timeout would treat a
  healthy run as dead. (D1)
- **M5. `kern synthesize-test --apply` generates tests that fail at runtime** —
  compiles (vet OK) but the generated table-driven test is not validated to pass
  against the target. (E1)
- **M6. `kern delete` leaves a 13-byte `package docs` stub** — symbol gone, empty
  file remains. (E1)
- **M7. `kern commitmsg` ignores the intent arg** — always emits `chore: update`;
  on a clean tree it exits 1 with zero output (silent failure). (B1b)
- **M8. `kern rename` dry-run mixes relative + stale absolute paths** — the
  root-cause confusion behind C1; index paths are not re-rooted when the repo
  moves. (E1 + me)
- **M9. `kern buddy` leaks machine-wide stats as project context** — "7829 ops …
  $0.6195 saved" appear in a repo digest, reading as project metrics. (E1)

## LOW / NIT (selected)

L1 `kern verify` with no args runs a full ~4.5-min build+test instead of a usage
error · L2 `kern optimize` exits 0 as a pure no-op when no LLM is configured
(echoes input, 0% saved) · L3 `kern budget --max-tokens 20` silently ignored
outside fit mode · L4 `kern log "<line>"` treats the positional as a file path
(ENOENT); usage omits it · L5 `kern explore` fuzzy-resolves to docs headings and
reports 0 callers for heavily-referenced package vars (var-read edges untracked) ·
L6 `kern prose` silently caps at 20 lines · L7 `kern risk` doubled error prefix
"kern: Risk: risk:" · L8 validate-proposed/explain-finding/repair-guidance are
flag-only (NL positional → "unknown flag"); explain-finding exits 0 with an empty
body on incomplete JSON · L9 `frobnicate --help` exits 0 (root help on stderr) —
breaks command-existence probes · L10 `kern compact /nonexistent` masks ENOENT
with a path-policy message · L11 `search --root /tmp` says "no symbols matched"
without hinting the root isn't a kern repo · L12 `kern verify` FAIL report goes to
stdout (stdout-grep CI traps) · L13 MCP schema nits: project_map/pack declare
`root` REQUIRED but work with `{}`; semcache `action` REQUIRED but defaults;
wrong-type args silently coerced (`root:12345` → "Project: 12345 (0 files)"); the
server never returns a JSON-RPC-level `-32602`, all errors are tool-level ·
L14 `kern_incident` maps alert.severity "high" to "Severity: info" ·
L15 `kern precache` defaults to a watch-mode daemon · L16 `kern install` bare run
leaks internal "blueprint install <hook>" usage · L17 `kern skills install --help`
prints `skills show` usage · L18 no-match exit codes are inconsistent across
commands (docs: 0; search: 1) · L19 `kern taint <path>` exits 1 with zero output
(silent failure).

## Verified healthy (the report's positives)

- **CLI catalog honesty**: all 198 catalog command names resolve to real
  commands (verified via dispatch table — an unknown-command `--help` also exits
  0, so help alone is not a reliable probe); 29 alias pairs behaviorally
  compared, 26 byte-identical, 2 N/A (not aliases), 1 mismatch (diff/stats).
- **MCP robustness**: 139 tools, no duplicate names, no empty descriptions,
  required⊆properties; 239 calls on one server process — 0 crashes, 41 bytes of
  stderr (all harness bookkeeping, none kern-emitted); out-of-root/empty/
  wrong-type/huge args never crash; every error is a clean tool-level `isError`.
- **Concurrency**: 8 parallel searches byte-identical; `index --force` rebuild
  racing searches is safe (observed sqlite fallback under contention — a
  `SQLITE_BUSY … falling back to the JSON cache` warning under load, behaviorally
  safe but not lock-free); determinism byte-identical across repeat runs for the
  sampled commands (note: outputs vary when the source/index changes — that's
  expected, not a defect).
- **Performance**: warm search 0.8s, fts 0.24s, index rebuild 4s, cold rebuild
  15.9s (<60s); explore ~3s; `impact` 5.0–8.6s (lane-measured; 2.5s in the
  orchestrator's quiet re-verification) — impact is the slowest warm read
  command and a bottleneck candidate; `verify` full suite PASS×2 (4781/0/224).
- **Exit-code contract**: `kern exitcode` 0/1/2/3 matches 8 spot-checks.
- **Stream discipline**: errors→stderr, results→stdout (exceptions L12, L9).

## Dropped-from-traces findings (recovered in review)

- **D1. `kern verify` can report "tests: FAIL passed=0 failed=0"** — a FAIL
  verdict with zero failed tests (a go-vet compile error surfaced as a test
  failure in E1's fixture). Contradictory user-facing output; MED.
- **D2. `kern commit "<msg>"` rewrites the user's explicit message** —
  "qa: add commit marker" → `feat(docs): qa commit marker` (E1). Message
  mangling on an explicit user string; MED.
- **D3. `kern wiki` writes files into the current directory** — a query-sounding
  command that writes; belongs to the write-path safety cluster. (B2)
- **D4. `search --repos` returned a Java symbol inside the kern repo index** —
  `notification-listener/src/main/java/...` contamination in a Go repo's index;
  unexplained, deserves investigation. (B1a)
- **D5. `kern_sandbox` on macOS reports "fs confinement unavailable (degraded)"** —
  a live operational caveat for macOS users of sandboxed exec. (D1)

## Recommendations (views)

1. **Write-path safety first**: add an out-of-root guard (verify every resolved
   target path is within the current project root) to rename/refactor/mutate/
   delete/synthesize-test; re-root index paths on load when the repo moved; make
   `mutate` run in a sandbox/scratch by default with an explicit `--apply` (like
   rename) and always self-revert.
2. **`execute` semantics**: either apply to the tree (that's what the name
   implies) or rename it; never copy `.git`/credentials into output; gitignore
   `.kern-git-aside-*`.
3. **Fresh-user flow**: onboard should print what it's about to write and support
   `--index-only`; doctor should read `index.sqlite` freshness, not index.json;
   `hook install` needs `hook uninstall` and a `--local`/`--global` choice.
4. **Fail loudly, not silently**: reject invalid `--types` (false PASS is the
   worst outcome); non-zero exits must always print a reason (commitmsg, taint);
   no-op success paths (optimize) should exit non-zero or say "no-op".
5. **Alias honesty**: fix diff/stats divergence or the alias claim; add a
   catalog self-test that a claimed alias behaves identically (the lane harness
   did exactly this in minutes).
6. **Debounce index rebuilds** across processes (a lock + freshness check already
   exists for the watcher) to avoid the 12–16s per-command rebuild storm.

## Method notes / caveats

- Perf numbers measured during a concurrent 6-lane audit sharing `.kern/
  index.sqlite`; quiet-window re-verification was done for critical claims.
- `kern taint`'s reported silent write and `kern check`'s .gitignore mutation did
  NOT reproduce in quiet re-runs (attributed to storm contamination from C2's
  mutate storm) — only the silent-failure half (L19) is retained.
- No repo files were modified by the audit; the one confirmed cross-repo damage
  (C1) was reverted immediately.
## Remediation closure status (2026-09-29, same day)

All findings below are closed in the working tree (single campaign commit
follows). Verification: per-fix targeted tests + live repro transcripts
(campaign ledger: `.slim/deepwork/blind-audit-remediation-2026-09-29.md`),
independent orchestrator spot-checks on the criticals, Oracle gate
PASS-WITH-EDITS → all 4 required edits + 5 advisories applied, and one full
suite run at the end.

| ID | Status | Where / how |
|---|---|---|
| C1 | FIXED | `internal/rename` re-roots edit paths against the current root + out-of-root guard (refuses with actionable error); regression tests (re-root, refuse-out-of-root, refuse-stale-absolute) PASS |
| C2 | FIXED | `internal/mutation` crash-safe journal (backup → atomic journal persist → mutant write); restore on every exit path incl. panic + SIGINT/SIGTERM; stale-journal self-heal skips LIVE runs (PID probe); fail-closed; `-race` PASS |
| H1 | FIXED | `internal/execution` redacts URL credentials (colon form, bare 40-hex userinfo, ghp_/github_pat_ tokens); signal-safe .git-aside restore; repo-scoped orphan-aside warnings |
| H2 | FIXED | `kern hook uninstall` (local + `--global`, refuses non-kern hooks/paths); shadow warning on install; PATH-resolved hook script |
| H3 | FIXED | `kern check` fully read-only; the runtime `.gitignore` block moved to `kern install hook` (blueprint install) |
| H4 | FIXED | `--types` validated (exit 2, full valid list); a FAIL-with-0-failed verdict prints the vet/coverage reason |
| H5 | FIXED | `ci --base` auto-detects the default branch (symbolic-ref, local) + override hint |
| H6 | FIXED | `onboard` prints every wired file + `--index-only`; wiring now runs BEFORE index build (root cause of doctor-STALE) |
| M1 | FIXED | `diff` de-aliased from `stats` in catalog + help |
| M2 | FIXED | `flows` validates file arg (exit 1 with reason) |
| M3 | FIXED | cross-process rebuild debounce (flock `index-build` scope; wait-and-reuse ≤60s; conservative fallback); `index --force` kept unconditional via `BuildPersistedForce` |
| M4 | FIXED | `kern_verify` reports progress via MCP progress notifications |
| M5 | FIXED | `synthesize-test --apply` validates (vet + scoped `go test -run`) before writing; refuses + rolls back on failure |
| M6 | FIXED | `delete` removes package-clause-only stub files (backup retained) |
| M7 | FIXED | `commitmsg` honors the intent positional (type inferred from verb); clean tree → loud exit 1 |
| M8 | FIXED | `rename` dry-run output re-rooted consistently |
| M9 | FIXED | `buddy` stats labeled `machine-wide (all projects):` |
| L1 | FIXED | bare `kern verify` → usage error, exit 2 |
| L2 | FIXED | `optimize` no-LLM case prints an explicit notice (no silent no-op) |
| L3 | FIXED | `budget --max-tokens` outside fit mode → loud error |
| L4 | FIXED | `log` positional treated as the log line; usage updated |
| L5 | FIXED | `explore` prefers symbol over doc heading + var caveat noted |
| L6 | FIXED | `prose` truncation prints a notice |
| L7 | FIXED | `risk` single `kern:` prefix |
| L8 | FIXED | flag-only commands accept NL positionals; explain-finding errors on incomplete JSON |
| L9 | FIXED | `frobnicate --help` exits 2 (existence probes work) |
| L10 | FIXED | `compact` missing file → `no such file: X` |
| L11 | FIXED | `search` non-repo root hint |
| L12 | BY-DESIGN | report to stdout + nonzero exit is the CI contract (help note added to catalog row) |
| L13 | FIXED | schemas de-required with documented defaults; strict arg typing (`root:12345` → tool-level `root must be a string`); severity mapping fixed |
| L14 | FIXED | full severity mapping (`high` → `Severity: error`) |
| L15 | FIXED | `precache` single-pass default; `--watch` opts into the daemon; `--interval` without `--watch` fails loud |
| L16 | FIXED | `install` user-facing usage |
| L17 | FIXED | `skills install --help` prints its own usage |
| L18 | FIXED | no-match exit codes consistent (docs 0 → exit 1 with hint) |
| L19 | FIXED | `taint` validates path (exit 1 with reason, no silent zero output) |
| D1 | FIXED | see H4 (FAIL-with-0-failed prints the reason) |
| D2 | FIXED | `commit` preserves an explicit message verbatim (flag or positional); generation only when none given |
| D3 | ALREADY-FIXED | wiki writes under `.kern/wiki` at HEAD (verified, no change needed) |
| D4 | BY-DESIGN | `--repos` is the cross-repo search feature; the "foreign" symbols are other REGISTERED projects, not index contamination |
| D5 | FIXED | sandbox message names the reason: Landlock is Linux-only, sensitive-path blocklist inactive on this platform |
