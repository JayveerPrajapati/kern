# Skills

Kern ships workflow runbooks that agents can follow through the tool surface
(`kern_skill`) and the silent pipeline (`kern_orchestrate --with-skill`).

## Bundled skills

Four skills are embedded in the binary and installed by `kern skills install`:

| Skill | Purpose |
|---|---|
| `kern-investigate` | Symbol/architecture investigation using the prebuilt index |
| `kern-safe-change` | Pre-edit blast radius, firewall gates, auto-repair workflow |
| `kern-incident-triage` | Log compression → stack-to-symbol correlation → reproduction |
| `kern-team-orchestration` | Orchestrate kern's 7-role specialist agent squad across the Explore, Plan, Edit, and Verify lifecycle. |

- `kern skills list` — catalog with descriptions.
- `kern skills show <name>` — full runbook.
- `kern_skill` MCP tool — `action=catalog` (list) or `action=load` (runbook body).
- `kern orchestrate "<task>" --with-skill kern-safe-change` — appends the
  runbook to the delivered context envelope (content-hash covered).

## User skills

Declare your own skills under `<root>/.kern/skills/<name>/SKILL.md` (or the
XDG user dir). A user skill is a SKILL.md with YAML frontmatter:

```markdown
---
name: deploy-checks
description: Run the pre-deploy verification checklist.
---

## Problem
...
```

User skills are resolved by `kern_skill action=load` (fallback after the
bundled set) and by `kern skills show`. The same format gates the bundled
set: frontmatter description, prose that states the procedure.

## Silent mode

`KERN_SILENT=1` strips the visible `[kern]` markers from plugin output and
injected context; default (unset) keeps the transparent markers.