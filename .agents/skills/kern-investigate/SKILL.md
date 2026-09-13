---
name: kern-investigate
description: >-
  Investigate symbols, architecture, dependencies, or blast radius in a repository using kern's prebuilt index instead of slow file reads and greps.
---

<!-- canonical source: internal/skills/assets/kern-investigate/SKILL.md; copies must stay identical — run kern setup to sync -->

# Kern Codebase Investigation Runbook

Use this skill when exploring an unfamiliar codebase, tracing call paths, analyzing dependencies, or checking what might break before making changes.

## Guiding Rule

Kern maintains a prebuilt symbol index and call graph. **Never** start by reading entire files or running raw `grep`/`find`/`glob`. Always query the index first.

---

## Phase 1: High-Level Orientation

1. **Map Repository Layout & Conventions**:
   Call `kern_project_map` (or via `kern_meta`):
   ```json
   {"request": "show me the project layout and architecture conventions"}
   ```
2. **Inspect Architecture Clusters**:
   ```json
   {"request": "show me the architectural components and entry points"}
   ```

---

## Phase 2: Symbol & Call-Graph Discovery

1. **Find Symbols (Function, Struct, Interface)**:
   Call `kern_search` or `kern_ast_search` with the symbol name:
   ```json
   {"request": "find the Dispatcher struct and NewServer function"}
   ```
2. **Understand Symbol & Call Hierarchy**:
   Call `kern_code_graph` or `kern_explore` on the target symbol:
   ```json
   {"request": "who calls HandleRequest and what does it call?"}
   ```
3. **Trace Shortest Call Path Between Components**:
   ```json
   {"request": "what is the call path from HandleHTTP to StoreRecord?"}
   ```

---

## Phase 3: Token-Sized Slices (When Verbatim Code Is Needed)

Only read verbatim code if you need to inspect exact logic. Instead of reading the whole file, fetch a slice sized to your token budget:
```json
{"request": "get context for HandleRequest within 400 tokens"}
```
Or call `kern_context(symbol="HandleRequest", budget=400)`.

---

## Phase 4: Blast Radius Simulation

Before proposing modifications to any symbol:
1. Run `kern_impact` or `kern_what_if`:
   ```json
   {"request": "what breaks if I change the signature of ProcessOrder?"}
   ```
2. Check for untested dependents or risk hotspots:
   ```json
   {"request": "check test coverage and call risk for ProcessOrder"}
   ```

---

## Executable Helper Script

Run symbol discovery and dependency analysis directly:
```bash
./scripts/inspect.sh <symbol_name>
```

