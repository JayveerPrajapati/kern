# E2E flaky-test policy

The E2E, daemon, and load suites are release gates. A test is never marked
skipped to make CI green, and failures are never silently retried.

## Triage rules

1. **Reproduce locally.** Run the exact Make target and test name from CI:
   `make e2e-full`, `make load-gate`, or
   `go test ./... -run '^TestName$' -count=10`.
2. **Classify before changing the test.**
   - *Product defect:* fix the product and add or retain a deterministic test.
   - *Test defect:* fix the fixture, timeout, output cap, or race in the test.
   - *Environment defect:* make the test hermetic; do not broaden assertions.
   - *Machine noise:* only timing thresholds may be reviewed, and only with a
     fresh baseline from at least three CI runs and three developer machines.
3. **No silent skips.** Platform-only skips must state the unsupported platform
   and the supported CI job that covers that behavior. A skipped test on a
   supported platform is a CI failure.
4. **No quarantine by default.** A test may be quarantined only after a written
   issue documents two independent CI failures plus one local non-reproduction,
   a likely cause, an owner, and a dated removal deadline of at most seven days.
5. **Quarantine is visible.** The issue and PR must name the quarantined test,
   and the nightly `e2e-load` job must keep running it unless the product is
   genuinely unavailable. Quarantines never suppress the load report.
6. **Close the loop.** Remove the quarantine in the same PR that fixes the root
   cause. A quarantine that outlives its deadline fails the release checklist.
