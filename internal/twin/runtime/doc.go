// Package runtime builds graph nodes from runtime telemetry (events,
// deployments) for the Digital Twin's Runtime category. It makes the twin
// "living" — runtime state flows back into the model as graph nodes. The
// builder is read-only: it reads from a runtime.Source and returns
// domain.Node/Edge slices without modifying the source or the graph.
package runtime
