package runtime

import (
	"regexp"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// prRefRe matches a GitHub-style PR reference in a commit message: a "#42",
// "(#42)" or "(42)" token preceded by a word boundary. Group 1 (or group 2
// when the token is a bare parenthesized number) holds the PR number.
var prRefRe = regexp.MustCompile(`(?i)(?:^|\s)(?:\(?#(\d+)\)?|\((\d+)\))`)

// ChainLink is one step in the deep evidence chain for an alert
// (alert -> service -> deployment -> commit -> symbol -> task/pr/agent).
type ChainLink struct {
	Stage string // "service","deployment","commit","symbol","task","pr","agent","trace","event"
	ID    string
}

// CorrelationChain is the resolved deep chain for an alert.
type CorrelationChain struct {
	Alert   domain.Alert
	Service string
	Links   []ChainLink
	// TraceLinks tie the chain to the trace/event evidence that produced it
	// (Phase 13.1): the correlation chain was previously detached from the
	// raw telemetry. These make the evidence traceable.
	TraceLinks []TraceLink
}

// CorrelateChain resolves the affected service and derives the deterministic
// evidence chain (deployments, commits, symbols, and task/pr/agent refs) for
// the alert, within the lookback window. It does not mutate the source.
func (c *Correlator) CorrelateChain(a domain.Alert) CorrelationChain {
	svc := c.resolveService(a)
	res := CorrelationChain{Alert: a, Service: svc}
	from := a.OccurredAt.Add(-c.window)

	var links []ChainLink
	if svc != "" {
		links = append(links, ChainLink{Stage: "service", ID: svc})
	}

	for _, d := range recentDeployments(c.src.Deployments(svc), from, a.OccurredAt) {
		id := d.Version
		if id == "" {
			id = d.CommitSHA
		}
		if id != "" {
			links = append(links, ChainLink{Stage: "deployment", ID: id})
		}
	}

	for _, rc := range recentCommits(c.src.Commits(), from) {
		sha := rc.SHA
		if len(sha) > 8 {
			sha = sha[:8]
		}
		if sha != "" {
			links = append(links, ChainLink{Stage: "commit", ID: sha})
		}
		for _, m := range prRefRe.FindAllStringSubmatch(rc.Message, -1) {
			if len(m) >= 3 {
				id := m[1]
				if id == "" {
					id = m[2]
				}
				if id != "" {
					links = append(links, ChainLink{Stage: "pr", ID: id})
				}
			}
		}
	}

	for _, e := range c.src.Events(svc) {
		if e.Timestamp.Before(from) || e.Timestamp.After(a.OccurredAt) {
			continue
		}
		if !e.IsError() {
			continue
		}
		for _, key := range []string{"symbol", "file"} {
			if id := e.Attributes[key]; id != "" {
				links = append(links, ChainLink{Stage: "symbol", ID: id})
			}
		}
		if id := e.Attributes["task"]; id != "" {
			links = append(links, ChainLink{Stage: "task", ID: id})
		}
		if id := e.Attributes["agent"]; id != "" {
			links = append(links, ChainLink{Stage: "agent", ID: id})
		}
	}

	res.Links = dedupeLinks(links)

	// Phase 13.1: link the chain to the trace/event evidence that produced it,
	// so the chain is traceable back to raw telemetry.
	res.TraceLinks = TraceEventsFromCorrelation(c.traceCorrelation(a, svc))
	return res
}

// traceCorrelation builds a lightweight Correlation (evidence only) so the
// chain can extract trace/event links without coupling to the full correlate
// path.
func (c *Correlator) traceCorrelation(a domain.Alert, svc string) *Correlation {
	corr := &Correlation{Alert: a, AffectedService: svc}
	from := a.OccurredAt.Add(-c.window)
	for _, e := range c.src.Events(svc) {
		if e.Timestamp.Before(from) || e.Timestamp.After(a.OccurredAt) {
			continue
		}
		switch e.Type {
		case EventTrace:
			corr.TraceSpans = append(corr.TraceSpans, e)
		case EventError:
			corr.ErrorEvents = append(corr.ErrorEvents, e)
		}
	}
	return corr
}

// dedupeLinks removes duplicate (Stage,ID) links, preserving first-seen order.
func dedupeLinks(links []ChainLink) []ChainLink {
	seen := make(map[ChainLink]struct{}, len(links))
	out := make([]ChainLink, 0, len(links))
	for _, l := range links {
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	return out
}
