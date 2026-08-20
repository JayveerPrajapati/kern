// Package agents implements the multi-agent engineering layer: specialist
// agent roles, the standard team, and a multi-agent pipeline that runs a task
// through the team with per-stage handoffs. It builds on the Agent Runtime
// (internal/agent) and governance (internal/governance); callers inject the
// per-stage execution via the pipeline step handler.
package agents
