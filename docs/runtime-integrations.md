# Runtime integrations (production intelligence)

kern can fold **runtime telemetry** — metrics, logs, traces, deployments —
into its code intelligence: incident correlation (`kern_runtime` correlate),
route drift (code-declared routes vs production routes), review overlays,
and modernization planning. All of it is opt-in: with no source configured,
every consumer treats runtime as an absent dimension, never a failure.

This page is the configuration reference. Resolution is shared by the CLI
and the platform (`runtime.LoadSource`), so behavior is identical across
surfaces.

## Source resolution (first hit wins)

| Order | Source | Environment variable | `.kern/config.json` key |
|---|---|---|---|
| 1 | live Prometheus poller | `KERN_PROMETHEUS_URL` | `runtime.prometheus_url` |
| 2 | live OpenTelemetry poller | `KERN_OTEL_URL` | `runtime.otel_url` |
| 3 | live Kubernetes API poller | `KERN_K8S_API` | `runtime.k8s.api` |
| 4 | local JSON snapshot | — | `<root>/.kern/runtime.json` |

- The Kubernetes poller also honors `KERN_K8S_NAMESPACE`
  (or `runtime.k8s.namespace`; empty = `default`).
- The poll interval is `KERN_POLL_INTERVAL` (or `runtime.poll_interval`;
  a Go duration string, default `30s`). Invalid values fall back to the
  default.
- URLs must be `http`/`https` with a host. A configured URL that fails
  validation wires **nothing** (no broken background poller) — `kern doctor`
  reports the invalid configuration with the reason.

## Credentials are environment-only

| Source | Credential env var |
|---|---|
| Prometheus | `KERN_PROMETHEUS_TOKEN` |
| OpenTelemetry | `KERN_OTEL_TOKEN` |
| Kubernetes | `KERN_K8S_TOKEN` |

A non-empty token is sent as a `Bearer` Authorization header on every poll.
The token keys are deliberately **not** available as `.kern/config.json`
keys: credentials cannot be written into a config file, so a repo's config
never becomes a place secrets live. Export them in your shell or service
manager instead.

## What doctor reports (and what it does not)

`kern doctor`'s `runtime` check is **offline by design**:

- **No source** → `warn` with the enable hint (this page).
- **Configured but invalid URL** → `warn` naming the URL and the reason.
- **Live source** → `ok` describing the *wiring* — kind, endpoint, origin
  (env/config), poll interval, credential presence (the env var **name**,
  never its value). Doctor constructs no poller and contacts nothing; live
  telemetry comes from the consumers that actually poll.
- **Local snapshot** → the snapshot is read (a local file) and its telemetry
  counts reported: events, errors, deployments, services.

## Local JSON snapshot

`.kern/runtime.json` is a self-contained snapshot — useful for offline
analysis, tests, or exporting from a monitoring tool:

```json
{
  "events": [
    {"id": "e1", "type": "error", "service": "checkout",
     "severity": "error", "message": "nil pointer",
     "timestamp": "2026-10-20T12:00:00Z"}
  ]
}
```

Live adapters parse the same shapes from their endpoints: Prometheus
text-exposition (`service="..."` label), OTLP-style JSON
(`logs`/`traces`/`metrics`), and Kubernetes API `items` documents.

## Surfacing telemetry

The MCP tool `kern_runtime` is the live surface (`status`, `drift`, `routes`,
`events`, `correlate`). Its `status` action reports `fetch_errors` — the
transport-failure count for live pollers — so an unreachable or unauthorized
endpoint is distinguishable from a reachable one with no data; a configured
endpoint failing URL validation reports `configured_but_invalid` instead of
a bare "not wired".

## Security notes

- Pollers only fetch from the explicitly configured endpoint; nothing is
  auto-discovered.
- Credentials are read from the environment at wiring time and never
  persisted, logged, or written into any kern store.
- Responses are capped (10 MiB) per fetch.
- Everything here is local-first: telemetry flows from your endpoints to
  your machine — no third-party relay, ever (ADR-0009).
