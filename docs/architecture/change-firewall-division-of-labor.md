# Change Firewall: Division of Labor between Kern and Blueprint

## Purpose

Kern and Blueprint form one system — the **AI Change Firewall** of Kern 2.0 —
with a strict division of labor:

- **Kern's governance firewall gates agent intent.** It decides *whether an
  agent may attempt a change*: intent classification, workflow selection,
  risk assessment, and human approval gates, all recorded in a tamper-evident
  audit log.
- **Blueprint's change firewall gates file changes.** It decides *whether a
  proposed change to a repository is admissible*: architecture boundaries,
  secrets, structural duplication, policy (mode, per-source overrides,
  suppressions), all recorded in a self-hashed audit trail.

An agent-authored change passes **both** firewalls. They are two enforcement
surfaces of one capability, not two tools doing the same thing.

## The division

| Dimension | Kern firewall | Blueprint firewall |
| --- | --- | --- |
| What it gates | Agent *intent* (a proposed task/change request) | *File changes* (content entering a repo) |
| Question it answers | "Is this change allowed to be attempted?" | "Is this change safe to write/commit?" |
| Input | Intent string + task lifecycle | Change request (repo, source, files, content) |
| Decision mechanism | Intent classification → workflow → risk → approval gate (human-in-the-loop) | Deterministic checks + policy (fail-closed), exit-code contract |
| Entry points | `kern_run` / `kern_workflow` / `kern loop` | Pre-commit hook, MCP `validate_staged` / `validate_proposed`, `blueprint ci`, `blueprint fix` |
| Audit | `.kern/audit/` — tamper-evident hash chain | `.blueprint/audit/audit.jsonl` — self-hashed records |
| Oracle | Index + call graph + evidence (its own intelligence) | Shells out to kern (guard/sec/fingerprint) under a versioned JSON contract |
| Failure mode | Approval required → task parks until resolved | Violation → BLOCK/ERROR with redacted, actionable findings |

## How they compose

1. **Intent gate (kern).** An agent proposes work through `kern_run` /
   `kern_workflow`. The intent is classified, the workflow selected, risk
   assessed; high-risk actions park at a human approval gate. The decision
   lands in the governance audit log.
2. **Change gate (blueprint).** Once approved, the agent's concrete file
   changes are validated by blueprint at whichever entry point applies:
   - pre-write, via MCP `blueprint_validate_proposed` (content not yet on
     disk — the seam where agent intent meets file content),
   - pre-commit, via the hook (`blueprint check --staged`),
   - pre-merge, via `blueprint ci` or `kern ci` (full `base..head` diff),
   - or as a repair loop, via `blueprint fix` or `kern fix` (proposed fixes validated in an
     isolated worktree).
3. **Unified in-tree layout.** Blueprint is natively integrated into Kern under
   `internal/blueprint/`. In addition to compatibility binaries (`cmd/blueprint`,
   `cmd/blueprint-mcp`), its governance commands are first-class subcommands on
   Kern: `kern check`, `kern fix`, `kern ci`, and `kern verify-receipt`.
4. **Shared intelligence.** Blueprint never reimplements analysis: it
   consumes kern's `guard` / `sec` / `fingerprint` oracles under a versioned
   JSON contract (`schema_version`), fail-closed on mismatch.
5. **Confinement.** Both tools confine agent-supplied paths: kern via
   `KERN_ROOTS` + `KERN_MCP_ROOTS` (pre-tool gate), blueprint via
   `BLUEPRINT_ROOTS` (pre-tool gate with nested path coverage).

## Reading order

- Kern governance model: `docs/architecture/agent-orchestration.md`,
  `docs/architecture/gap-analysis.md` (firewall capability).
- Blueprint: its README ("Enforcement layers") and the strengthening plan in
  the sibling workspace (`support_docs/blue/`).
- The contract between them: `schema_version` on every kern JSON output
  blueprint consumes (guard, sec, fingerprint).

## Shared findings format

Blueprint's `Finding` is the shared findings format across both tools
(rule_id/severity/category/message + provenance: rule_version, kern_version,
index_freshness, confidence, scope). Canonical contract:
`schema/findings-schema.json` + `docs/findings-format.md` in the blueprint
repo. Evolution is additive only; the redaction invariant (a redacted finding
never carries secret material in text fields) applies to every consumer.
