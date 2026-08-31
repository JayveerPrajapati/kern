# Authorized Context (`kern_authorize_context`)

## Overview

`kern_authorize_context` is the **authorization plane for agent context** — the
P0.1 spine primitive that every governed retrieval tool hangs off. Given an
agent identity, a task, and the project index, it computes the exact set of
symbols and call edges the agent is *permitted to read*, together with an
auditable authorization proof that pins the decision to the exact index and
policy inputs that produced it. It exists so a governed agent can answer "what
may I read for this task?" with a cryptographically verifiable answer instead
of guessing, and so a denial is always explainable (stage + reason + policy)
rather than a bare error. It is exposed three ways: the `kern authorize-context`
CLI command, the `kern_authorize_context` MCP tool (in the default tool
surface), and via `kern_meta` NL routing.

## The model

The primitive returns a single contract:

```json
{ "scope": ..., "proof": ... }
```

- **`scope`** (`AuthorizedScope`) is the *authorized subset of the index*: the
  `symbols` the agent may read (with file/line/kind/package), the `edges` (call
  graph) reachable from those symbols, the `denied` symbols the scope excluded
  (each with the stage and reason that excluded it), and the `policy_source`
  that produced the scope (`"task-scope"` or `"permissive-default"`).
- **`proof`** (`AuthorizationProof`) is the *tamper-evident authorization
  decision*: the gateway decision (allowed/denied), the agent summary, the
  effective task scope, index freshness/version, and a SHA-256 `fingerprint`
  over the stable inputs. Mutating the scope or the inputs changes the
  fingerprint — a caller can verify a response was computed over a specific
  index and symbol set.

**Concrete example.** Agent `reviewer-1` asks for the context of task
`refactor-dispatch` with a scope that denies `internal/legacy/`. The response's
`scope.symbols` contains every index symbol whose file passes
`scope.CheckPath` (so nothing under `internal/legacy/`), `scope.edges` contains
only call edges whose caller is in that allowed set, and `scope.denied` lists
the `internal/legacy/` symbols that were excluded, each with
`stage: "path"` and `reason: "path denied by task scope: internal/legacy/..."`.
The `proof` records the decision, `policy_source: "task-scope"`, and a
fingerprint over the index root + updated-at + decision + the sorted allowed
symbol names.

On denial the contract still holds: `scope` is empty (zero symbols) and the
`proof` carries the deny reason — the denial itself is auditable.

## Algorithm

`AuthorizeContext(req Request, ix *index.Index, fw *firewall.Firewall)`
(`internal/governance/authz/authorize.go`) runs a six-step flow, and always
returns a `Response` whose `Proof` is auditable; on denial it additionally
returns `ErrUnauthorized`.

1. **Validate** — `agent_id` is required and the index must be non-nil;
   otherwise a plain error is returned.
2. **Resolve agent** — `req.AgentIdentity` wins if supplied directly,
   otherwise `identity.GetAgent(req.AgentID)` looks the agent up in the
   identity registry. An unknown agent fails closed: `denyUnknownAgent` builds
   an **authentication-stage** denial (`stage: "authentication"`, policy
   `identity.authentication`, risk CRITICAL / score 1.0, mitigation "register
   the agent before use") and returns it with `ErrUnauthorized`.
3. **Effective scope** — `effectiveScope(req)` returns the request's
   `TaskScope` when present, otherwise a permissive default
   (`TaskScope{TaskID: req.Task}` — all paths allowed). `PolicySource` is set
   to `"task-scope"` or `"permissive-default"` accordingly.
4. **Firewall gate** — the firewall owns authentication + permission
   enforcement: `fw.Check(req.AgentID, "context", "read")`. A nil firewall is
   itself a denial (`firewall.availability`, "no firewall configured"); a
   failed check is a denial at `stage: "firewall"` with policy
   `firewall.permission` and reason "context.read denied by firewall".
5. **Symbol / edge filter** — `filterSymbols` partitions `ix.Symbols` by
   `scope.CheckPath(s.File)` into allowed and denied sets (denied symbols
   recorded with `stage: "path"`), then applies the optional `SymbolFilter`
   substring (matched against qualified or plain name) to the *allowed set
   only*. `filterEdges` keeps every call edge whose caller is in the allowed
   set — edges to callees unresolved in the index are kept (external
   dependencies, not a leak) — sorted stably by `From`, then `To`, so the
   output — and the fingerprint — is deterministic.
6. **Proof** — `buildProof` assembles the decision, agent summary (ID +
   permission count), the effective task scope, the fingerprint, index
   freshness/version, and `DecidedAt`; `buildDecision` attaches the
   explain-deny object (`resource: "context"`, `action: "read"`) whenever the
   decision is not allowed.

## CLI usage

```
kern authorize-context -agent <id> -task <desc> [-root .] [-symbol <filter>] [-deny-path <path>] [-json]
```

Flags (`cmd/kern/cmd_authorize_context.go`):

| Flag         | Meaning                                                       |
|--------------|---------------------------------------------------------------|
| `-agent`     | Agent ID to authorize (required)                              |
| `-task`      | Task ID the authorization is scoped to (required)             |
| `-root`      | Project root (default `.`)                                    |
| `-symbol`    | Optional substring filter applied to allowed symbols only     |
| `-deny-path` | Path prefix denied by the task scope (repeatable)             |
| `-json`      | Emit JSON (default `true`); `-json=false` gives text output   |

Exit codes:

| Code | Meaning                                    |
|------|--------------------------------------------|
| 0    | allowed — scope + proof printed            |
| 2    | denied — proof (and denied list) still printed |
| 1    | error (bad flags, index load failure, etc.)|

Example:

```bash
kern authorize-context -agent reviewer-1 -task "refactor dispatch" \
  -root . -deny-path internal/legacy -symbol "Dispatch"
```

Truncated JSON response (allowed):

```json
{
  "scope": {
    "symbols": [
      {
        "name": "Dispatch",
        "qualified": "(*Server).Dispatch",
        "kind": "func",
        "file": "internal/mcp/server.go",
        "line": 210,
        "pkg": "internal/mcp"
      }
    ],
    "edges": [
      { "from": "(*Server).Dispatch", "to": "handleExplore" }
    ],
    "denied": [
      {
        "symbol": { "name": "LegacyThing", "qualified": "LegacyThing", "kind": "func", "file": "internal/legacy/old.go", "line": 41, "pkg": "internal/legacy" },
        "stage": "path",
        "reason": "path denied by task scope: internal/legacy/old.go"
      }
    ],
    "policy_source": "task-scope"
  },
  "proof": {
    "decision": { "decision": "ALLOW", "allowed": true },
    "agent": { "id": "reviewer-1", "permission_count": 3 },
    "task_scope": { "task_id": "refactor dispatch", "paths": [], "denied_paths": ["internal/legacy"] },
    "fingerprint": "9f2c1a7e...",
    "index_freshness": "fresh",
    "index_version": "2026-08-30T12:00:00Z",
    "decided_at": "2026-08-30T12:01:00Z"
  }
}
```

With `-json=false` the text form prints one line, e.g.
`ALLOWED  agent=reviewer-1 task=refactor dispatch symbols=42 edges=67 denied=3 fingerprint=9f2c1a7e...`,
or a `DENIED ... stage=... reason=...` line followed by the per-symbol denied
list. The fingerprint is truncated to its first 12 hex characters for display.

## MCP usage

The `kern_authorize_context` tool (`internal/mcp/handlers_governance.go`,
`handleAuthorizeContext`) is registered in the **default tool surface**
(`defaultTools` in `internal/mcp/server.go`), alongside `kern_meta`, so it is
advertised without `KERN_MCP_FULL=1`.

Input arguments (schema in `internal/mcp/server.go`):

| Arg            | Required | Meaning                                                        |
|----------------|----------|----------------------------------------------------------------|
| `agent_id`     | yes      | Agent ID to authorize (must be registered, e.g. via `kern_agent` / `identity.RegisterAgent`) |
| `task`         | yes      | Task ID the authorization is scoped to                         |
| `root`         | no       | Project root (defaults to current directory)                   |
| `symbol_filter`| no       | Optional substring filter applied to the allowed symbols only  |
| `scope`        | no       | Optional object: `{paths, denied_paths, services, envs, artifacts}` |

Output is the `{scope, proof}` JSON above. On denial the handler returns the
proof JSON **and** an error (`authorize-context denied: ...`) so the denial is
auditable; on allow it appends the index provenance stamp. The firewall is
built per call — the MCP server holds no global firewall state — and an
unregistered agent is denied at the authentication stage by the primitive
itself.

## `kern_meta` routing

`kern_meta`'s NL router (`internal/mcp/handlers_highlevel.go`) classifies
requests containing any of these keywords to `kern_authorize_context`,
passing the full request as the `task`:

- `authorize`
- `authorized`
- `allowed to see`
- `permitted`
- `what can i`

The rule sits **before** the explore/search routing so an authorization
question never falls through to a plain search. Example:

```
kern_meta(request="what can I touch in this repo for the refactor task")
```

→ `[kern] classified as: kern_authorize_context` + the `{scope, proof}` result.

## The proof structure

Every field of `AuthorizationProof` (`internal/governance/authz/types.go`):

| Field            | Type                | Meaning                                                          |
|------------------|---------------------|------------------------------------------------------------------|
| `decision`       | `GatewayResult`     | Gateway outcome: `decision` (`ALLOW`/`DENY`), `allowed`, `risk`, `approval`, and — when denied — `deny` (an explain-deny object with `stage`, `agent_id`, `task_id`, `resource` `"context"`, `action` `"read"`, `reason`, `risk`, `required_approval`, `policy`). |
| `agent`          | `AgentSummary`      | Identity portion of the proof: `id` and `permission_count` (number of permissions on the resolved `AgentIdentity`). |
| `task_scope`     | `TaskScope`         | The effective scope the decision was computed under: `task_id`, `paths`, `denied_paths`, `services`, `envs`, `artifacts`. |
| `fingerprint`    | `string`            | Hex SHA-256 over the stable inputs (below).                      |
| `index_freshness`| `string`            | `"fresh"` when the index was updated < 5 minutes ago, else `"stale"`. |
| `index_version`  | `string`            | `index.UpdatedAt` formatted as RFC 3339 UTC.                     |
| `decided_at`     | `time.Time`         | When the decision was made (UTC).                                |

**The fingerprint.** `fingerprint()` hashes, with SHA-256 over
`root=<index root>`, `index_updated=<RFC3339Nano>`, `policy_source=...`,
`allowed=<true|false>` (plus `deny_stage=...` when denied), and one
`symbol=<qualified name>` line per allowed symbol in **sorted** order. Because
every input is stable and the symbol list is sorted, identical inputs always
produce identical fingerprints and mutating the scope symbols changes it. To
verify a proof, recompute the hash over the same inputs (index identity,
policy source, decision, sorted allowed qualified names) and compare hex
strings; the CLI's text mode shows the first 12 hex characters.

## Policy granularity (P0.1)

P0.1 ships **path-level** filtering only: `TaskScope.CheckPath` (in
`internal/domain/boundary.go`) applies `DeniedPaths` first, then the `Paths`
allowlist, using prefix matching — and `filterSymbols` runs every index symbol
through it. `services`/`envs`/`artifacts` are carried on `TaskScope` and
accepted by the MCP scope object, but P0.1 enforcement is path-scoped.
Symbol-level policy (deny a *symbol* regardless of its file) is deferred to P1.

When no explicit scope is given — in the CLI, when no `-deny-path` is passed,
and in the MCP tool, when no `scope` object is passed — the effective scope is
a permissive default (`TaskScope{TaskID: task}`) and the response records
`policy_source: "permissive-default"`. The default is permissive **by policy,
never by accident**: it is an explicit, labeled decision the proof records,
and the firewall's `context.read` gate still applies on top of it.

## What's deferred

- **Mandatory pre-tool gating of retrieval tools (P1.2)** — `kern_authorize_context`
  is a tool you call ("Use before retrieval when a task must not leak
  out-of-scope code"), not yet a gate that other retrieval tools enforce
  automatically before returning context.
- **NL-to-scope resolution (P1)** — `kern_meta` passes the raw request string
  as the `task`; nothing yet parses free-form requests into a structured
  `TaskScope` (`paths`/`denied_paths`/...).
- **Symbol-level policies (P1)** — policy enforcement is path-scoped via
  `TaskScope.CheckPath`; per-symbol allow/deny rules are not implemented.
- **Remote agent identity verification (P2)** — agent resolution is local
  (`identity.GetAgent` over the in-process registry); there is no remote
  identity attestation.