# Telemetry Audit — zero network egress in core packages

**Status:** enforced by CI (`go test ./internal/architecture`)
**Date:** 2026-09-23
**Git HEAD:** `3866e5a92d167a32be538e33f115bdc15253016c`

## Purpose

kern's marketing posture is "Zero telemetry. It stays local." This document
and the test that gates it turn that promise into a machine-checkable claim:
a static import scan that **fails the build** if any core (non-network-by-
design) internal package gains network egress or telemetry capability.

The point is not to police intent — it is to catch *drift*: the day someone
adds an `http.Client` to the indexer or a `net.Dial` to the memory layer to
"phone home" a metric, this test fails with the exact file, line, and import.
The allowlist is the explicit, reviewed surface of what is allowed to touch
the network, and nothing else may.

## Method

`TestNoNetworkInCorePackages` in `internal/architecture/telemetry_audit_test.go`:

1. Walks every **non-test** `.go` file under `internal/` (recursively),
   skipping `_test.go` files, vendored paths, `.kern` trees (the sandbox
   loop is a separate module), and **subtree roots on the allowlist**
   (allowlisting `internal/llm` exempts `internal/llm/**`).
2. Parses each file with `go/parser`. Files importing `net` or `os/exec` are
   re-parsed in full so the check operates on the **AST** (comments and string
   literals can't trip it) rather than raw text.
3. Fails on any of:
   - **`net/http`** — HTTP client *or* server capability; core packages have
     no business with either.
   - **`net`** when the file uses egress-capable symbols — `net.Dial*`,
     `net.Listen*`, `net.Conn`, `net.Listener`, `net.Lookup*`, `net.Resolver`,
     `net.Interface*`, etc. Parsing-only helpers (`net.ParseIP`,
     `net.SplitHostPort`, `net.JoinHostPort`, `net.ParseCIDR`, `net.IP`,
     `net.IPNet`) are allowed, same spirit as the `net/url` carve-out: pure
     parsing is not telemetry.
   - **`net/smtp`, `net/rpc`, `net/textproto`** — stdlib `net/*` subprotocols
     that egress. `net/url` and `net/mail` are pure parsing and remain allowed.
   - **Third-party dependencies whose import path smells like a network
     client** (`http`, `grpc`, `websocket`, `mqtt`, `smtp`, `tls`, `client`,
     ... tokens). None exist in `go.mod` today; this is a defensive net for
     future dependencies.
   - **`os/exec` + curl/wget** — `exec.Command` / `CommandContext` /
     `LookPath` calls that pass a string argument naming `curl` or `wget`
     (either as the binary or inside a `sh -c "curl ..."` string).

The failure message names the file, the line, the offending import/symbol,
and points at this document for the allowlist procedure.

## Scope

- **In scope:** every non-test `.go` file under `internal/`.
- **Out of scope:** `.kern/sandboxes/loop/` (separate Go module), vendored
  trees, `_test.go` files (test-only network fixtures are a deliberate
  exception; the audit is about shipped capability).

## Allowlist (network-by-design packages)

Each entry is a **subtree root** and must be justified. Adding an entry is a
reviewed act, not a reflex — the audit must never be weakened to make a test
pass.

| Package | Why it's exempt |
| --- | --- |
| `internal/enterprise` | Enterprise auth + org API (outbound and serving). |
| `internal/fetch` | Outbound HTTP fetcher for pages/documents. |
| `internal/llm` | Outbound LLM provider clients (OpenAI/Anthropic/Google/Ollama). |
| `internal/mcp` | MCP loopback HTTP server (covers `internal/mcp/transport` TLS loopback listener and `internal/mcp/doc`, which uses `internal/fetch`). |
| `internal/mcpclient` | Outbound MCP client. |
| `internal/orgapprovals` | Org approvals REST surface (HTTP serving, mounted on the enterprise org API). |
| `internal/prprovider` | PR-provider API clients (GitHub). |
| `internal/relay` | Peer relay networking. |
| `internal/resilience` | Chaos/injection scenarios spin up **local loopback** HTTP servers to simulate network failures; no outbound egress. |
| `internal/runtime` | `live.go`: `LivePrometheusSource` / `LiveOtelSource` / `LiveKubernetesSource` poll remote endpoints. |
| `internal/sdk` | Outbound kern-server REST client. |
| `internal/web` | Local HTTP server (dashboard). |
| `internal/webhook` | Outbound delivery of eventbus events to registered webhook URLs. |

Verified non-allowlisted neighbors (checked, found network-free, no entry
needed): `internal/deployment` (runs local shell commands only),
`internal/doctor` (local health checks only). Core packages such as `pii`,
`optimize`, `script`, and `governance` import `net` **only** for parsing
helpers (`net.ParseIP`, `net.SplitHostPort`) — the audit explicitly allows
that and would catch any future egress-capable `net.*` symbol in them.
`internal/script` uses `os/exec` to run local sandboxed runtimes and its
`curl`/`wget` strings are a **deny-list** that blocks those tools under a
fail-closed `unshare --net` isolation — not an egress path.

## Limitations

- **Build tags are ignored.** Every `.go` file on disk is parsed regardless
  of its build constraints. This is deliberately fail-closed: a network
  import hidden behind a build tag is still caught.
- **`os/exec` with dynamically-built binaries.** A binary name assembled at
  runtime (`var bin = "curl"`) cannot be proven statically. This is covered
  separately by the runtime fail-closed gates: unprivileged network
  namespaces (`unshare --user --map-root-user --net`) refuse to run rather
  than degrade, and `KERN_ALLOW_UNISOLATED=1` / `KERN_ALLOW_NET=1` overrides
  are explicit operator opt-ins, never defaults.
- **Local shadowing.** An identifier shadowing the `net`/`exec` package name
  could in theory dodge the symbol checks. Accepted for a static audit.
- **Localhost-only binding is not telemetry.** The audit allowlists serving
  packages (web, mcp) and loopback-only chaos simulators (resilience); a
  core package binding a port would still fail via `net.Listen`/`net/http`.

## How to reproduce

```sh
# Single command; fails with file:line + offending import on any violation.
go test ./internal/architecture -run TestNoNetworkInCorePackages -count=1

# Full architecture-package suite (parity, doc drift, hygiene, this audit).
go test ./internal/architecture -count=1
```

## Policy

- **Never weaken the audit to make it pass.** If the test flags something,
  either it is a genuine, previously-undetected egress capability (report it
  as a finding — do not allowlist silently) or the package's network use is
  clearly by design (then allowlist with a one-line justification here).
- This document and the allowlist in the test file must stay in sync; a
  mismatch between them is itself a review signal.