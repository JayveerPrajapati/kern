//go:build ignore

// Signing moved to internal/approvalsign. The Ed25519 keystore pushed this
// package past its ARCHITECTURE.md LOC cap; the contract resolves that by
// extraction, never cap-raising. This stub stays build-ignored.
package execapproval
