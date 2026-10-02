# kern_commands.md — one-command-at-a-time QA ledger

Per the sweep rule: exactly one kern command per pass, exercised with real
invocations, findings + views appended before the next command is selected.

## kern execute

**Date:** 2026-10-01 · **Binary:** cb64669 · **Scenario:** scratch git repo
(`/tmp/kern-exec-test/repo`, module calctest) with a fake PAT planted in
`.git/config` (`https://ghp_FAKE_PAT_FOR_AUDIT_1234567890@github.com/...`).

### Findings

1. **Blind-audit hazard #6 NOT reproducible (CLOSED):** the audited claim was
   "`kern execute` moves .git aside to the parent dir, exposing the PAT from
   .git/config in output". At HEAD: the fake PAT never appears in output
   (grepped the full transcript), no `.git*` artifact is created outside the
   repo, and the working tree stays byte-identical (sandbox-only contract
   holds). This closes the last unverified hazard from the 2026-09-29
   blind audit — all six now have live-verified dispositions.
2. **Happy path:** git-format patch → verdict PASS, embedded diff shown, task
   recorded (`t-2 COMPLETED`), exit 0, working tree untouched.
3. **Bad-patch handling:** a malformed hand-written patch fails with
   `kern: Execute: invalid patch: apply patch failed: exit status 128: error:
   corrupt patch at <stdin>:9` and exit 1 — correct fail-loud semantics,
   no tree mutation.
4. Output is compact and useful (verdict, summary, diff, task id).

### Views

- The command does exactly what its help promises ("nothing is applied to the
  working tree") and nothing more — the sandbox-only contract is verifiable
  with a single `git status` after invocation.
- The sandbox applies the patch and runs a build gate before reporting PASS,
  which is the right shape for an "apply and verify" primitive.
- No issues found. No changes required.

## kern run

**Date:** 2026-10-01 · **Binary:** cb64669 · **Scenario:** same scratch repo,
intent "add a Sub function to calc.go that subtracts a from b".

### Findings

1. Deterministic intent compiler, not an executor: classifies intent
   (CODE_CHANGE), selects workflow (B_SAFE_CHANGE), target (Sub), risk
   (MEDIUM, approval: none), and lists the derived caps/tools/agents. Exit 0,
   clean 10-line output, useful "next:" hint pointing at the tool sequence or
   `kern do`.
2. Task bookkeeping is correct: the run created task t-3/t-4 in CREATED state
   — accurate, since `kern run` plans but does not execute. `kern tasks`
   merges cross-process tasks correctly (t-1 FAILED from a bad patch, t-2
   COMPLETED, t-5 COMPLETED from `kern do` — all from separate CLI
   invocations, one table).

### Views

- Right shape for a "compile then drive" primitive: it tells you exactly
  which tools/agents the workflow wants, instead of hiding them.
- No issues found.

## kern do

**Date:** 2026-10-01 · **Binary:** cb64669 · **Scenario:** same repo, intent
"add a Sub function", Ollama DOWN (verified: localhost:11434 unreachable).

### Findings

1. **Autonomous loop verified end-to-end with no local Ollama:** with Ollama
   down, the provider chain fell back to the local agent CLI and the output
   named it (`provider: claude`) — the documented fallback.
   No silent hang (probeLLMProvider guards it).
2. All 9 stages ran with correct skip semantics: intent/remember/plan/code/
   verify/observe/learn ok; protect+deploy `skipped:below-autonomy` at
   default L2. Verify ran build+test+security+architecture+dependency — all
   PASS.
3. The coder produced a CORRECT change (`func Sub(a, b int) int { return
   a - b }`) in 1 round, 6.03s, and the diff was presented as the
   deliverable.
4. **Working tree untouched — by design at L2** (source: `runDo` — "L0
   read-only, L2 sandbox code, L3 PR creation, L4 deploy with approval").
   Verified: `git status` clean, `grep Sub calc.go` empty, build still OK.
5. Learned memory recorded (id 2cb5e3c1206c-0).

### Views

- This is the strongest safety design in the repo: an autonomous coding
  agent that writes a correct patch, verifies it five ways, and still does
  not touch your tree unless you escalate levels. The level ladder
  (read-only → sandbox → PR → deploy-with-approval) is the right shape for
  autonomy gating.
- The provider-fallback naming is a genuinely good observability touch —
  you always know which LLM served the run.
- Minor UX consideration (not a bug): a user who expects `kern do` to apply
  changes may be surprised the tree is clean afterwards; the output's
  "deployed: false" plus the diff makes it discoverable, but an explicit
  "applied: false (L2 = sandbox; use --level L3 for a PR)" line would remove
  all ambiguity.

## kern deploy / kern request-approval / kern approve

**Date:** 2026-10-01 · **Binary:** cb64669 · **Scenario:** same scratch repo;
tasks from earlier `kern do` runs; approval store `.blueprint/approvals/`.

### Findings

1. **Deploy state machine is correct and fail-loud:** `kern deploy` on a
   COMPLETED task → "state COMPLETED is terminal... no transition allowed"
   (exit 1); on a CREATED task → "invalid task transition: CREATED ->
   DEPLOYING" (exit 1). No path deploys a task from a wrong state.
2. **Deploy is fail-closed twice over:** without `KERN_ALLOW_DEPLOY` the L4
   deploy stage reports "deploy skipped: KERN_ALLOW_DEPLOY not set
   (production mutation disabled by default)"; with the env set but no
   deployer configured it reports "deploy skipped: no deployer configured
   (simulated)". A misconfigured machine cannot accidentally deploy.
3. **Two-person rule verified end-to-end:** `kern request-approval
   --intent=... --files=...` creates an `apr-*` request (risk classified,
   recorded in `.blueprint/approvals/requests.jsonl`), prints the exact
   human command to run. `kern approve <id> --approver --reason` records the
   decision with timestamp; `--reject` path works (exit 0). **Single-use
   key enforced:** replaying approve on a decided id exits 3 with "already
   decided as approved" (exit 3 = policy family per the exit-code contract).
4. Discoverability gap (minor): `kern request-approval --help` prints only
   "usage: kern request-approval [flags]" — the real flags (--intent,
   --files, --source, --requester, --repo) are only in source comments. A
   bare invocation does print "--intent is required" (exit 2), so it is
   learnable, but the help is thin.
5. **Cross-run pattern memory is live:** the remember stage surfaced my
   repeated similar intents as a recurring "unhealthy" pattern (deployed=
   false at L2, score ~0.55) and the planner incorporated it into risk
   ("treat it as a flagged constraint"). Working as designed.

### Views

- The approval gate mechanics (request → decide → single-use → audit jsonl)
  are solid and correctly exit-coded.
- Observation worth a design look: **every L2 sandbox run records
  deployed=false, which the pattern extractor scores as "unhealthy"** —
  iterative sandbox-first work (the recommended mode!) progressively poisons
  the pattern memory and bleeds "recurring failure" warnings into later
  plans. A pattern feature distinguishing "L2 sandbox by choice" from
  "failed deploy" would remove the false positive.
- Deploy state-machine errors are exemplary: name the task, the state, and
  why the transition is refused.

## kern evidence (export / verify / explain)

**Date:** 2026-10-01 · **Binary:** cb64669 · **Scenario:** scratch repo with
accumulated audit records from the earlier approval/execute/task runs.

### Findings

1. **Round-trip verified:** export produced a schema-v1 bundle (7.3KB) with
   embedded authorization proof (ALLOW decision), agent id, repo root,
   freshness, and a 16-entry audit chain. verify → exit 0, "Bundle ... VALID.
   Audit chain intact."
2. **Tamper-evidence verified:** mutating one authorization field in the
   JSON and re-verifying → exit 2, "bundle hash mismatch — content tampered
   or bundle_hash altered". Fail-loud, correct exit code.
3. `kern evidence explain` produces a genuinely readable plain-language
   summary ("What this proves: the agent was authorized to touch exactly
   the 1 in-scope symbols at ...; the index was fresh; the audit chain is
   intact through ...").
4. The seal is digest-only ("unsigned") in this environment — signatures
   would need key material; the digest seal is still a strong integrity
   check as demonstrated.

### Views

- This is the most complete feature exercised in the sweep: export →
  verify → explain → tamper-detect all behave exactly as a
  tamper-evident log should, with the failure path proven, not assumed.
- The explain output is the differentiator — most audit tooling makes you
  parse JSON; this states what the evidence PROVES in prose.

## kern cross-repo-impact

**Date:** 2026-10-01 · **Binary:** cb64669 · **Scenario:** scratch repo +
the kern repo, symbol `Add`.

### Findings

1. Works, and is fast for what it does: 4.4s, 20 cross-repo references
   across 3 repositories (the two --repo flags plus previously registered
   repos picked up from the cache).
2. Report shape is right: per-repo caller lists with file:line, truncated
   gracefully ("… and 6 more callers in kern").
3. Matching is by simple symbol name, so it is inherently approximate
   (`Add` matches every Add in every repo) — reasonable for a survey tool,
   and the output makes the semantics clear by listing callers per repo.

### Views

- For a rename/refactor pre-flight across a service mesh of repos this is
  the right primitive; the caveat is users must understand simple-name
  matching (qualified-name matching would be a future refinement).
- No issues found.
