# Memory Governance

Memory governance (backlog item **P1-007**) adds a governance layer over the
typed engineering memory store (`internal/memory`). It controls **who** can
read/write/delete memories, keeps a **persistent audit trail** of every memory
operation, and enforces **retention policies** (expiry, archiving, size caps).

The layer lives in three files:

| File | Concern |
|---|---|
| `internal/memory/governance.go` | Access control (policy, permissions, `Governance` wiring) |
| `internal/memory/audit.go` | Audit trail (events, persistence, filtering) |
| `internal/memory/retention.go` | Retention policies (expiry, archive, size cap) |

## How it works

A `MemoryStore` is created un-governed with `NewMemoryStore` and behaves exactly
as before. Attach a `Governance` layer with `WithGovernance` (or the
environment-driven `WithEnvGovernance`) to start enforcing permissions,
recording audits and applying retention:

```go
store := memory.NewMemoryStore(root).WithGovernance(memory.NewGovernance(root))
```

`NewGovernance` reads its configuration from the environment (see below) and
never fails: malformed policy/retention config falls back to the permissive
default / no-op retention, and the audit trail degrades to in-memory, so
memory stays usable even when governance config is wrong.

The default policy is **fully permissive** (everyone may read, write and
delete) and the default retention never expires or archives — so attaching
governance never breaks existing behavior; it only starts recording the audit
trail. Tighten the policy to actually restrict.

## Access control

### Permissions

| Permission | Grants |
|---|---|
| `read` | Recalling memories (`AuthorizedRecall`) |
| `write` | Adding (`Add`), updating (`Update`), superseding (`Supersede`), retiring (`MarkHistorical`) |
| `delete` | Removing memories (`Delete`) |

The governed read path is `AuthorizedRecall(query, agentID, clearance)`, which
combines the governance read check with the existing classification-clearance
filter (spec §41 F-55). The un-governed `Recall`/`List`/`Get` paths remain
available for internal/CLI use and do not require an agent identity.

For write operations the acting agent is taken from the memory's `Source`
field (`"human"` when empty); delete/update/retire operations currently
attribute the actor as `"human"`.

### Policy JSON

```json
{
  "default_permissions": ["read", "write", "delete"],
  "agents": {
    "planner-agent": ["read", "write"],
    "auditor-agent": ["read"]
  },
  "allowed_agents": ["planner-agent", "auditor-agent", "writer-agent"],
  "denied_agents": ["rogue-agent"]
}
```

Semantics:

- `agents` — per-agent grants. An agent not listed falls back to
  `default_permissions`.
- `default_permissions` — baseline for unknown agents. Empty means no
  permissions.
- `allowed_agents` — non-empty allowlist: only these agents may access memory.
- `denied_agents` — always wins: denied every permission regardless of other
  grants.
- An agent with an explicit empty grant (`"agent": []`) has **no** permissions.

Every denial is recorded in the audit trail with `allowed: false` and the
reason, and the store returns an error that satisfies
`errors.Is(err, memory.ErrPermissionDenied)`.

## Audit trail

Every governed operation is recorded as an `AuditEvent`:

| Field | Meaning |
|---|---|
| `id` | `memory-audit-<seq>` |
| `timestamp` | UTC time of the operation |
| `agent_id` | acting agent (`Source`, the `agentID` passed to `AuthorizedRecall`, `"human"`, or `"retention"`) |
| `operation` | `add`, `update`, `recall`, `delete`, `supersede`, `retention` |
| `memory_id` | affected memory (when applicable) |
| `type` / `scope` | memory type and scope (when applicable) |
| `root` | project root the store serves |
| `allowed` | `true` = operation succeeded, `false` = denied |
| `reason` | denial reason / retention summary |

Events are kept in a bounded in-memory ring (1000) and **persisted** to
`<cache>/memory-governance/audit/<project-hash>/` as one JSON file per event,
so the trail survives restarts. Persistence is best-effort: a failed write
degrades the trail to in-memory but never fails the memory operation being
audited.

### Querying the trail

```go
trail := store.Governance().Audit
trail.Recent(50)                              // newest first
trail.FilterByAgent("planner-agent")          // who did what
trail.FilterByOperation(memory.OpDelete)      // all deletions
```

## Retention

A `RetentionPolicy` controls the memory lifecycle:

| Field | Behavior |
|---|---|
| `enabled` | Turns expiry/archiving on. The size cap applies regardless. |
| `max_entries` | Caps the store size; oldest entries dropped beyond it (default `200`). |
| `expire_after` | Auto-deletes memories older than this duration. `0` disables. |
| `archive_after` | Retires memories older than this to the `historical` status (kept for reference, no longer surfaced as current). `0` disables. |

Durations accept Go syntax (`"720h"`, `"168h"`) or a day suffix (`"30d"`,
`"7d"`).

### Retention JSON

```json
{
  "enabled": true,
  "max_entries": 200,
  "expire_after": "365d",
  "archive_after": "90d"
}
```

When enabled, every write enforces the policy (expired entries are dropped,
old entries archived, size capped). An explicit sweep reports the counts:

```go
res, err := store.ApplyRetention()
// res: {Kept, Expired, Archived, Trimmed}
```

The sweep is recorded in the audit trail as operation `retention` with the
counts in the reason.

## Configuration

| Variable | Purpose |
|---|---|
| `KERN_MEMORY_GOVERNANCE` | `1`/`true`/`on`/`yes` enables governance on stores created through `memory.WithEnvGovernance` (used by the enterprise server). Unset = legacy un-governed behavior. |
| `KERN_MEMORY_POLICY` | Inline JSON access-control policy (see above). |
| `KERN_MEMORY_POLICY_FILE` | Path to a JSON policy file (used when `KERN_MEMORY_POLICY` is empty). |
| `KERN_MEMORY_RETENTION` | Inline JSON retention policy (see above). |
| `KERN_MEMORY_RETENTION_FILE` | Path to a JSON retention file. |
| `KERN_MEMORY_AUDIT_DIR` | Override the audit persistence directory. Defaults to `<cache>/memory-governance/audit/<project-hash>/`. |

Example: run a governed server with a restricted policy and 90-day expiry:

```sh
export KERN_MEMORY_GOVERNANCE=1
export KERN_MEMORY_POLICY='{"default_permissions":["read"],"agents":{"planner-agent":["read","write"]}}'
export KERN_MEMORY_RETENTION='{"enabled":true,"expire_after":"90d"}'
```

## Scope notes

- Governance applies to the typed `MemoryStore` (`store.go`). The legacy
  lesson-only API in `memory.go` (package-level `Add`/`Recall`/`List`/`Clear`)
  is the v1 compatibility surface and remains un-governed.
- Access control is best-effort identification: write attribution comes from
  the memory's `Source` field, so callers that omit it are treated as
  `"human"`. For strict per-agent enforcement, pass an explicit `Source` on
  writes and use `AuthorizedRecall` (which takes the agent ID) for reads.