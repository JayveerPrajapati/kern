# Agent Note: Enterprise Control Plane
Status: implemented

## Problem
Multi-project/org visibility was absent: memory, tasks, graph search, and agent governance were per-
project.

## Decision
internal/enterprise: shared org memory, task aggregation, multi-repo graph search, agent
registration; opt-in via enterprise.New + KERN_AUTH_TOKEN; local mode unaffected.

## Consequence
Enterprise is additive; single-project behavior unchanged.
