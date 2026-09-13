# Agent Note: MCP Server Security Model
Status: implemented

## Problem
The MCP server exposes ~14 tools with arbitrary root/dir args and a loopback client; confinement
discipline was unclear.

## Decision
withinRoot/rootedPath confine most tools; ~14 tools accept an arbitrary root by design — the
loopback client is the trusted principal. kern_doc_fetch name escapes sanitized (sanitizeDocName);
sandbox skip-map fixed data-loss; stdio graceful shutdown via signal.NotifyContext + CancelAll/Close
+ Inflight drain.

## Consequence
Closed the known open security/shutdown bugs as of 2026-08-19; the confinement model is: host-side
trust boundary, not per-tool enforcement.
