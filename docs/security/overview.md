# Security and Governance Overview

kern is designed to give agents local code intelligence without giving up
operator control. This page is the user-facing summary; the detailed model is
[`SECURITY.md`](../../SECURITY.md) and the
[threat model](threat-model.md).

## Defaults

- **No telemetry.** Indexes, audit records, memory, and metrics stay on the machine.
- **No automatic machine-wide wiring.** The installer only downloads and verifies binaries. Agent wiring requires `--wire` (PowerShell `-Wire`), `KERN_AUTO_WIRE=1`, or an explicit `kern setup` command.
- **Workspace confinement.** Tool operations are limited to configured workspace roots (`KERN_MCP_ROOTS`).
- **Fail-closed external fetch.** `kern_doc action=fetch` requires an operator-approved domain allowlist.
- **Risk-assessed execution.** LOW/MEDIUM host commands run audited; HIGH/CRITICAL are approval-gated. `KERN_EXEC_POSTURE=closed` restores blanket fail-closed execution.
- **Secret and PII masking.** AWS keys, tokens, credentials, and other sensitive patterns are sanitized before prompts or logs.

## Autonomous interception

When an agent is wired, naive tool calls are redirected to compact, governed
kern tools. Git commit hooks can run staged security and architecture checks,
and post-commit hooks can record the resulting diff into project memory.

```bash
kern setup --dry-run    # preview every file and hook before writing
kern setup --global     # explicitly opt into machine-wide agent wiring
```

## Emergency break-glass

Emergency overrides are intentionally explicit and audited:

```bash
KERN_BYPASS=1 KERN_BYPASS_REASON="P0 hotfix" git commit -m "fix: emergency db failover"
```

Use this only for a production incident. Every bypass writes an audit record
with its reason.

## Health

`kern doctor` reports:

- installation and binary health,
- agent wiring status,
- index freshness and precision tier,
- plugin and catalog drift,
- dropped audit entries,
- cache health.
