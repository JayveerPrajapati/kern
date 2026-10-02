---
name: kern-incident-triage
description: >-
Use when investigating any crash, panic, error log, stack trace, or failing test. Compress the raw log with kern_optimize (action=log), run kern ops triage to map stack frames to AST symbols, auto-synthesize a minimal failing reproduction test in an ephemeral worktree, and auto-repair the defect.
---

<!-- canonical source: internal/skills/assets/kern-incident-triage/SKILL.md; copies must stay identical — run kern setup to sync -->

# Kern Auto-SRE Incident Triage Runbook

Use this skill when investigating production incidents, analyzing stack traces, debugging failing tests, or resolving error logs.

---

## Triage Workflow

```
[Compress Log] ➔ [Correlate to AST] ➔ [Synthesize Test] ➔ [Isolated Worktree Fix]
 (kern_optimize log)   (symbol mapping)     (sandbox test)         (kern ops triage)
```

---

## Step 1: Compress Raw Logs

Never paste uncompressed, noisy logs into context. Compress them first using `kern_optimize` (action=log):
```json
{"request": "compress this log: <paste log text>"}
```
This strips repetitive timestamps, thread IDs, and boilerplate while preserving critical error frames, exceptions, and unique traces.

---

## Step 2: Automated Incident Triage with KernOps

Run the Auto-SRE triage command pointing to the error log:
```bash
kern ops triage --log /path/to/incident.log
```
Available flags:
- `--log <path>` — path to the raw log / stack trace file, or `-` for stdin
- `--repo <root>` — repository root path (default `.`)
- `--non-interactive` — run in headless mode
- `--auto-approve` — automatically grant approvals for CI runs
- `--json` — emit the triage report as JSON

Reproduction test synthesis and isolated-worktree fixes are automatic (not flag-gated): every `kern ops triage` run writes a minimal failing unit test in an ephemeral Git worktree sandbox.

What `kern ops triage` performs automatically:
1. **Deduplication**: Clusters duplicate stack traces into unique failure signatures.
2. **AST Symbol Mapping**: Resolves raw file/line frames to exact functions, methods, and types in the prebuilt index.
3. **Reproduction Test Synthesis**: Writes a minimal failing unit test in an ephemeral Git worktree sandbox.
4. **Auto-Repair Loop**: Synthesizes a patch to make the reproduction test pass without violating firewall gates.

---

## Step 3: Verify the Fix

1. Review the synthesized patch in the worktree.
2. Run test verification:
   ```json
   {"request": "verify test coverage and run build"}
   ```
3. Run `kern check` to verify architectural conformance.

---

## Executable Helper Script

Automatically compress and triage an incident log:
```bash
./scripts/triage.sh /path/to/error.log
```

