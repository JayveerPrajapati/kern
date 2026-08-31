# kern Evidence Bundle Schema (v1)

`kern evidence export` produces a single, self-contained, tamper-evident JSON
artifact — the **evidence bundle** — that turns "authorized, evidence-backed
context" into something an enterprise security team can take to a
SOC 2 / ISO 42001 / EU AI Act review.

The bundle is the exportable form of three proofs the product already
produces internally, plus a snapshot of the authorization log (audit chain):

| Pillar | Source | What it proves |
|---|---|---|
| `authorization` | `authz.AuthorizeContext` (re-derived at export) | what an agent was permitted to read for a task, and why |
| `freshness` | `index.Index.FreshnessProof` | the index the authorization was computed over still matches the working tree |
| `lineage` | the authorized scope | which symbols and call edges the authorized context covered |
| `audit_trail` | persisted audit chain (`.kern/audit`) | the tamper-evident authorization log snapshot |

## Wire format

```jsonc
{
  "schema_version": 1,            // must be 1; Verify() rejects anything else
  "bundle_id": "a1b2…-…",         // random RFC-4122 v4 UUID
  "generated_at": "2026-08-31T…Z",
  "repo_root": "/abs/path/to/repo",
  "task_id": "T-42",              // omitempty
  "agent_id": "default",          // omitempty

  "authorization": {
    "proof": {
      "decision": { "decision": "allowed", "allowed": true, "risk": {…},
                    "approval": null, "deny": null, "budget": null },
      "agent":   { "id": "default", "permission_count": 1 },
      "task_scope": { "task_id": "T-42", "paths": […], "denied_paths": […], … },
      "fingerprint": "…",          // sha256 of index identity + decision + allowed symbols
      "index_freshness": "…",
      "index_version": "…",
      "decided_at": "…"
    },
    "scope": {
      "symbols":       [ { "name": "PublicA", "qualified": "public.PublicA",
                           "kind": "func", "file": "public/a.go",
                           "line": 3, "pkg": "public" } ],
      "edges":         [ { "from": "public.PublicA", "to": "public.PublicB" } ],
      "denied":        [ { "symbol": {…}, "stage": "path", "reason": "…" } ],
      "policy_source": "permissive-default"
    },
    "reconstructed": true          // proof was re-derived at export time
  },

  "freshness": {
    "proof": {
      "verdict": "fresh",          // fresh | stale | unknown (unknown ⇒ fail closed)
      "recorded": { "tree_oid": "…", "content_root": "…", "git_commit": "…", "built_at": "…" },
      "current":  { "tree_oid": "…", "content_root": "…", "git_commit": "…", "built_at": "…" },
      "checked_at": "…"
    },
    "index_version": "…"
  },

  "lineage": {
    "symbols": [ { "name": "PublicA", "qualified": "public.PublicA",
                   "file": "public/a.go", "line": 3 } ],
    "edges":   [ { "caller": "public.PublicA", "callee": "public.PublicB" } ],
    "task": "T-42"
  },

  "audit_trail": [
    { "id": "audit-1", "timestamp": "…", "agent_id": "…", "action": "read",
      "resource": "…", "result": "allowed", "hash": "…",
      "task_id": "…", "validation_outcome": null }
  ],
  "audit_chain_hash": "…",         // last hash in the audit chain snapshot

  "bundle_hash": "…"               // sha256 of the canonical JSON of everything above
}
```

Field names are lower_snake_case for the bundle itself. Nested proof types
carry the field names of their source structs (`AuthorizationProof`,
`FreshnessProof`, `AuthorizedScope`) — see `internal/governance/authz/types.go`
and `internal/index/identity.go`.

## Tamper-evidence: the hash chain

`bundle_hash` is the SHA-256 of the canonical JSON of the entire bundle with
`bundle_hash` cleared. The bundle contains no maps, so `encoding/json` output
is deterministic — any verifier (this CLI, a future one, a security team's
script) recomputes the same digest over the same content.

`Verify()` checks, in order:

1. `schema_version == 1`;
2. `bundle_hash` is non-empty;
3. recomputed `sha256(canonicalJSON(bundle with bundle_hash="")) == bundle_hash`.

A tampered field, an altered `bundle_hash`, or a version bump all fail the
seal. `kern evidence verify` additionally replays the repo's audit chain
(`<repo_root>/.kern/audit`) with `AuditLog.VerifyChain()` and cross-checks the
bundle's `audit_trail` hashes against the first N on-disk entries, so a
bundle whose claimed authorization log no longer matches the repo is flagged
even if its own seal is intact.

## Known limitations (documented, not fixed here)

1. **SHA-256 hash-chaining, not cryptographic signing.** The bundle is
   tamper-evident but not attacker-proof: anyone who can rewrite the bundle
   can recompute `bundle_hash` (there is no secret). Key-based signing
   (x509 / Ed25519) is a future item.
2. **Authorization is reconstructed at export time.** There is no persisted
   per-call `AuthorizationProof` to fetch, so `kern evidence export` re-runs
   `authz.AuthorizeContext` against the current index. The `reconstructed`
   field on the authorization section records this. A future task should
   persist the proof at decision time so exports reflect the historical
   decision, not a re-derivation.
3. **Lineage reflects the authorized scope at export time**, not a historical
   retrieval log (per-response `Provenance` is in-memory only and not
   persisted). The bundle's `lineage.symbols`/`edges` are the authorized
   scope, which is the correct evidence for *what an agent was permitted to
   read*, but not for *what was actually retrieved in a specific session*.
4. **The audit chain's `computeAuditHash` does not include
   `ValidationOutcome` in its input** — a known gap in the audit chain to be
   fixed in a future task (the chain would need a schema-bumping migration).
   The bundle's **own** hash DOES cover the `validation_outcome` snapshots in
   `audit_trail`, so the exported artifact is not affected by the gap.
5. **`AuditEntrySnapshot` is a flat projection** of `AuditEntry` (the fields
   a reviewer needs), not the full struct. Risk/Approved are not snapshotted;
   extend the snapshot (and bump `schema_version`) if reviewers need them.

## CLI

```
kern evidence export [--root ROOT] [--agent-id ID] [--task T] [--out FILE]
    # --out "-" (default) prints the bundle JSON to stdout
    # exit 0 = ok, 1 = error
kern evidence verify [--file FILE] [--root ROOT]
    # --file defaults to stdin; --root overrides the bundle's repo_root
    # exit 0 = valid, 2 = tampered/broken, 1 = parse/usage error
```

## Versioning

`schema_version` is 1. Any breaking change to the wire format (renamed/
removed fields, new required sections, hash-input changes) must bump it, be
documented here, and be rejected by `Verify()` — which is exactly what the
`TestBundleVerify_SchemaVersion` test pins.