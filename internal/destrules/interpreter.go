//go:build ignore

// Interpreter/eval and encoded-pipe heuristics moved to internal/execinterp:
// they pushed destrules past its ARCHITECTURE.md LOC cap, and the contract
// resolves that by extraction, never cap-raising. This stub stays
// build-ignored.
package destrules
