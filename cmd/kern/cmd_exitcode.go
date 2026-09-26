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
//   - the review family (kern review / kern changes) exits 3 (policy family)
//     when changed files carry risk; a clean review exits 0.
//   - kern security joins the policy family: error-severity findings exit 3
//     (QA F2 — findings are a reported risk, matching kern review; the hard
//     CI gate failure remains kern verify --types security's FAIL -> 1).
//   - bare action commands require their required argument: kern approve
//     without an id and kern evidence without a subcommand exit 2 (the
//     pending-approval listing is the explicit 'kern approve list').
//   - kern search exits 1 when no symbols match (the no-match contract
//     shared with kern explore / kern graph: "not found" is an error, not
//     success). An empty --json result stays exit 0 — an empty result array
//     is data, not an error.
//   - kern verify exits 1 only for a FAIL verdict; WARN and SKIPPED verdicts
//     are reported outcomes that exit 0 (a SKIPPED run — e.g. govulncheck not
//     installed or a missing manifest — is never a hard failure).

package main

import "fmt"

// runExitcode implements `kern exitcode`: prints the exit-code conventions
// table and exits 0. It is deterministic and self-documenting — a stable
// reference for scripts and CI that wrap kern.
func runExitcode(rest []string) {
	fmt.Println("kern exit code conventions")
	fmt.Println("  0  ok — command completed successfully; also the reported-outcome exit for")
	fmt.Println("     clean reviews (kern review/changes with no risk) and non-FAIL verification")
	fmt.Println("     verdicts (WARN and SKIPPED are reported, not failures — kern verify)")
	fmt.Println("  1  error — runtime failure (kern: fatal); a FAIL verification verdict")
	fmt.Println("     (kern verify prints \"verification FAILED\" and exits 1)")
	fmt.Println("  2  usage — bad flags, missing required arguments, unknown command (kern: fatalUsage)")
	fmt.Println("  3  decided-state / policy outcome — e.g. an approval request that is already")
	fmt.Println("     decided (kern approve, kern request-approval, kern reject), a receipt that")
	fmt.Println("     cannot be found (kern verify-receipt), an invalid Blueprint configuration")
	fmt.Println("     (kern check, kern ci), changed files with risk (kern review / kern changes),")
	fmt.Println("     or error-severity security findings (kern security — the review-family")
	fmt.Println("     convention; a hard CI failure stays kern verify --types security -> 1)")
	fmt.Println()
	fmt.Println("examples:")
	fmt.Println("  kern refactor-transaction            missing --edits -> 2 (usage)")
	fmt.Println("  kern evidence (no subcommand)                       -> 2 (usage)")
	fmt.Println("  kern approve (no id)                                 -> 2 (usage; 'kern approve list' shows pending)")
	fmt.Println("  kern reject apr-<id> (already decided)              -> 3 (decided-state)")
	fmt.Println("  kern verify-receipt --receipt-id <id> (tampered)    -> 2 (invalid)")
	fmt.Println("  kern verify-receipt --receipt-id <id> (not found)   -> 3 (policy)")
	fmt.Println("  kern review (risk found)                            -> 3 (policy family)")
	fmt.Println("  kern security (error-severity findings)              -> 3 (policy family)")
	fmt.Println("  kern search <query> (no symbols matched)             -> 1 (no-match error; empty --json result stays 0)")
	fmt.Println("  kern verify --types cve (SKIPPED: govulncheck absent) -> 0 (reported)")
	fmt.Println("  kern verify (FAIL verdict)                          -> 1 (failure)")
}
