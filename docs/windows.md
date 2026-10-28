# Windows execution posture

kern's deterministic core (index, search, graph, context, MCP protocol,
confinement, approvals) is OS-invariant. The features that touch the host are
conditional, and Windows is deliberately stricter than macOS/Linux where the
platform has no equivalent isolation primitive.

## Sandbox / exec: fail-closed by default

`internal/script` (the `kern exec` / MCP `kern_exec` path) and
`internal/sandbox` (`kern sandbox`, `kern execute`) refuse to run when network
isolation is unavailable, instead of silently degrading to full network
egress. On a default Windows host this means:

- `kern exec`, `kern sandbox`, `kern execute`, and MCP `kern_exec` return a
  clear error: `network isolation not available on this platform (windows);
  refusing to run unisolated (fail-closed)`.
- The only overrides are the operator's explicit `KERN_ALLOW_UNISOLATED=1`
  (or the legacy alias `KERN_ALLOW_NET=1`). They are env-only by design; an
  agent cannot flip them by touching a file.
- `kern build` is not the sandbox path: it uses `cmd /c` on Windows and is
  governed by the approval firewall (dangerous Windows families require an
  operator approval).

The refusal is pinned by `internal/script/isolation_test.go`
(`TestFailsClosedWithoutNetNS`) and `internal/sandbox/network_test.go`.

## opencode plugin Tier 3

The plugin's pure-read fallback runs `kern exec --readonly`. On Windows that
call cannot be isolated, so the plugin must not claim the read was
"served by kern": a pure read either falls back to a raw read with an honest
one-line note, or surfaces the fail-closed message. Writes and arbitrary exec
remain fail-closed through `kern build` / the governance firewall.

## What is verified where

- macOS/Linux: runtime-tested on a real host.
- Windows: cross-build (`GOOS=windows`, `-tags sqlite,notreesitter`) and
  static/classifier tests, plus the blocking Windows CI legs. Windows runtime
  is **not** release-qualified until a `windows-latest` run passes.
