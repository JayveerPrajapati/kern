# Review Lenses — internal/lenses
Review lenses re-rank evidence by named review postures (security,
performance, maintainability, architecture, balanced) so a reviewer sees the
most relevant evidence first. Deterministic, stdlib-only, no I/O.

## Core types — `internal/lenses/lens.go`
- `Lens` (`lens.go:15`): `{Name, Priorities []EvidencePriority, Weight}`.
- `EvidencePriority` (`lens.go:22`): `{Type domain.EvidenceType, Weight}`
  — evidence classes come from `internal/domain/domain.go:150-161`:
  `graph`, `test`, `build`, `git`, `runtime`, `memory`, `policy`.
- `Registry` (`lens.go:28`): `NewRegistry` / `Register` (duplicate and
  empty-name errors) / `Select(name) (Lens, bool)` / `List` (sorted).

## Built-ins — `lens.go:69-160`
| Lens | Name | Top priorities |
|------|------|----------------|
| `SecurityLens` | security | policy 1.0, runtime 0.8, graph 0.6, git 0.5 |
| `PerformanceLens` | performance | runtime 1.0, graph 0.7, test 0.5, git 0.4 |
| `MaintainabilityLens` | maintainability | graph 1.0, test 0.7, git 0.6, build 0.4 |
| `ArchitectureLens` | architecture | graph 1.0, policy 0.6, memory 0.5, git 0.5 |
| `BalancedLens` | balanced | all seven types 1.0 |

`NewRegistryWithBuiltins()` pre-populates all five. `CombinedLens(name,
lenses...)` merges lenses per evidence type by average weight (deterministic,
type-sorted) for presets like "security-focused".

## Application — `lens.go:180-255`
- `ApplyLens(l Lens, claims []domain.Claim) []domain.Claim`: score(claim) =
  sum of priority weights over `claim.Evidence[].Type` (unscored types → 0);
  stable sort by score DESCENDING. Ties keep original order, so the balanced
  lens (all weights 1.0) never reorders. **Never drops claims** — ranking
  only.
- `GetPriorities(l Lens)` / `RenderPriorities(l Lens)` — priority listing
  (weight DESC, type ASC, %.2f) for CLI/MCP display.

## Surfaces
- MCP `kern_analyze --lens X`: real re-ranking — `TaskService.AnalyzeWithLens`
  (`internal/app/task_analysis.go`) re-ranks the packet facts by the lens
  before rendering and task attachment; unknown lens fails the task with
  `unknown lens %q`.
- MCP `kern_context --lens X` / `kern_review --lens X`: validate the lens and
  prepend a deterministic `lens: <name> (<priorities>)` line; unknown lens →
  error; no lens arg → byte-identical output.
- No new MCP tools were added for lenses (the arg lives on existing tools);
  the catalog stays at 121.

## Guarantees
- Deterministic: identical input → identical ranking (stable sort, fixed
  weights).
- Budget-independent: ranking never truncates; token fitting is a separate
  layer (`internal/budget`).
- Lenses are orthogonal to output profiles (`internal/profiles`) — a lens
  decides *what evidence ranks first*, a profile decides *how the answer is
  shaped*.