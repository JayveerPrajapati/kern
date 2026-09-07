---
name: kern-incident-triage
description: >-
  Triage runtime crashes, errors, or production logs by compressing logs, correlating stack traces to AST symbols, creating reproduction tests, and auto-repairing.
---

# Kern Auto-SRE Incident Triage Runbook

Use this skill when investigating production incidents, analyzing stack traces, debugging failing tests, or resolving error logs.

---

## Triage Workflow

```
[Compress Log] ➔ [Correlate to AST] ➔ [Synthesize Test] ➔ [Isolated Worktree Fix]
 (kern_optimize_log)   (symbol mapping)     (sandbox test)         (kern ops triage)
```

---

## Step 1: Compress Raw Logs

Never paste uncompressed, noisy logs into context. Compress them first using `kern_optimize_log`:
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
Or with specific options:
```bash
kern ops triage --log /path/to/incident.log --reproduce --worktree
```

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

