---
name: kern-team-orchestration
description: >-
  Orchestrate kern's 7-role specialist agent squad (Planner, Architect, Coder, Reviewer, Security, Tester, SRE) across the Explore, Plan, Edit, and Verify lifecycle.
---

<!-- canonical source: internal/skills/assets/kern-team-orchestration/SKILL.md; copies must stay identical — run kern setup to sync -->

# Kern Multi-Agent Team Orchestration Runbook

Use this skill to orchestrate kern's 7 specialized agent personas when tackling complex features, large refactors, security audits, or production triage.

---

## The 7 Specialist Agent Roles

| Persona | Role | Key Capabilities | Autonomy | Primary Triggers |
|---|---|---|---|---|
| **Planner** | `RolePlanner` | `source:read`, `docs:read`, `memory:read` | L0–L3 | `kern_meta("plan <task>")`, `kern_plan` |
| **Architect** | `RoleArchitect` | `source:read`, `graph:read`, `boundaries:read` | L0–L3 | `kern_meta("architecture / impact")`, `kern_explore` |
| **Coder** | `RoleCoder` | `source:read`, `source:write`, `tests:read` | L2–L3 | `kern-safe-change`, `TreeDiff`, `kern_refactor` |
| **Reviewer** | `RoleReviewer` | `source:read`, `tests:read`, `verify:run` | L0–L2 | `kern_meta("review changes")`, `kern_review` |
| **Security** | `RoleSecurity` | `source:read`, `security:run` | L0–L2 | `kern_check`, `kern_sec`, `kern_taint` |
| **Tester** | `RoleTester` | `tests:read`, `tests:write`, `test:run` | L0–L2 | `kern_synthesize_test`, `kern_validate` |
| **SRE** | `RoleSRE` | `runtime:read`, `ops:read`, `deploy:read` | L0–L4 | `kern-incident-triage`, `kern_correlate_evidence` |

---

## 4-Phase Team Orchestration Workflow

### Phase 1: Explore (Planner & Architect)
1. **Discover Layout & Conventions**:
   ```json
   {"request": "show me the project map and active packages"}
   ```
2. **Trace Architecture & Call Graph**:
   ```json
   {"request": "explore symbol Server and its callers and callees"}
   ```

### Phase 2: Plan & Blast Radius (Planner & Architect)
1. **Calculate Impact Matrix**:
   ```json
   {"request": "what breaks if I modify Server.dispatch?"}
   ```
2. **Draft Phased Milestones**:
   ```json
   {"request": "produce a phased implementation plan for the refactor"}
   ```

### Phase 3: Surgical Mutation (Coder & Tester in Sandboxes)
1. **Execute Changes in Ephemeral Sandboxes**:
   Use `TreeDiff` and `EvaluateCandidates` to validate patches without risking live files.
2. **Auto-Synthesize Tests**:
   Ensure new and modified paths have dedicated test coverage before merging.

### Phase 4: Verification & Release (Security, Reviewer & SRE)
1. **Validate All 40 Firewall Gates (G0–G39)**:
   ```bash
   kern check
   ```
2. **Generate Signed CI Attestation**:
   ```bash
   kern ci
   ```

---

## Executable Helper Commands
- Initialize standard team: `kern team --root .`
- Check workspace flight logs: `kern flight list`
- Verify system health & parity: `kern doctor`
