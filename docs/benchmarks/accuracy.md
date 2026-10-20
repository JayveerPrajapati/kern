# Index Accuracy Per Language

**Suite:** benchmarks · **Status:** [measured] — measured at git `2ba8f90` · **Corpus:** committed fixture corpus (`internal/index/testfixture`)

Where is kern's index reliable? This page publishes the honest per-language
accuracy answer. The numbers below are **measured, not hand-written**: they come
from running both extractors (tree-sitter and the regex fallback) over the
committed fixture corpus — the same measurement `TestParityReportPerLanguage`
pins, so a change to any extractor fails the parity gate AND this doc's
freshness gate (`TestAccuracyDocMatchesParity`) until both are updated
deliberately.

## What "accuracy" means here

kern extracts symbols and call edges. Two extraction tiers exist per language:

| Tier | Meaning |
|---|---|
| `resolved` | Native AST parsing (Go via `go/ast`; Java via the dedicated parser). Call edges carry AST-grade HIGH confidence. |
| `ast` | Tree-sitter grammar parse (the default build — requires CGO). |
| `heuristic` | Regex fallback (`-tags notreesitter`). Lower fidelity by contract: a documented **subset** of tree-sitter's facts. |

The parity contract (see [`internal/index/parity_test.go`](../../internal/index/parity_test.go)):
the regex path is a *documented lower-fidelity subset* of tree-sitter — it may
be less certain, never more certain, and never silently drop a fact
tree-sitter is confident in. That contract is enforced per edge, per language.

- **Recall** = shared edges / tree-sitter edges — how much of the AST truth the
  regex fallback surfaces. Pinned at **1.000** on every measured language: the
  subset contract holds perfectly.
- **Precision** = shared edges / regex edges — how much of what regex reports
  is AST-confirmed. Below 1.0 by exactly the *documented declaration-line
  artifacts* (regex also records edge targets at declaration sites; the parity
  gate enumerates and hard-asserts every such edge, so the gap is accounted
  for, never a surprise).

## Measured per-language parity (tree-sitter build, fixture corpus)

| Language | TS symbols | Regex symbols | Shared symbols | TS edges | Regex edges | Shared edges | Regex precision | Regex recall |
|---|---|---|---|---|---|---|---|---|
| `java` | 8 | 7 | 7 | 6 | 11 | 6 | 0.545 | 1.000 |
| `python` | 11 | 11 | 11 | 8 | 13 | 8 | 0.615 | 1.000 |
| `typescript` | 15 | 13 | 12 | 9 | 13 | 9 | 0.692 | 1.000 |

Shared-edge confidence mix (regex, tree-sitter): `MEDIUM,HIGH` 11 ·
`MEDIUM,MEDIUM` 12 — regex never claims higher confidence than tree-sitter
on a shared edge (enforced).

## Go (not parity-measured — stronger)

Go has no regex fallback: extraction is native `go/ast`, tier `resolved`.
Every call edge carries AST-grade HIGH confidence by construction, so the
subset/parity question does not arise. Java is likewise `resolved` even in
the pure-Go build (dedicated parser).

## What a user should take from this

- **Default (tree-sitter) build:** Go and Java resolved; java/python/typescript
  (and every other tree-sitter grammar language) at AST precision — reliable
  for intelligence queries.
- **Pure-Go build (`-tags notreesitter`):** Go and Java stay resolved; the
  other languages are the regex subset — full recall of shared facts, lower
  confidence, known declaration-line artifacts. `kern doctor`'s `precision`
  check reports the tier mix of your actual index, and `--precision strict`
  queries skip heuristic-tier edges rather than guessing.

## Regenerate

```
go test ./internal/index -run 'TestParityReportPerLanguage' -v
```

prints the measured table; `TestAccuracyDocMatchesParity` fails if this page's
numbers lag the measurement. Update both together, deliberately.
