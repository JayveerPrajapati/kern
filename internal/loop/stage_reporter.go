package loop

import "context"

// StageReporter receives each loop stage as it starts (only stages that pass
// the autonomy gate; skipped stages are not reported), with the cumulative
// percentage across the 9-stage chain. It travels on the context so callers
// that know the transport (e.g. the MCP server holding a progress token) can
// receive stage updates without changing every intermediate signature
// (TaskService, highlevel handlers) along the way.
type StageReporter func(stage string, pct int)

type stageReporterKey struct{}

// WithStageReporter returns a context carrying a stage reporter. A nil
// reporter returns ctx unchanged.
func WithStageReporter(ctx context.Context, fn StageReporter) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, stageReporterKey{}, fn)
}

// StageReporterFromContext returns the carried stage reporter, or nil when
// absent (callers must treat nil as a no-op).
func StageReporterFromContext(ctx context.Context) StageReporter {
	fn, _ := ctx.Value(stageReporterKey{}).(StageReporter)
	return fn
}
