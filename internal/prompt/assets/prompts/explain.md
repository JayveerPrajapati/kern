# explain

Explain {{TASK}} in {{ROOT}} to a capable engineer who has not seen this code.

Use `kern graph` and `kern context` to trace the real call path before
explaining. Cover:
- What it is and its responsibility.
- Where it is called from and what it calls.
- The important invariants and why they matter.
- The trade-offs or known limitations.

Keep it under 250 words. Lead with the answer.

Project map:
{{MAP}}
