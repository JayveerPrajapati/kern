// Package coder implements the autonomous coding agent .
// It drives an LLM provider to generate code from an intent + plan, applies
// the patch to a sandbox worktree, verifies it (build/test), and iterates on
// failure until verification passes or the round budget is exhausted. It can
// serve as the loop's default StepFunc, and degrades gracefully when no LLM
// provider is available (returns ErrNoProvider).
package coder
