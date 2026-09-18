// Command kern exitcode prints kern's documented exit-code conventions.
//
// The table below reflects the conventions the CLI actually enforces:
//   - fatal()/fatalUsage() in cmd/kern/helpers.go panic exitError{code:1}
//     / exitError{code:2}; main() recovers the sentinel and exits with it.
//   - dispatchCommand returns 2 for unknown commands (usage + exit 2).
//   - the blueprint change-governance suite (kern check, kern ci, kern
//     request-approval, kern reject, kern verify-receipt) uses 3 for
//     decided-state / policy outcomes: already-decided approvals, invalid
//     Blueprint configuration, and receipts that cannot be found; a tampered
//     receipt or broken audit chain is 2 (invalid).

package main

import "fmt"

// runExitcode implements `kern exitcode`: prints the exit-code conventions
// table and exits 0. It is deterministic and self-documenting — a stable
// reference for scripts and CI that wrap kern.
func runExitcode(rest []string) {
	fmt.Println("kern exit code conventions")
	fmt.Println("  0  ok — command completed successfully")
	fmt.Println("  1  error — runtime failure (kern: fatal)")
	fmt.Println("  2  usage — bad flags, missing required arguments, unknown command (kern: fatalUsage)")
	fmt.Println("  3  decided-state / policy outcome — e.g. an approval request that is already")
	fmt.Println("     decided (kern approve, kern request-approval, kern reject), a receipt that")
	fmt.Println("     cannot be found (kern verify-receipt), or an invalid Blueprint configuration")
	fmt.Println("     (kern check, kern ci)")
	fmt.Println()
	fmt.Println("examples:")
	fmt.Println("  kern refactor-transaction            missing --edits -> 2 (usage)")
	fmt.Println("  kern reject apr-<id> (already decided)              -> 3 (decided-state)")
	fmt.Println("  kern verify-receipt --receipt-id <id> (tampered)    -> 2 (invalid)")
	fmt.Println("  kern verify-receipt --receipt-id <id> (not found)   -> 3 (policy)")
}
