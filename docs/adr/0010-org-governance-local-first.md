# ADR-0010: Org Governance — Local-First Multi-Repo, No SSO

## Status

Accepted (2026-09-26, consolidating the P13 stage 1–3 work that landed in
code comments without a recorded decision)

## Context

A 2026-09-22 external audit flagged org-level governance as the biggest
strategic gap: "no SSO/RBAC/central policy — enterprise mode is single-node
+ token; per-repo approval files mean no org-wide AI governance yet." The
P13 stages were designed and shipped in code (`internal/enterprise`,
`internal/orgapprovals`, `internal/mcp/org`, `internal/mcp/rbac`,
`internal/governance`) but the design intent lived in stage comments —
`P13 stage 1/2/3` — not in an ADR, so the audit (and any new contributor)
could not see what exists, what is deliberately missing, and why.

This ADR records the actual capability map (re-verified 2026-09-26,
file:line evidence in the audit trail) and the decisions about what org
governance in kern is — and is not.

## Decision

### Org governance is local-first multi-repo, file-backed, and org-rooted

- **The org root is the trust anchor.** `KERN_ORG_ROOT` designates one
  directory whose `.kern/` holds the org's authoritative state:
  `org-policy.json` (versioned, SHA-256-hashed),
  `org-approvals.json`, `org-rbac.json`, the org agent/user registries,
  and the org audit log. An org is a filesystem tree, not a service.
- **Central policy distribution is in-process and drift-checked.**
  `WriteOrgPolicy`/`ReloadOrgPolicy` push the org policy into the
  enterprise server snapshot and every cached `ProjectApp` in-place via
  the `ProjectApp.SetPolicies` seam; new apps seed from the snapshot;
  `PolicyDrift` detects out-of-band edits. What we deliberately do NOT
  have: cross-node sync, git-based distribution, inheritance/includes,
  or per-repo policy-hash pinning. A single enterprise server over one
  org root is the deployment model; anything wider is future work
  requiring its own ADR.
- **Org approvals are org-wide, single-use, and atomic.**
  `internal/orgapprovals` pre-approves an action/resource scope across
  every project under the org root; consumption is flock-guarded so two
  concurrent deploys cannot double-spend. The deploy gate composes
  per-project firewall first, org approval as override.
- **RBAC is enforced, not advisory — at two layers.** The MCP dispatch
  funnel runs `rbac.CheckAgentTool` for every tool call (org-wins role
  resolution: `org-rbac.json` → project `rbac.json` → unassigned), and
  since 2026-09-26 every `kern_org_*` tool body additionally gates each
  action against the org-role model (mutations → org-admin; reads →
  org-member; new actions fail closed until explicitly granted in
  `orgAdminActions`).
- **Org mode fails closed on untrusted identity.** `agent_id` is
  client-asserted, so enterprise refuses to start org mode unless
  `KERN_RBAC_DEFAULT_DENY=1` (escape hatch `KERN_ORG_ALLOW_WEAK_RBAC=1`
  is documented-unsafe). This is the honest posture for a tool whose
  principals are agents on a loopback trust boundary.

### There is no SSO — deliberately

Transport auth is a single shared static bearer token (`KERN_AUTH_TOKEN`,
constant-time compare). There is no OIDC/SAML/LDAP, no external IdP, no
per-user transport identity. Reasons:

1. **The wedge.** kern is one binary, zero network, zero telemetry. An
   IdP integration is a network dependency, a vendor surface, and an
   availability coupling that contradicts ADR-0009's local-first
   principle for the same class of feature.
2. **The trust model is agents, not browsers.** The principals are AI
   agents calling MCP/REST with client-asserted identities; the
   enforcement point is the dispatch + org-action RBAC layers, not
   session establishment.
3. **Single-node is the honest scope.** Multi-node org mode with
   centralized identity is an enterprise deployment problem that should
   be solved when there is a real deployment asking for it, via an
   adapter seam (see Consequences).

If org SSO becomes necessary, the seam is an identity-provider adapter
(the `internal/runtime` `Source` interface pattern): a local
file/token-provider ships by default, and an OIDC adapter can be added
behind an interface without rewriting the RBAC layers — but nothing
ships today.

### Known role-resolution wrinkle (recorded, not fixed)

Org roles (`org-admin`, `org-member`) and tool-taxonomy roles
(`admin`, `developer`, …) are separate vocabularies. The dispatch-layer
check resolves the agent via org-rbac.json first, and `org-admin` is not
a taxonomy role — so an actor holding only an org role is denied at
dispatch ("assigned unknown role") before reaching the org-action layer
where they would be authorized. The working union today: an org-admin
who needs tool access must also carry a taxonomy role in the project
rbac.json. Unifying the two vocabularies (or teaching dispatch to treat
org-admin as tool-admin) is an open decision.

## Consequences

- Org governance works today for the documented deployment: one machine,
  one org root, one enterprise server, agents authenticated by token +
  asserted identity, RBAC enforced per action.
- The 2026-09-22 audit gap is narrowed to what this ADR names:
  single-node policy propagation (no cross-repo push beyond the org
  root), no SSO (deliberate), and the dual-vocabulary wrinkle.
- Adding SSO or multi-node distribution later requires: an
  identity-provider adapter interface (SSO) or a distribution protocol
  (multi-node), each deserving its own ADR — neither is on the roadmap
  by default.
- The org-role cache (`internal/orgapprovals/org_role_cache.go`)
  reconciles cross-process `org-rbac.json` edits by mtime+size, not
  content — accepted as a mild consistency gap for the single-node
  model.
