// ProjectApp is the seam that keeps internal/enterprise independent of the
// web console. The enterprise server manages one ProjectApp per registered
// project — lazily built, cached, LRU-evicted — and delegates HTTP serving,
// task aggregation, and architecture reporting to it.
//
// web.App satisfies this interface structurally; enterprise must never import
// internal/web. The concrete construction (web.New) is injected at the
// composition roots (cmd/kern, cmd/kern-server) via Server.SetAppFactory, so
// the stdio MCP server process no longer transitively links the entire web
// console (the mcp → org → enterprise → web closure). Without an injected
// factory the enterprise server fails closed ("no app factory configured")
// rather than building a nil app.
//
// The ArchitectureReport return type is internal/architecture.Report (not a
// web type) so both web and enterprise can name it without importing each
// other; web's ArchitectureReport converts its internal JSON DTO to it.
package enterprise

import (
	"net/http"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/architecture"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// ProjectApp is the per-project console surface the enterprise server drives.
// Signatures are copied verbatim from web.App's methods; the project root is
// bound at construction time by the injected factory.
type ProjectApp interface {
	// ServeHTTP serves the project console. The enterprise server rewrites
	// the URL to strip the project prefix before delegating.
	ServeHTTP(w http.ResponseWriter, r *http.Request)
	// SetUserRoleLookup wires the org user registry's role lookup into the
	// project console so approve/reject enforce the org RBAC layer (Feature
	// Batch G). Called right after a successful build.
	SetUserRoleLookup(lookup func(id string) (string, bool))
	// SetPolicies swaps the project firewall to the given policy set (org
	// policy propagation, P13 stage 1).
	SetPolicies(policies []domain.Policy)
	// Close tears down background resources (relay, bus subscriptions,
	// in-process MCP server). It is invoked on LRU eviction so a rebuilt
	// app does not leak goroutines or sockets.
	Close() error
	// ListTasks returns the project's task registry for org-level task
	// aggregation.
	ListTasks() []*agent.Task
	// ArchitectureReport returns the project's architecture validation
	// report for the org-level aggregation endpoint.
	ArchitectureReport() (*architecture.Report, error)
}
