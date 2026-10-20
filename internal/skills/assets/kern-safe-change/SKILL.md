---
name: kern-safe-change
description: Use for ANY code change in a kern-managed repo - edit, refactor, rename, add a feature, or prep a PR. Runs the governed change lifecycle - pre-edit blast radius check, surgical edits guided by the call graph, verification gates via kern verify, auto-repair via kern heal, then a tamper-evident evidence bundle and a deterministic commit message.
---

<!-- canonical source: internal/skills/assets/kern-safe-change/SKILL.md; copies must stay identical — run kern setup to sync -->
<!-- note: some tools named in this runbook are full-catalog (KERN_MCP_FULL=1) tools; the 7-role squad reaches them in-process, external agents on the default-29 surface should use kern_meta to route or set KERN_MCP_FULL=1 -->

# Kern Safe Change & Refactor Runbook

Use this skill whenever modifying code, refactoring symbols, adding features, or preparing pull requests in a kern-managed codebase.

---

## The Governed Change Lifecycle

```
[Pre-Edit Risk] ➔ [Implement] ➔ [Verify Gates] ➔ [Auto-Repair] ➔ [Evidence & Commit]
  (kern_impact)              (kern_verify)      (kern_heal)     (kern evidence, git commit)
```

---

## Step 1: Pre-Edit Blast Radius & Safety Check

Before modifying any file or symbol, assess the blast radius and risk score:
```json
{"request": "pre-edit risk check for modifying internal/index/engine.go"}
```
* Run `kern_impact` on the change to map blast radius before editing.
* Check if callers are covered by tests. If not, synthesize a test first using `kern_heal` or a targeted `kern_verify` run.

---

## Step 2: Implement Changes

Perform the necessary code modifications:
- For surgical structural changes or renames, prefer `kern_graph`/`kern_context` analysis over naive regex search/replace.
- For semantic merges, verify with `kern_verify` (types=build,test) after the change.
- Build, test and lint through kern, not bash. Pass your own command to `kern_verify` and ask only for what you need: `kern_verify command="go test ./..." output=summary` (output: `summary` counts and failing tests, `failures`, `tail:N`, `lines:A-B`, `full`). The full run is kept, so `kern_verify anchor=<id> output=lines:A-B` re-slices it without rerunning. CLI: `kern verify --command "go test ./..." --output summary`.

---

## Step 3: Run the Verification Gate (`kern verify`)

Verify the change against the security and architecture gates (secret detection, boundary enforcement, LOC caps):

```bash
kern verify security,architecture
```
With changes staged (the pre-commit tier):
```bash
kern verify --staged security,architecture
```
Or in JSON format for automated parsing:
```bash
kern verify security,architecture --json
```

If any check fails:
- Review the specific failing check in the report (e.g. hardcoded secrets, boundary breach, architecture drift).

---

## Step 4: Auto-Repair in Sandbox (`kern heal`)

If failures are repairable, trigger the sandboxed self-correction loop:
```bash
kern heal --task "<what the code is meant to do>"
```
Re-run `kern verify security,architecture` to confirm the gates pass cleanly.

---

## Step 5: Evidence Bundle & Commit

1. **Export a Tamper-Evident Evidence Bundle**:
   ```bash
   kern evidence export --full-state
   ```
2. **Commit with a Deterministic Message**:
   ```bash
   kern commit
   ```

---

## Executable Helper Script

Run pre-edit risk checks and verification gates before and after changes:
```bash
./scripts/pre_check.sh <optional_file_or_symbol>
```
