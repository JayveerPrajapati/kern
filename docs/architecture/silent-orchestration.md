# Silent Orchestration — packages & patterns (tracker Phases 6–11)
The silent-orchestrator extension added eight capability packages on top of
the Kern 2.0 foundation. All are deterministic (no LLM) except the explicit
opt-in `BlindJudge`. None added MCP tools beyond the existing catalog (still
121); every new surface is an optional arg on an existing tool or a CLI that
mirrors an existing tool.

## Security & privacy — `internal/governance` (Phase 6)
- **Egress firewall** (`egress.go`): `EgressPolicy` enum
  (`local-only` / `external-redacted` / `external-approved`), `EgressRule
  {Policy, Hosts, Ports}`, pure `CheckEgress(rule, host, port)`, `IsLocalHost`
  (loopback/private/link-local/ULA/single-label/`.local`), and
  `Firewall.CheckEgress(agentID, host, port)` — auth → authz
  (`Can("egress","connect")`) → rule decision → one-shot approval gate →
  allow/deny, every outcome audited. `Firewall.Check` gained step 3.5:
  `action == "egress"` evaluates the rule (unset → local-only default),
  denied targets fail closed, external-approved defers to the approval gate.
- **Secret stripping** (`secrets.go`): `SecretFilter{Patterns, Exact,
  Allowlist, RedactWith}`, `DefaultSecretFilter`, `IsSecret`, `StripSecrets`.
  Wired into `sandbox.Run` (`c.Env` — defense-in-depth over the allowlist)
  and `script.RunScript` NoIsolate path (the real leak: unisolated runs
  previously inherited the full operator env).
- **Policy decisions** (`decisions.go`): `PolicyDecision{Policy, Decision,
  Reason}` + `RecordDecision` → `AuditEntry` (new `Policy`/`Reason` fields,
  additive + backward compatible with the persisted tamper chain) +
  `PolicyEvaluated`/`PolicyBlocked` bus events.

## Review lenses — `internal/lenses` (Phase 7)
See `docs/review-lenses.md`. Ranking-only (`ApplyLens`), five built-ins,
`--lens` on kern_analyze (real fact re-ranking via
`TaskService.AnalyzeWithLens`) and kern_context/kern_review (validated lens
line). Never drops claims; balanced never reorders.

## Evaluation harness — `internal/eval` (Phase 8)
- `EvalHarness{Samples, Budget, Rubric, Reproducible}` + `Run` →
  `EvalResult{Score, TokenReduction, EvidenceRetention, ErrorRate,
  Reproducible, Samples, Rubric}`. Deterministic: `tokenize.Count` +
  `budget.Fit` + `strings.Contains` evidence retention; never calls the
  judge.
- `Assertion` rubric: `AssertTokenReduction` (≥), `AssertEvidenceRetention`
  (≥), `AssertErrorRate` (≤); unknown type fails.
- `BlindJudge{Provider, Model, Blind}` — opt-in external surface:
  `Provider.Generate` with `Temperature: 0` + fixed seed; `Blind` hides
  baseline vs candidate labels ("Output A/B").

## Portable skills — `internal/skills` (Phase 9)
`portable.go`: `Skill` + `SkillManifest{Version, Author, Permissions,
Dependencies}`; `ParseManifest` (tolerant frontmatter), `LoadSkillsFromDir`
(subdirs with SKILL.md; unreadable skipped; missing dir errors), `ValidateSkill`
(name required; well-formed manifest), `PreviewPermissions` (dedupe by
resource|action). ed25519 `SignSkill`/`VerifySkill` — canonical payload
includes the playbook body, so any tampering breaks the signature. The
embedded skills (`skills.go`) are untouched.

## Output profiles — `internal/profiles` (Phase 10)
See `docs/context-envelope.md` for the envelope; profiles shape it:
`OutputProfile{Name, Style, Format, Language}`, four built-ins
(`machine-json` / `action-first` / `human-readable` / `debug`),
`ApplyProfile` (pure shaping — no compression, so no-profile output is
byte-identical), three-layer `RawEvidence → RenderedEvidence (terse.Compress)
→ FormattedEvidence` with `Render`/`Format`/`Pipeline`. `--profile` arg on
kern_context/kern_review/kern_analyze (applied last, after the lens).

## Integration & verification (Phase 11)
- `internal/integration/` — test-only package composing the real packages
  end-to-end: `TestContextEnvelope`, `TestProgressiveDisclosure`,
  `TestDeterministicPlanner`, `TestHostAdapter`, `TestReviewLenses`,
  `TestPhase6to10Integration`. Tiny fixture repos + `index.Build`; full suite
  runs in ~1s.
- `internal/verification/silent.go` — end-to-end helpers:
  - `VerifyFullPipeline(root, symbol)` → `FullPipelineReport` (envelope →
    planner → evidence selection → L1/L2/L3 retrieval → host
    inject/extract into a temp copy; never touches the real tree),
  - `VerifySilentOrchestration(root, symbol)` → (silent, violations) — the
    rendered pipeline must not leak kern internals (`.kern/`, `kern_`,
    `internal/`, `MCP` markers),
  - `VerifyTokenReduction(root, symbol)` → `eval.EvalResult` — token
    reduction without critical-evidence loss.

## Patterns to follow
- Deterministic where possible; LLM only behind the `llm.Provider` interface
  and only for explicitly opt-in surfaces (`BlindJudge`).
- Fail closed (egress, approvals); never silently degrade.
- Additive API changes only (`AuditEntry.Policy/Reason`, new methods, new
  args on existing tools) — the catalog and existing callers stay intact.
- Every outcome is audited (`RecordDecision`); every new surface has a
  byte-identical default path.