---
name: kern-team-orchestration
description: Use for any multi-step, multi-agent, or parallel task - complex features, large refactors, security audits, incident response. Coordinates kern's 7 specialist roles (Planner, Architect, Coder, Reviewer, Security, Tester, SRE) across the Explore, Plan, Edit, Verify lifecycle via kern run.
---

<!-- canonical source: internal/skills/assets/kern-team-orchestration/SKILL.md; copies must stay identical — run kern setup to sync -->
<!-- note: some tools named in this runbook are full-catalog (KERN_MCP_FULL=1) tools; the 7-role squad reaches them in-process, external agents on the default-29 surface should use kern_meta to route or set KERN_MCP_FULL=1 -->

# Kern Multi-Agent Team Orchestration Runbook

Use this skill to orchestrate kern's 7 specialized agent personas when tackling complex features, large refactors, security audits, or production triage.

---

## The 7 Specialist Agent Roles

| Persona | Role | Key Capabilities | Autonomy | Primary Triggers |
|---|---|---|---|---|
| **Planner** | `RolePlanner` | `source:read`, `docs:read`, `memory:read` | L0–L3 | `kern_meta("plan <task>")`, `kern_plan` |
| **Architect** | `RoleArchitect` | `source:read`, `graph:read`, `boundaries:read` | L0–L3 | `kern_meta("architecture / impact")`, `kern_explore` |
| **Coder** | `RoleCoder` | `source:read`, `source:write`, `tests:read` | L2–L3 | `kern-safe-change`, `TreeDiff` |
| **Reviewer** | `RoleReviewer` | `source:read`, `tests:read`, `verify:run` | L0–L2 | `kern_meta("review changes")`, `kern_review` |
| **Security** | `RoleSecurity` | `source:read`, `security:run` | L0–L2 | `kern_verify` (types=security) |
| **Tester** | `RoleTester` | `tests:read`, `tests:write`, `test:run` | L0–L2 | `kern_verify` (types=test) |
| **SRE** | `RoleSRE` | `runtime:read`, `ops:read`, `deploy:read` | L0–L4 | `kern-incident-triage` |

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
1. **Run the Verification Gates (security, architecture)**:
   ```bash
   kern verify security,architecture
   ```
2. **Export a Signed Evidence Bundle**:
   ```bash
   kern evidence export
   ```

---

## Executable Helper Commands
- Run the full pipeline on an intent: `kern run "<intent>" --root .`
- Check system health & parity: `kern doctor`
