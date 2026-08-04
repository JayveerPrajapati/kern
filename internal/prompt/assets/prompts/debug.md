# debug

You have a failing run in {{ROOT}}. Get to the root cause efficiently instead
of guessing.

1. If a trace is available, pass it through `kern optimize_log` or let kern
   compress it first, then read the compressed version.
2. Trace the code path with `kern graph <sym>` and `kern context <sym>`.
3. Form a hypothesis, then confirm it with a minimal check — do not change
   code speculatively.
4. Apply the fix and re-run with `kern build`.

Context:
{{MAP}}

Relevant file (if known):
{{FILE}}

Failure description:
{{TASK}}
