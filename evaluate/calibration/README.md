# evaluate/calibration — risk-scale calibration & impact F1

Reproducible calibration numbers for kern's review model, following the
F1-style protocol code-review-graph publishes: on real commit history, does
the tool's prediction match the files the commit actually changed?

## Run

```sh
go run ./evaluate/calibration              # self-calibration on kern's own history
go run ./evaluate/calibration ../project   # calibration for the project being reviewed
go run ./evaluate/calibration . --commits 120 --thresholds 2.0,4.0,6.0
```

Requires a git checkout with history; the index is built if not cached.

## What the two tables mean

### 1) Risk-threshold calibration (review load vs recall)

`kern_changes` / `kern review` score every changed file:

    risk = 1.0
         + log2(1 + direct callers)          # who is affected immediately
         + log2(1 + transitive blast)        # who is affected through the graph
         + 1.5 if the change crosses packages
         + 2.0 if changed symbols have no test coverage
         + 1.0 per hub symbol changed, +0.5 per call into a hub

"Flagging" a file means the reviewer should look at it. The analysis is
diff-driven, so it can never flag a file the commit did not touch — precision
is 1.0 by construction and is NOT reported. The calibration question is
recall vs review load:

| threshold | recall | mean flagged/commit |
|---|---|---|
| 2.0 | 1.000 | 13.26 |
| 4.0 | 0.849 | 11.26 |
| 6.0 | 0.730 | 9.69 |
| 8.0 | 0.596 | 7.91 |

Interpretation for kern's own history: recall stays high up to ~6.0 while
review load barely moves, because most changed files are genuinely risky
(self-selection — we commit cross-cutting changes). For a new project, run
the sweep and pick the knee, or keep the 4.0 default (the action
`risk-threshold` gate in the PR workflow).

### 2) Impact F1 (CRG protocol)

Given the symbols a commit touched, the graph predicts the affected files
(blast radius). Ground truth = files actually edited in that commit.
Precision = predicted files that were really edited; recall = edited files
the graph predicted. This is the honest error budget of the call graph:

    precision=0.039 recall=1.000 F1=0.076   (kern on kern, measured 2026-09-22, 54 scored commits)

Recall 1.000 means no real ripple edit was missed; precision 0.039 means the
graph massively over-predicts (every caller of a changed symbol is
"affected", but authors rarely edit all of them). That is the expected
conservative bias of a deterministic static graph — the number to watch
across versions is F1 trending up without recall dropping below ~0.9.

Regenerate these numbers with:

```sh
go run ./evaluate/calibration/              # self-calibration on kern's own history
```

> Context for the low precision: the underlying call graph over-predicts
> because a large share of callees are unresolved — `kern index --status`
> on this snapshot reports 6161/9379 call edges resolved (34% unresolved).
> Unresolved callees widen the predicted blast radius, which drags precision
> down while keeping recall at 1.000. Precision is expected to rise as graph
> resolution improves; the earlier 0.235 figure was measured on a 60-commit
> sample with a different (smaller, more resolvable) index snapshot and is
> not directly comparable.

## Honesty notes

- Files with no indexable content (docs, generated files not in the index)
  are excluded from both ground truth and prediction; commits touching only
  those are skipped.
- Single-commit scoring uses `git diff -U0 <c>^..<c>` with added-line ranges,
  so a one-line edit does not flag every symbol in a 500-line file.
- The sweep counts one "flagged file" per changed file above the threshold;
  it does not weight by lines changed or risk magnitude.