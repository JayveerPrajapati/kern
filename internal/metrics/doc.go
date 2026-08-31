// Package metrics provides a unified, thread-safe metrics layer for kern,
// consolidating performance, self-observability, AI governance, and product
// success metrics into a single Recorder.
// All metrics are local — nothing is transmitted. The Recorder is nil-safe
// (all methods are no-ops on a nil *Recorder), so callers can use
// `var r *metrics.Recorder` without nil checks.
// Usage:
// r := metrics.New()
// start := time.Now()
// ix, _ := index.Build(root)
// r.RecordIndexBuild(time.Since(start))
// // ... later ...
// snapshot := r.Snapshot()
// fmt.Println(snapshot.Report())
package metrics
