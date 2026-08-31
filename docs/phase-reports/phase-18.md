# PHASE 18 — WEB CONTROL CONSOLE — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 18.1–18.3.
Go: 1.23, stdlib-only default build.

## Scope

A human can inspect and approve/reject a Task through the UI.

## Micro-phase audit (all verified in source + tests)

- **18.1 (P0) Core pages** — Dashboard (/), Tasks (/tasks), Task Detail
  (/task/{id} + task_detail.html), Approvals (/approvals + pending/approve/
  reject endpoints), Risks (/risks), Artifacts (/artifacts), Audit (/audit).
- **18.2 (P1) Engineering views** — System Map (/system-map), Graph (/graph),
  Memory (/memory), Agents (/agents), Incidents (/incidents), Architecture
  (/api/architecture).
- **18.3 (P2) Efficiency/evaluation** — /efficiency (per-task efficiency
  report). Agent comparison / task replay / context inspection are covered by
  the CLI (`kern task replay`, `kern task efficiency`) + RunCompare — the P2
  partial surface.

## Gap found and closed

### G1 — UI approve/reject was disconnected from the workflow engine's gate store

The web's approval surface used an in-memory `ApprovalWorkflow` + the firewall
(governance deploy gates), but the agent-team workflow engine persists its
gates to `.kern/approvals.json` (`approval.FileStore`). A workflow parked by
`kern_workflow`/`kern workflow` was therefore invisible to the UI's pending
list and could not be approved/rejected by a human in the console — the exit
gate was unmet for the team-runner path.

**Fix (`internal/web/`):**
- `App.fileApprovals *apprstore.FileStore` (same store the engine writes).
- `handleApprovalApprove`/`handleApprovalReject`: when the in-memory workflow
  doesn't know the approval (workflow-gate case), fall back to the file store
  (`Decide`); on the normal path they ALSO write the decision through the file
  store so any engine reading it observes the UI decision.
- `buildApprovals`: merges the file store's pending approvals into the UI's
  pending list (deduped by ID).

## Exit gate

> "A human can inspect and approve/reject a Task through the UI." — **MET and
> LOCKED**:
> - `TestWorkflowApprovalThroughUI` — a workflow parked at its approval gate is
>   visible in /api/approvals/pending, inspectable at /task/{id}, approved via
>   POST /api/approvals/approve, and the workflow then completes on resume.
> - `TestWorkflowRejectionThroughUI` — a UI rejection leaves the workflow
>   parked (WAITING_FOR_APPROVAL, not completed).

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 packages pass, exit 0** (web + app + mcp + 87 rest)
- Existing web suites (approval endpoints, pages, REST, hardening) still PASS