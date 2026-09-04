// Package cli exposes the command runners for Blueprint CLI operations.
package cli

// RunCheck executes `blueprint check` / `kern check`.
func RunCheck(args []string) int { return runCheck(args) }

// RunFix executes `blueprint fix` / `kern fix`.
func RunFix(args []string) int { return runFix(args) }

// RunCI executes `blueprint ci` / `kern ci`.
func RunCI(args []string) int { return runCI(args) }

// RunVerifyReceipt executes `blueprint verify-receipt` / `kern verify-receipt`.
func RunVerifyReceipt(args []string) int { return runVerifyReceipt(args) }

// RunDoctor executes `blueprint doctor`.
func RunDoctor(args []string) int { return runDoctor(args) }

// RunGraph executes `blueprint graph`.
func RunGraph(args []string) int { return runGraph(args) }

// RunInstall executes `blueprint install`.
func RunInstall(args []string) int { return runInstall(args) }

// RunWatch executes `blueprint watch`.
func RunWatch(args []string) int { return runWatch(args) }

// RunMetrics executes `blueprint metrics`.
func RunMetrics(args []string) int { return runMetrics(args) }

// RunRequestApproval executes `blueprint request-approval` / `kern request-approval`.
func RunRequestApproval(args []string) int { return runRequestApproval(args) }

// RunApprovalDecision executes `blueprint approve` / `blueprint reject`.
func RunApprovalDecision(decision string, args []string) int {
	return runApprovalDecision(decision, args)
}
