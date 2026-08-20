// Package agent implements the multi-agent runtime core: an agent registry, the
// runtime task model, a workflow engine, agent handoffs, session/context
// tracking, and human approval integration. Callers supply a [Provider] so the
// runtime never talks to an external model directly.
package agent
