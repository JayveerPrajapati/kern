# Agent Note: kern meta Skill Load Routing
Status: implemented

## Problem
Skill queries routed through a legacy kern_skills path (not in the MCP catalog), and load-intent phrases had no route.

## Decision
- classifySkillTools runs before the workflow router: runbook/playbook queries, literal skill names, "incident triage", and "safe change" with explicit skill language route to kern_skill load; generic skill queries route to catalog.
- kern_skill load gained a user-skill fallback (<root>/.kern/skills), making it the complete skill face; the legacy kern_skills route was retired (handleSkills stays for direct tests).
- Deliberately un-routed: bare "make a safe change" -> kern_impact and "triage this incident" -> kern_incident (better answers than the runbook).

## Consequence
- Explicit skill asks resolve to the runbook; semantic workflow asks keep their better routes.
