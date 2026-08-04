# fix bug

A bug exists in {{ROOT}}. Work through it systematically and stop when you
have a verified fix — do not invent extra changes.

1. Reproduce: state the expected vs actual behaviour.
2. Locate the responsible code. Use `kern context <symbol>` and `kern graph`
   to pull the exact call path instead of reading whole files.
3. Identify the root cause and explain it in one or two sentences.
4. Implement the minimal fix.
5. Verify: run the relevant test or `kern build` and report the result.

Context:
{{MAP}}

Relevant file (if known):
{{FILE}}

Symptom / task:
{{TASK}}
