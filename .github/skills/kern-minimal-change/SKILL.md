---
name: kern-minimal-change
description: >-
  Opt-in restraint overlay for ANY code change. Climbs the minimal-change ladder before writing: build nothing if you can, reuse what exists, stdlib before platform before a new dependency, a one-liner before an abstraction, and only then the minimum code that works. Use when asked to keep changes small, avoid over-engineering, or in any repo where restraint matters.
---

<!-- canonical source: internal/skills/assets/kern-minimal-change/SKILL.md; copies must stay identical — run kern setup to sync -->

# Kern Minimal Change Ladder

Before writing code, climb down this ladder and stop at the FIRST rung that
solves the problem. Do not descend past it.

1. **Build nothing.** Does this need to exist at all? Delete the need, not
   just the code — a feature nobody asked for is the most expensive kind.
2. **Reuse what exists.** `kern_search` the concept, `kern_explore` the
   symbol, `kern_impact` the blast radius — then extend the existing thing
   instead of writing a near-duplicate.
3. **Standard library.** A stdlib import is free; code you maintain is not.
4. **Platform primitives** (language and runtime features) before inventing
   your own version of what the platform already does.
5. **An existing dependency** before a new one. Adding a dependency is a
   decision, not an accident.
6. **A one-liner** before an abstraction. Abstractions earn themselves by
   paying rent twice — wait for the second caller to exist before you
   build the abstraction.
7. **Only then:** the minimum code that works.

Rungs 2 and 5 are machine-checked: `kern verify` warns when new code
structurally duplicates existing code, and when the change introduces
dependencies that were not there at HEAD. The rest is judgment — that is
the point: checks enforce, this skill persuades.

## Carve-outs — never be lazy about these

- Understanding the problem before solving it.
- Input validation at trust boundaries.
- Error handling that prevents data loss.
- Security.
- Accessibility.
- Calibration against real hardware or production measurements.
- Anything explicitly requested.

"Minimal" never means skipping these.

## Name the ceiling

When you deliberately simplify, leave a comment naming the ceiling and the
upgrade path. The next reader must know exactly how far the shortcut
extends and what growing past it costs:

```go
// kern: linear scan — index when the log exceeds ~10k entries
```

## One runnable check

Non-trivial logic leaves ONE runnable check behind: an assert-based demo or
one small test that fails when the behavior breaks. No frameworks, no
fixtures, no suite — one check. Trivial one-liners need none.

## Root cause, not symptom

Before editing a shared function, `kern_impact` it: find every caller.
Fix the function once instead of patching N call sites — or worse, copying
its logic N times.
