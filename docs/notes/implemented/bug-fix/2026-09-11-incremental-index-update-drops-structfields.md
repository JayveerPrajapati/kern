# Agent Note: Incremental Index Update Drops StructFields
Status: implemented

## Problem
`kern dead` reported `httpClient.roundTrip` (client.go) as certainly dead despite a live field-access caller (`c.http.roundTrip` at client.go:369). Root-caused: schema v12's StructFields (receiver-field callee rewrite) was silently stripped by EVERY incremental index update on a disk-loaded prior — `reconstructFileResult` (update.go) rebuilt the per-file Pkg as {Name,Path,Files,Imports} and never carried prev's package-merged StructFields forward. Watcher/daemon-driven updates progressively emptied the map; the field-access rewrite no-ops; the dead lens re-flags live methods. The equivalence tests never caught it because the fixture tree has no structs with fields.

## Decision
- update.go reconstructFileResult: copy prev.Pkgs[dir].StructFields into the reconstructed pkg (per-file attribution is not serialized; the package-merged map is the only surviving source).
- update_test.go: TestUpdatePreservesStructFieldsOnLoadedPrior — fixtures structs with fields, Build→Save→Load→Update, asserts StructFields survive AND the field-access callee ("a.TaskSvc.Run()" -> TaskService.Run) still resolves. Verified it FAILS without the fix (callers = []) and PASSES with it.

## Verified
- Full go test ./... 135 packages OK, go vet clean, tag builds clean.
- Fresh build: 3954 struct-field entries, httpClient.roundTrip callers = [Client.roundTrip]. Clean --update preserves 3954.
- Residual environment factor (not code): a long-lived kern-mcp daemon started BEFORE the fix (PID 24869, 9:14PM) writes degraded indexes in the background from its old in-memory index; its background save raced the verification reads and masked the fix. Restart kern-mcp/opencode to clear. Current binary builds are correct.
