// Incident → twin → code correlation report type (Feature Batch D). The
// correlation ENGINE stays with the platform (app.Platform.CorrelateCode —
// it needs app's construction context); the report TYPE moves with the task
// cluster because it leaks through TaskService's public API (TaskService.
// CorrelateCode returns it).
package tasklife

import "github.com/JayveerPrajapati/kern/internal/domain"

// Correlation is the incident→twin→code correlation report for an alert:
// the affected service, the existing runtime evidence, the twin-resolved
// implicated source files and symbols, a deterministic confidence, and any
// auto-attached heal playbook.
type Correlation struct {
	Alert             domain.Alert
	AffectedService   string
	RuntimeEvidence   []string // formatted runtime evidence (errors/deployments/chain)
	ImplicatedFiles   []string // root-relative source files implicated by the service
	ImplicatedSymbols []string // symbols defined in (or referenced by) the implicated code
	Confidence        string   // "high" (exact service→code mapping) or "low" (heuristic)
	PlaybookSignature string   // auto-attached heal playbook signature (empty = none)
	PlaybookSteps     []string // auto-attached playbook steps
}
