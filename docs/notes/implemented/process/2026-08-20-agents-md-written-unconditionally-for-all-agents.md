# Agent Note: AGENTS.md Written Unconditionally for All Agents
Status: implemented

## Problem
setup.Wire() wrote AGENTS.md only inside an opencode gate, so Claude/Codex/etc.-only installs
silently skipped it.

## Decision
AGENTS.md is written unconditionally (universal instruction file) for all 12 detected agents;
integration depth is capability-driven (AGENTS.md universal, MCP universal, hooks capability-gated).

## Consequence
Agent neutrality: no agent gets extras; hooks mirror only where the platform API allows.
