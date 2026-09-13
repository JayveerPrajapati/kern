# Agent Note: Web Console Prebuilds the Index Once
Status: implemented

## Problem
kern-server re-ran index.Build per request: 30-90s latency, 2GB+ memory, SDK timeouts.

## Decision
web.New builds index/graph/engines once at startup; handlers reuse prebuilt constructors
(incident.NewEngineWithGraph, verification.NewEngineWithIndex,
architecture.ValidateProjectWithIndex); arch endpoint uses a 5s TTL cache. Graph/index are read-only
after New.

## Consequence
Per-request reindex banned; any new handler needing the index must use the shared engines.
