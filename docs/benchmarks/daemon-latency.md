# Daemon Warm-Path Latency (and the Sandbox Artifact)

**Date:** 2026-11-10
**Revision:** `c01bc5c0` (`git rev-parse HEAD` at measurement time)
**Suite:** kern-bench (`kern bench`) · **Status:** [measured] — host numbers re-measured 2026-11-10; the sandbox-vs-host gap is the finding this page exists to record

## 1. Purpose

A 2026-11-10 head-to-head against a peer tool (densic 0.2.0) measured kern CLI
latency at 1.2–2.3 s per call versus 0.19–0.29 s — a 6–9× gap. This page
records what that gap actually was: **a sandbox artifact, not a product
property.** The same commands, run on the host, answer in 26–190 ms.

## 2. Methodology

Two environments, same binary, same repo (kern itself, 17,798 symbols,
21.5 MB SQLite store, darwin/arm64 go1.27.1, 8 cpus):

- **Host:** `kern bench --root <repo>` (the deterministic zero-network
  harness; cold/warm load + fixed query set + daemon-backed CLI legs).
  Regenerate with `kern bench --root <repo> --mode daemon --json`.
- **Sandbox:** the same `kern search/explore/impact` invocations timed inside
  an isolated execution sandbox (seatbelt, network egress blocked, own
  temp cwd; wall-clock ms around each invocation).

The sandbox cannot use the host daemon: the daemon socket name embeds the
binary key, so a sandboxed client resolves a *different* socket path than
the running host daemon, cannot connect, and falls back to the in-process
path — which then pays roughly cold-path cost (~1.0–1.2 s on this repo).

## 3. Measured results (host, revision c01bc5c0)

| path | median | min | runs |
|---|---|---|---|
| cold build (fresh) | 1017.02 ms | 1017.02 ms | 1 |
| warm load (store) | 35.87 ms | 29.27 ms | 3 |
| cli: search (fresh process) | 69.20 ms | 57.89 ms | 3 |
| cli: context (fresh process) | 26.28 ms | 25.70 ms | 3 |
| cli: explore (fresh process) | 188.50 ms | 180.90 ms | 3 |
| cli: impact (fresh process) | 77.66 ms | 77.29 ms | 3 |
| cli: impact-hub (fresh process) | 146.73 ms | 138.79 ms | 3 |

In-memory queries after hydration: symbol search 0.74 ms, ranked search
12.5 ms, one-hop callers 42 ns, transitive blast radius 6.4 ms, hub ranking
71.9 ms.

## 4. The sandbox artifact (measured, same day)

| command | host (daemon) | sandbox (fallback) |
|---|---|---|
| kern search (repeat) | 69–108 ms | 1020–1249 ms |
| kern explore | 189 ms | 1423 ms |
| kern impact | 78–147 ms | 2108 ms |

## 5. Conclusions

1. The host warm path (daemon-backed, fresh process) is 26–190 ms — the
   peer-comparison target (< 300 ms) is met with margin; `kern search`
   measured *faster* than the peer's 190 ms on this repo.
2. Latency comparisons between tools MUST run in the same environment:
   sandboxed-vs-host comparisons overstate a daemon-backed CLI by ~10×.
3. The daemon (`kern daemon status`) auto-starts, is per-root, and its
   socket embeds the binary key — a rebuilt binary deliberately never talks
   to an old daemon (identity-mismatch → stop + restart, daemon_proxy.go).

## 6. Regeneration

```bash
kern bench --root <repo> --mode daemon --json
kern daemon status --root <repo>
```
