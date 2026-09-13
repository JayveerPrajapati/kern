# Agent Note: Dead Code Sweep Finds Tool False Positives
Status: implemented

## Problem
`kern dead` reported 10 no-caller public symbols (~880 lines) as cleanup candidates.

## Decision
- Verified every candidate with `kern delete <sym>` (the safe-deletion analyzer): ALL 10 are NOT SAFE to delete.
- 6 have live production callers the dead tool missed: BlueprintService.Validate (Dial, DocBudgetCheck.Run, Server.hand...), indexService.EnsureFresh (runEnsureFresh), Engine.Evaluate (BlueprintService.runCheck, Server.precheckTool), Analyzer.Analyze (App.handleV1Analyze, Pipeline.Run), TaskService.CreatePR + GitHubProvider.CreatePR (Engine.CreateFixPR). The remaining 4 are exported symbols whose external/dynamic callers are invisible to the index.
- Root cause: kern dead's caller detection misses interface-dispatch and MCP-handler-mediated calls (the cross-package qualified-callee quirk, memory #52).

## Consequence
- NO deletions performed — deleting any candidate would have removed live code.
- Finding recorded: `kern dead` false-positives on interface-dispatched public API; a future tool fix should reconcile its caller detection with `kern delete`'s analyzer before any dead-code campaign is trusted.
