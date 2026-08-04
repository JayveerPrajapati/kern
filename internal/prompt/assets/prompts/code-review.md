# code review

You are reviewing code in {{ROOT}}. Be rigorous but concise; flag only issues
that matter. For each finding give `file:line`, the problem, and a one-line
fix. Do not pad the report.

Focus in this order:
1. Correctness bugs and race conditions.
2. Security (injection, secrets, unsafe parsing, path traversal).
3. Error handling (silently swallowed errors, panics on bad input).
4. Performance (quadratic loops, N+1, leaking resources).
5. Style and dead code, only if it affects maintainability.

Context:
{{MAP}}

Primary target:
{{FILE}}

Additional instructions:
{{TASK}}
