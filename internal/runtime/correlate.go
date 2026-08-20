package runtime

import (
	"sort"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Correlation is the outcome of correlating one production alert against the
// runtime: the affected service, the deployments and commits that produced it,
// and the error/log/trace/metric evidence observed in the lookback window.
type Correlation struct {
	Alert           domain.Alert
	AffectedService string
	Severity        domain.Severity
	Deployments     []domain.Deployment
	RecentCommits   []Commit
	ErrorEvents     []Event
	LogEvents       []Event
	TraceSpans      []Event
	MetricEvents    []Event
	Window          time.Duration
}

// Correlator maps a production alert to the affected runtime service and the
// evidence around it. It depends only on the Source interface so no vendor
// coupling reaches core logic.
type Correlator struct {
	src    Source
	window time.Duration
}

// NewCorrelator builds a correlator over a source with a default lookback
// window for the evidence gathered around an alert.
func NewCorrelator(src Source, window time.Duration) *Correlator {
	if window <= 0 {
		window = 30 * time.Minute
	}
	return &Correlator{src: src, window: window}
}

// Correlate resolves the affected service for an alert and gathers the runtime
// evidence (deployments, commits, errors, logs, traces, metrics) observed within
// the lookback window leading up to the alert. The result is deterministic.
func (c *Correlator) Correlate(a domain.Alert) Correlation {
	svc := c.resolveService(a)
	res := Correlation{
		Alert:           a,
		Severity:        a.Severity,
		AffectedService: svc,
		Window:          c.window,
	}
	from := a.OccurredAt.Add(-c.window)

	res.Deployments = recentDeployments(c.src.Deployments(svc), from, a.OccurredAt)
	res.RecentCommits = recentCommits(c.src.Commits(), from)

	for _, e := range c.src.Events(svc) {
		if e.Timestamp.Before(from) || e.Timestamp.After(a.OccurredAt) {
			continue
		}
		switch e.Type {
		case EventError:
			res.ErrorEvents = append(res.ErrorEvents, e)
		case EventLog:
			res.LogEvents = append(res.LogEvents, e)
		case EventTrace:
			res.TraceSpans = append(res.TraceSpans, e)
		case EventMetric:
			res.MetricEvents = append(res.MetricEvents, e)
		}
	}
	sort.SliceStable(res.ErrorEvents, func(i, j int) bool {
		return res.ErrorEvents[i].Timestamp.Before(res.ErrorEvents[j].Timestamp)
	})
	return res
}

// resolveService determines the affected service. It prefers the service
// declared on the alert; otherwise it infers the service from the most recent
// error event observed in the lookback window (deterministic fallback).
func (c *Correlator) resolveService(a domain.Alert) string {
	if a.Service != "" {
		return a.Service
	}
	from := a.OccurredAt.Add(-c.window)
	best := ""
	bestT := time.Time{}
	for _, e := range c.src.Events("") {
		if e.Type != EventError {
			continue
		}
		if e.Timestamp.Before(from) || e.Timestamp.After(a.OccurredAt) {
			continue
		}
		if e.Service != "" && e.Timestamp.After(bestT) {
			best, bestT = e.Service, e.Timestamp
		}
	}
	return best
}

// recentDeployments returns deployments deployed within the closed window
// [from, to], newest first.
func recentDeployments(deployments []domain.Deployment, from, to time.Time) []domain.Deployment {
	var out []domain.Deployment
	for _, d := range deployments {
		if d.DeployedAt.Before(from) || d.DeployedAt.After(to) {
			continue
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].DeployedAt.After(out[j].DeployedAt)
	})
	return out
}

// recentCommits returns commits committed at or after from, newest first.
func recentCommits(commits []Commit, from time.Time) []Commit {
	var out []Commit
	for _, c := range commits {
		if !c.CommittedAt.Before(from) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CommittedAt.After(out[j].CommittedAt)
	})
	return out
}
