# Agent Note: kern meta Routes for Notes
Status: implemented

## Problem
The kern_meta NL router had no route to the decision-record system; note queries fell through to symbol search.

## Decision
- classifyRetrievalTools routes "validate notes" / "notes … valid" phrases to kern_note action=validate and "list … notes" / "note inventory" to action=list; the meta dispatch switch gained the kern_note case.
- Deliberately NOT routed: kern_agent_message / kern_agent_interrupt / kern_mcp_call — they require structured args the NL router cannot reliably derive; the model calls them directly.

## Consequence
- Note validation and inventory are reachable in natural language; steering tools stay explicit-arg.
