package runtime

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// namedSource decorates a *Store with a distinct adapter name so the vendor
// wrappers report the right Source identity ("otel", "prometheus", "kubernetes")
// instead of the local store's "local".
type namedSource struct {
	*Store
	name string
}

// Name implements Source with the adapter-specific identity.
func (s *namedSource) Name() string { return s.name }

// ---------------------------------------------------------------------------
// OpenTelemetry (OTLP-style JSON)
// ---------------------------------------------------------------------------

// otelItem is a single telemetry record inside an OTLP-style document. The same
// shape is reused for logs, traces and metrics.
type otelItem struct {
	Service    string            `json:"service"`
	Timestamp  string            `json:"timestamp"`
	Severity   string            `json:"severity"`
	Body       string            `json:"body"`
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	Name       string            `json:"name"`
	Value      json.RawMessage   `json:"value"`
	Attributes map[string]string `json:"attributes"`
}

// otelDoc is the parsed OTL-style JSON document.
type otelDoc struct {
	Logs    []otelItem `json:"logs"`
	Traces  []otelItem `json:"traces"`
	Metrics []otelItem `json:"metrics"`
}

// ParseOtel parses a simplified OTLP-style JSON document into telemetry events.
// Document shape: {"logs":[{"service":..,"timestamp":RFC3339,"severity":"error"|"warning"|"info","body":..,"trace_id":..,"span_id":..,"attributes":{..}}],
//
//	"traces":[{...same...}],
//	"metrics":[{"service":..,"timestamp":..,"name":..,"value":<number|string>,"attributes":{..}}]}
func ParseOtel(data []byte) ([]Event, error) {
	var doc otelDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(doc.Logs)+len(doc.Traces)+len(doc.Metrics))
	for _, it := range doc.Logs {
		ev, ok := otelEvent(it, EventLog)
		if !ok {
			continue
		}
		if it.Severity == "error" || it.Severity == "critical" {
			ev.Type = EventError
		}
		ev.Message = it.Body
		ev.Severity = it.Severity
		events = append(events, ev)
	}
	for _, it := range doc.Traces {
		ev, ok := otelEvent(it, EventTrace)
		if !ok {
			continue
		}
		ev.TraceID = it.TraceID
		ev.SpanID = it.SpanID
		ev.Message = it.Body
		ev.Severity = it.Severity
		events = append(events, ev)
	}
	for _, it := range doc.Metrics {
		if it.Service == "" {
			continue
		}
		ts := parseTime(it.Timestamp)
		if ts.IsZero() {
			continue
		}
		events = append(events, Event{
			Type:       EventMetric,
			Service:    it.Service,
			Severity:   "info",
			Message:    it.Name + "=" + formatOtelValue(it.Value),
			Timestamp:  ts,
			Attributes: it.Attributes,
		})
	}
	return events, nil
}

// otelEvent builds a non-metric event, returning ok=false when the service is
// empty (such items are skipped).
func otelEvent(it otelItem, typ EventType) (Event, bool) {
	if it.Service == "" {
		return Event{}, false
	}
	ts := parseTime(it.Timestamp)
	if ts.IsZero() {
		return Event{}, false
	}
	return Event{
		Type:       typ,
		Service:    it.Service,
		Timestamp:  ts,
		Attributes: it.Attributes,
	}, true
}

// formatOtelValue renders a JSON number or string without quotes.
func formatOtelValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return fmt.Sprintf("%v", v)
}

// OtelSource parses an OTLP-style JSON document into a Source.
func OtelSource(data []byte) (Source, error) {
	events, err := ParseOtel(data)
	if err != nil {
		return nil, err
	}
	st := NewStore()
	st.IngestAll(events)
	return &namedSource{Store: st, name: "otel"}, nil
}

// ---------------------------------------------------------------------------
// Prometheus text exposition
// ---------------------------------------------------------------------------

// promServiceLabel matches the service label inside a metric label set, e.g.
// in `http_requests{service="checkout",method="GET"} 12`.
var promServiceRe = regexp.MustCompile(`service="([^"]*)"`)

// ParsePrometheus parses a Prometheus text-exposition payload into metric
// events. Line shape: <name>{service="svc",...} <value> [timestamp]. Lines
// starting with '#' are ignored. service may be absent (label empty).
func ParsePrometheus(data []byte) ([]Event, error) {
	lines := strings.Split(string(data), "\n")
	events := make([]Event, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ev, ok := parsePrometheusLine(line)
		if !ok {
			continue
		}
		events = append(events, ev)
	}
	return events, nil
}

// parsePrometheusLine parses a single metric line. Returns ok=false for
// malformed lines (no panic).
func parsePrometheusLine(line string) (Event, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return Event{}, false
	}
	head, valStr := fields[0], fields[1]

	name := head
	service := ""
	if i := strings.Index(head, "{"); i >= 0 {
		name = head[:i]
		if m := promServiceRe.FindStringSubmatch(head); len(m) == 2 {
			service = m[1]
		}
	}

	ts := time.Now()
	if len(fields) >= 3 {
		if secs, err := strconv.ParseFloat(fields[2], 64); err == nil {
			ts = time.Unix(int64(secs), 0).UTC()
		}
	}

	ev := Event{
		Type:      EventMetric,
		Service:   service,
		Severity:  "info",
		Message:   name + "=" + valStr,
		Timestamp: ts,
	}
	return ev, true
}

// PrometheusSource parses a Prometheus text-exposition payload into a Source.
func PrometheusSource(data []byte) (Source, error) {
	events, err := ParsePrometheus(data)
	if err != nil {
		return nil, err
	}
	st := NewStore()
	st.IngestAll(events)
	return &namedSource{Store: st, name: "prometheus"}, nil
}

// ---------------------------------------------------------------------------
// Kubernetes JSON snapshot
// ---------------------------------------------------------------------------

// kubeDeployment is one deployment entry in a Kubernetes JSON snapshot.
type kubeDeployment struct {
	Name      string `json:"name"`
	Service   string `json:"service"`
	Version   string `json:"version"`
	Timestamp string `json:"timestamp"`
}

// kubeEvent is one event entry in a Kubernetes JSON snapshot.
type kubeEvent struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Type      string `json:"type"` // "Warning" | "Normal"
	Reason    string `json:"reason"`
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// kubernetesDoc is the parsed Kubernetes JSON snapshot.
type kubernetesDoc struct {
	Deployments []kubeDeployment `json:"deployments"`
	Events      []kubeEvent      `json:"events"`
}

// ParseKubernetes parses a Kubernetes JSON snapshot into deployments and
// events. Document shape: {"deployments":[{"name":..,"service":..,"version":..,"timestamp":RFC3339}],
//
//	"events":[{"namespace":..,"service":..,"type":"Warning"|"Normal", "reason":..,"message":..,"timestamp":RFC3339}]}
func ParseKubernetes(data []byte) ([]domain.Deployment, []Event, error) {
	var doc kubernetesDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	deployments := make([]domain.Deployment, 0, len(doc.Deployments))
	for _, d := range doc.Deployments {
		if d.Service == "" {
			continue
		}
		dep := domain.Deployment{
			Service:    d.Service,
			CommitSHA:  d.Name,
			Version:    d.Version,
			DeployedAt: parseTime(d.Timestamp),
		}
		if dep.DeployedAt.IsZero() {
			continue
		}
		deployments = append(deployments, dep)
	}
	events := make([]Event, 0, len(doc.Events))
	for _, e := range doc.Events {
		if e.Service == "" {
			continue
		}
		typ := EventLog
		sev := "info"
		if e.Type == "Warning" || kubeReasonFails(e.Reason) {
			typ = EventError
			sev = "error"
		}
		ev := Event{
			Type:      typ,
			Service:   e.Service,
			Severity:  sev,
			Message:   e.Reason + ": " + e.Message,
			Timestamp: parseTime(e.Timestamp),
		}
		if ev.Timestamp.IsZero() {
			continue
		}
		events = append(events, ev)
	}
	return deployments, events, nil
}

// kubeReasonFails reports whether a Kubernetes event reason implies a failure
// condition (crash/error/failure).
func kubeReasonFails(reason string) bool {
	r := strings.ToLower(reason)
	for _, sub := range []string{"fail", "error", "crash", "oom", "unhealthy", "backoff"} {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

// KubernetesSource parses a Kubernetes JSON doc into a Source.
func KubernetesSource(data []byte) (Source, error) {
	deployments, events, err := ParseKubernetes(data)
	if err != nil {
		return nil, err
	}
	st := NewStore()
	st.IngestAll(events)
	for _, d := range deployments {
		st.AddDeployment(d)
	}
	return &namedSource{Store: st, name: "kubernetes"}, nil
}

// parseTime parses an RFC3339 timestamp, returning the zero time on absent or
// malformed input so a bad field never fabricates a timestamp (and thus never
// places an event inside a lookback window). Callers must guard for IsZero and
// drop such records.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
