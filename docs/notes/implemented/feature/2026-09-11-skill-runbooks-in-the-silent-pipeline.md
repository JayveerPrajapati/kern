# Agent Note: Skill Runbooks in the Silent Pipeline
Status: implemented

## Problem
The T1-1 skill tool exposed skills to the model, but the silent pipeline could not include a skill runbook in the delivered context.

## Decision
- `kern_orchestrate --with-skill <name>` (CLI), `kern_orchestrate skill=` (MCP), `--skill` on the plugin tool: after the envelope render, the bundled skill's SKILL.md is appended as a `## Skill:` section to both the hashed render (content hash + handle cover it, deterministically) and the deliverable `FittedText`.
- Unknown skill names fail loud (`orchestrate: unknown skill ... (available: ...)`).

## Consequence
- The model receives the repo's own operating procedure for the task class inside the envelope; the content hash proves the runbook was part of the delivered context.
