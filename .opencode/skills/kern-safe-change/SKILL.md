---
name: kern-safe-change
description: >-
  Safely mutate, refactor, or edit code in the repository with pre-edit blast radius checks, firewall gate validation (G0-G39), auto-repair, and cryptographic CI receipts.
---

<!-- canonical source: internal/skills/assets/kern-safe-change/SKILL.md; copies must stay identical — run kern setup to sync -->

# Kern Safe Change & Refactor Runbook

Use this skill whenever modifying code, refactoring symbols, adding features, or preparing pull requests in a kern-managed codebase.

---

## The Governed Change Lifecycle

```
[Pre-Edit Risk Check] ➔ [Sandboxed Mutation] ➔ [Change-Firewall Gates] ➔ [Auto-Repair] ➔ [Receipt & Commit]
      (kern_pre_edit)       (ast_transform)         (kern check)          (kern fix)      (kern ci / commitmsg)
```

---

## Step 1: Pre-Edit Blast Radius & Safety Check

Before modifying any file or symbol, assess the blast radius and risk score:
```json
{"request": "pre-edit risk check for modifying internal/index/engine.go"}
```
* Or call `kern_pre_edit(file="path/to/file.go", symbol="TargetSymbol")`.
* Check if callers are covered by tests. If not, synthesize a test first using `kern_synthesize_test`.

---

## Step 2: Implement Changes

Perform the necessary code modifications:
- For surgical structural changes or renames, prefer `kern_ast_transform` or `kern_rename` over naive regex search/replace.
- For semantic merges, verify with `kern_semantic_diff`.

---

## Step 3: Run the Native Change-Firewall (`kern check`)

Verify staged changes against native change-firewall gates G0–G39 (G3 secret detection, G2 architecture/boundary enforcement, G6 duplication):

```bash
kern check
```
Or in JSON format for automated parsing:
```bash
kern check --json
```

If any gate fails:
- Review the specific violated gate (e.g., G3 secret detection, G2 boundary breach, G30 gofmt drift).

---

## Step 4: Auto-Repair in Sandbox (`kern fix`)

If violations are repairable, trigger the sandboxed auto-repair loop:
```bash
kern fix
```
Re-run `kern check` to verify that all gates pass cleanly.

---

## Step 5: Generate Cryptographic Receipt & Commit

1. **Generate CI Receipt**:
   ```bash
   kern ci
   ```
2. **Verify Receipt Authenticity**:
   ```bash
   kern verify-receipt
   ```
3. **Generate Conventional Commit Message**:
   Call `kern_commitmsg` to produce a deterministic, standardized commit message based on the AST diff:
   ```json
   {"request": "generate commit message for staged changes"}
   ```

---

## Executable Helper Script

Run pre-edit risk checks and change-firewall validation before and after changes:
```bash
./scripts/pre_check.sh <optional_file_or_symbol>
```

