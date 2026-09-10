// Package service provides the delivery-mechanism-independent service layer
// for kern. Business logic that used to live inside the CLI (cmd/kern), the
// MCP server (internal/mcp) and the web console (internal/web) is exposed
// here as a single, uniform API so any delivery mechanism can call the same
// operations without importing the engines directly.
//
// Five services are provided, one per core domain:
//
//   - IndexService:      build / load / status / watch the symbol index
//   - GraphService:      explore / search / path / why over the code graph
//   - MemoryService:     add / recall / list project lessons
//   - GovernanceService: approve / audit / check (change governance)
//   - SecurityService:   scan / mask / validate (secrets, PII, schemas)
//
// Services are stateless facades over the internal engines; they take the
// project root (and a context for cancellation) on every call. A single
// *Services value can be shared across all delivery mechanisms of one
// process.
package service

// Services aggregates every service in the layer. Delivery mechanisms
// construct one *Services (service.New()) and pass it to their handlers;
// they never reach into the internal engines themselves.
type Services struct {
	Index      IndexService
	Graph      GraphService
	Memory     MemoryService
	Governance GovernanceService
	Security   SecurityService
}

// New returns a fully wired service layer backed by the real internal
// engines. Each service is stateless and safe for concurrent use.
func New() *Services {
	return &Services{
		Index:      newIndexService(),
		Graph:      newGraphService(),
		Memory:     newMemoryService(),
		Governance: newGovernanceService(),
		Security:   newSecurityService(),
	}
}
