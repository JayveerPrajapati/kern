# Governance troubleshooting

kern's guard is intentional: it routes code-reading, search, and execution intents
through deterministic, token-efficient tools instead of raw shell commands. This page
maps the most common blocked operations to the supported path.

## Common blocked actions

| Blocked intent | Use instead |
|---|---|
| Read a code file | `kern_explore` (symbol or file), `kern_compact_file`, or `kern_context` |
| Regex/grep a codebase | `kern_search`; use `kern_ast_search` for AST patterns |
| List or explore a repository | `kern_buddy` or `kern_project_map` |
| Understand a symbol and its callers | `kern_explore`, then `kern_impact` before editing |
| Run a build, test, or lint command | `kern_verify command="go test ./..."` |
| Search project docs | `kern_doc action="search" query="..."` |
| Fetch a public documentation page | `kern_doc action="fetch" url="..."`, then search the local index |
| Run arbitrary code | `kern_exec`; execution fails closed until enabled below |
| Inspect a truncated kern result | `kern_fetch_raw_anchor` |
| Review changed files | `kern_review`, then `kern_verify` |

Simple shell commands such as `pwd`, `ls`, `which`, `echo`, and read-only Git
status/log operations are allowed to pass through. Content-emitting commands such
as `cat`, `sed`, `head`, and `grep` are blocked because they bypass the symbol
index and can dump unnecessary source into the model context.

## Host guard contract

| Host | Integration | Config path | Guarded aliases |
|---|---|---|---|
| Claude Code (`claude`) | PreToolUse hook | `~/.claude/settings.json` | `Read` · `Grep` · `Glob` · `Bash` |
| Cursor (`cursor`) | preToolUse hook | `~/.cursor/hooks.json` | `Read` · `Grep` · `Glob` · `Bash` |
| Copilot (`copilot`) | preToolUse hook | `~/.copilot/hooks/kern-pretooluse.json` | `Read` · `Grep` · `Glob` · `Bash` |
| Gemini (`gemini`) | BeforeTool hook | `~/.gemini/settings.json` | `read_file` · `grep` · `glob` · `list_files` · `run_shell_command` |
| Antigravity (`antigravity`) | PreToolUse hook | `~/.gemini/config/hooks.json` (or `<repo>/.agents/hooks.json`) | `view_file` · `grep_search` · `find_by_name` · `run_command` |
| Qwen (`qwen`) | PreToolUse hook | `~/.qwen/settings.json` | `Read` · `Grep` · `Glob` · `Bash` |
| Qoder (`qoder`) | PreToolUse hook | `~/.qoder/settings.json` | `Read` · `Grep` · `Glob` · `Bash` |
| Codex (`codex`) | PreToolUse hook | `~/.codex/hooks.json` | `Read` · `Grep` · `Glob` · `Bash` |
| Continue (`continue`) | PreToolUse hook | `~/.continue/settings.json or ~/.config/continue/settings.json` | `Read` · `Grep` · `Glob` · `Bash` |
| Kiro (`kiro`) | PreToolUse hooks | `~/.kiro/hooks/kern-guard.json` | `read` (read/search/list) · `shell` (execution) |
| opencode (`opencode`) | plugin shadow tools | `<repo>/.opencode/plugins/kern.ts` (global copy under the opencode config directory) | read · grep · glob · bash |

All hook hosts send either `{"tool_name":"...","tool_input":{...}}` or, on
Antigravity, `{"toolCall":{"name":"...","args":{...}}}`. The guard maps every
alias to the same four families: read, search, glob/list, and shell execution.
`kern doctor` reports global guard freshness, and `kern setup --check` reports
each host's hook config and matcher completeness. If a host reports a missing
or partial matcher, run:

```bash
kern setup --global
```

## Execution gates

`kern_exec` is fail-closed by default. Enable it only for the host/process that
needs it:

```bash
KERN_ALLOW_EXEC=1            # enable governed isolated execution
KERN_TOOLS=kern_exec,kern_run
```

### What counts as destructive

Both classifiers — the shell guard (`kern guard classify`, enforced by the host
hook and the plugin) and the governed-exec gate (`kern_exec`, `kern build`) —
share one destructive list, so routing a command through either surface cannot
bypass it. `kern guard parity` and `kern verify claims` fail if they ever
disagree. Gated forms include:

| Family | Gated | Stays governed (not gated) |
|---|---|---|
| Files | `rm -rf`, `rm -r … -f`, `find … -delete`/`-exec rm`, `dd of=/dev/…`, `mkfs*`, `chmod -R /` | `rm build/output`, in-project `mv`/`cp` |
| Writes by side effect | `echo x > file`, `: > file`, `>> file`, `truncate`, `shred` | `> /dev/null`, `2>/dev/null`, `2>&1` |
| Home/system state | `mv ~ …`, `mv /etc/…`, `mv /Users/…` | in-project moves |
| Remote history | `git push -f`, `--force`, `--force-with-lease`, `--mirror`, `origin +main` | `git push`, `git push origin main` |
| Local work | `git checkout -- .`, `git restore .`, `git clean -fdx` | `git checkout -- <file>`, `git restore --staged .` |
| Cluster/cloud/data | `kubectl delete namespace\|pv\|pvc\|secret`, `delete -f`, `--all`; `terraform destroy`; `aws s3 rm --recursive`; `docker … prune`, `compose down -v`; `psql -c 'drop …'` | `kubectl get`, `docker ps`, `psql -c 'select …'` |
| Network | `curl … \| sh`, plain `curl`/`wget` fetch, installers | loopback-only fetch with `KERN_ALLOW_LOOPBACK=1` |

If a command is denied by the approval workflow, the denial includes an approval
ID. Resolve it without changing the command (a different command creates a new
approval):

```bash
kern approve <approval-id> --approver <you> --reason <why>
kern approval wait <approval-id>
```

## Network isolation policy

Script execution (`kern_exec`, sandbox runs) is network-isolated wherever the
platform allows it, and **fails closed where it does not** — an unavailable
isolator is never silently downgraded to a plain subprocess. The policy has
exactly two modes, both operator-controlled:

| Mode | Meaning | How |
|---|---|---|
| `required` (default) | no isolator → the run is denied, pre-execution | nothing to set |
| `allow-with-audit` | explicit operator override; the unisolated run proceeds and the exec-gate outcome is recorded in the governance audit chain | `KERN_ALLOW_UNISOLATED=1` (alias `KERN_ALLOW_NET=1`) |

There is deliberately no `disabled` mode and no per-project config toggle:
the escape is a host environment variable, so it is visible in the process
environment and auditable, and it never persists in a repo file that could
ship with the code.

Platform behavior (`kern doctor` reports the live state as
`network-isolation`):

| Platform | Isolator | Behavior |
|---|---|---|
| Linux | `unshare --user --map-root-user --net` | full egress block for script runs |
| macOS | Apple Seatbelt (`/usr/bin/sandbox-exec`) | network-denying seatbelt profile |
| Windows | none (no unprivileged isolation primitive) | scripts fail closed; only the audited override above permits unisolated execution |

When doctor reports `network isolation: unavailable`, every governed script
run on that host is already being denied — the override above is the
documented, deliberate choice to accept unisolated execution (for example
inside an already-locked-down container or CI runner where the boundary is
external to kern). Docker-based alternatives need no kern configuration:
run the whole `kern` invocation inside the container and the same
governance applies to it.

## Path confinement

MCP path arguments are confined to the roots configured when the server starts.
If a tool reports that a path is outside its allowed roots, start the server with:

```bash
KERN_MCP_ROOTS=/path/one,/path/two kern mcp
```

This setting cannot be widened by a tool call. That is deliberate: a tool result
must never be able to expand its own authority.

## Break-glass

For a genuine emergency, an operator may disable enforcement for one host process:

```bash
KERN_BYPASS=1 KERN_BYPASS_REASON="P0 production hotfix" <command>
# equivalent:
KERN_ENFORCE=0 <command>
```

Use the narrowest possible scope, record the reason, and re-enable enforcement
immediately. Hook bypasses are recorded under `~/.kern/audit/bypass.jsonl`;
firewall gates write their governed audit records under `.kern/audit/`.

## Audit chain growth policy

kern's governance audit chain (`.kern/audit/chain.jsonl`) is append-only by
design: each record's keyed hash covers its predecessor, so the full-chain
replay during verification is itself the tamper-evidence. kern therefore
never truncates, rotates, or compacts the chain — and there are no retention
knobs to configure, on purpose ([ADR-0013](adr/0013-audit-chain-append-only.md)).

- `kern doctor` reports the chain size on every run and warns once it passes
  50 MB.
- Chain growth is proportional to governed activity (approvals, firewall
  decisions, exec-gate outcomes, audited bypasses) and stays local to the
  project.

To archive a chain that has grown too large (operator action, never
automatic):

1. Move the file aside while no kern process is appending:
   `mv .kern/audit/chain.jsonl ~/archives/kern-chain-<date>.jsonl`
2. The next governed write starts a fresh chain automatically. Nothing is
   deleted — the moved file is your audit history.
3. Re-verify an archive at any time by placing it under a scratch
   `.kern/audit/` directory and running `kern audit --root <scratch-dir>`.

## Related

- [Tool selection guide](tool-selection.md)
- [Configuration reference](configuration.md)
- [Privacy and security specification](privacy.md)
- [ADR-0013: audit chain is append-only](adr/0013-audit-chain-append-only.md)
