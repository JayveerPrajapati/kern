# PHASE 16 — AUDIT + RESUME + REPLAY — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 16.1–16.4.
Go: 1.23, stdlib-only default build.

## Scope

Tasks can survive restart and resume without losing state; every run leaves an
auditable, replayable trail.

## Micro-phase audit (all verified in source + tests)

- **16.1 (P0) Flight recorder** — `internal/flight`: Record
  (task/agent/action/context/result/status/approved/timestamp) + the full
  canonical action vocabulary covering every required item: task_started,
  context_retrieved, memory_retrieved, tool_called, decision_made,
  file_modified/changed, test_executed, approval_requested, change_accepted,
  deployment_performed, production_outcome, pr_created,
  verification_started/completed. The loop records a flight record per stage
  (loop.go:410); helpers query WhyDecision/WhatContextUsed/WhichToolsCalled/
  WhoApproved/WhatChanged/WhatTested. Cross-instance persistence tested.
- **16.2 (P1) Resume** — `TaskService.Resume` + `reconstructContext`
  (rehydrates ContextPacket + Plan from artifacts + rich snapshot — "full
  reconstruction", not a shell) + CLI `kern task resume <id>`. The exit gate
  was additionally proven cross-PROCESS in Phase 9 (fresh TaskService resumes a
  parked workflow).
- **16.3 (P1) Replay** — `TaskService.ReplayTask` records repository version,
  model, config hash + (NEW) context version + tool versions; CLI
  `kern task replay <id>` also shows the snapshot history.
- **16.4 (P2) Compare runs** — `TaskService.RunCompare` (artifact diff +
  snapshot history + agent / tool-call proxy / cost / success dimensions),
  tested by TestRunComparePopulatesRichDimensions.

## Gap found and closed

### G1 — Replay record lacked context version + tool versions (16.3 partial)

`ReplayRecord` captured repo/model/config-hash but not the "context version"
or "tool versions" the spec lists — two replays could not be compared on what
context the run saw or what tools it used.

**Fix (`internal/app/task.go`):**
- `ReplayRecord` gains `ContextVersion` (SHA-256 digest of the task's rendered
  context packet) + `ToolVersions` (SHA-256 digest of the distinct step
  actions the task invoked), both derived deterministically from the task
  itself inside `ReplayTask`.

## Exit gate

> "Tasks can survive restart and resume without losing state." — **MET and
> LOCKED**: cross-process resume (Phase 9), context reconstruction
> (TestResumeReconstructsContext / RichSnapshotContext), and the replay record
> now carrying the full Phase 16.3 metadata set (extended
> TestReplayTaskRecordsMetadataAndReplayRecord asserts ContextVersion +
> ToolVersions digests).

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 packages pass, exit 0** (app + flight + 88 rest)
- Existing replay/resume/compare suites still PASS (additive fields)