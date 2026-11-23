// serve.go is the guard's serving WIRING. The tiers themselves (index-backed
// answers, tier-2 tails, the readonly-sandbox pipeline, the governed
// auto-redirect map) live in internal/guardserve; this file injects the two
// host couplings — the kern binary resolver and the redirect audit sink — and
// keeps the hook call sites (guardhook.go) unchanged.
package guardhook

import (
	"io"

	"github.com/JayveerPrajapati/kern/internal/guardrule"
	"github.com/JayveerPrajapati/kern/internal/guardserve"
)

var guardServer = guardserve.New(kernBin, auditRedirect)

// servePipeline runs the serve tiers for a classified pipeline. A true return
// means the pipeline was served (code is the hook exit code); false falls
// through to auto-redirect/deny.
func servePipeline(cmd string, res guardrule.ClassifyResult, root string, stdout io.Writer, hasToolCall bool) (int, bool) {
	return guardServer.Pipeline(cmd, res, root, stdout, hasToolCall)
}

// autoRedirect answers simple single-token commands via kern CLI equivalents.
func autoRedirect(cmd string, stdout io.Writer) (int, bool) {
	return guardServer.AutoRedirect(cmd, stdout)
}
