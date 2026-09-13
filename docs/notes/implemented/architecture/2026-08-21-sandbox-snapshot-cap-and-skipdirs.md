# Agent Note: Sandbox Snapshot Cap and SkipDirs
Status: implemented

## Problem
Sandbox snapshot capped at 100 MiB; large generated dirs hitting the cap skipped go.mod/source and
broke the closed loop at verify.

## Decision
maxSnapshotBytes=100MiB, configurable via KERN_SANDBOX_MAX_SNAPSHOT_BYTES; SkipDirs must include all
large generated dirs (.git/.kern/bin/graphify-out/...).

## Consequence
Any new large generated dir must be added to SkipDirs or the loop regresses.
