# Agent Note: Call Edges Reference Qualified Callee Names
Status: implemented

## Problem
Cross-package call edges point to qualified names (db.Do) while graph node IDs are bare symbols
(Do); resolvers dropped unresolvable callees.

## Decision
Traversals operate on raw edge endpoints, mapping endpoints to nodes only for directory matching
(rules.go crossesBoundary BFS + resolveEdgeID). Intel queries intentionally drop unresolvable
callees.

## Consequence
Any code walking call edges must not resolve node IDs before traversal.
