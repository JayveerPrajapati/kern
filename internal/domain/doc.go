// Package domain contains the canonical Kern 2.0 domain entities .
// These are pure domain types — no MCP, CLI, REST, or storage specifics — and
// are used by every interface (CLI, MCP, REST, SDK, agents, UI).
// The package keeps all entities in a flat layout because they are
// interdependent (Graph references Node/Edge, ContextPacket references
// Symbol/File/Risk/Claim) and many packages import them by name.
// domain.go holds the core entities (Node, Edge, Graph, Symbol, File, etc.),
// entities.go the extended entities (Service, API, Database, Table, Topic,
// Commit, PullRequest, Permission, Artifact, VerificationResult), runtime.go
// the runtime entities (Alert, Deployment, Incident, Hypothesis, RootCause),
// and context_packet.go the ContextPacket.
package domain
