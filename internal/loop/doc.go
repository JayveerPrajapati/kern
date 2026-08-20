// Package loop implements the continuous closed loop: orchestration that
// connects Intent → Plan → Code → Verify → Deploy → Observe → Learn,
// config-gated by an autonomy level (L0–L5). It reuses the agent runtime,
// execution worktree, verification, governance approval, runtime intelligence
// and engineering memory so the loop can act, verify, observe and learn.
package loop
