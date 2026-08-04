# write tests

Write tests for {{FILE}} in {{ROOT}}. Match the project's existing test
conventions (framework, naming, helpers). Cover:

- Happy path and the obvious edge cases (empty input, boundaries).
- Error paths: bad input, missing resources, failure injection.
- Any invariants the code documents.

Keep tests readable: one scenario per test, clear names, no assertions of
unrelated behaviour. Then run the suite with `kern build` and fix failures.

Project conventions:
{{MAP}}

Target file:
{{FILE}}

Extra requirements:
{{TASK}}
