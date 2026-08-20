package runtime

import (
	"testing"
)

func TestParseOtel(t *testing.T) {
	doc := `{
		"logs": [
			{"service":"checkout","timestamp":"2026-08-18T10:00:00Z","severity":"error","body":"boom","attributes":{"symbol":"Checkout.Run"}},
			{"service":"payments","timestamp":"2026-08-18T10:01:00Z","severity":"info","body":"ok","attributes":{}}
		],
		"traces": [
			{"service":"checkout","timestamp":"2026-08-18T10:02:00Z","body":"span POST /checkout","trace_id":"t1","span_id":"s1"}
		],
		"metrics": [
			{"service":"checkout","timestamp":"2026-08-18T10:03:00Z","name":"http.duration.p99","value":250,"attributes":{"unit":"ms"}},
			{"service":"payments","timestamp":"2026-08-18T10:04:00Z","name":"queue.depth","value":"3","attributes":{}}
		]
	}`
	events, err := ParseOtel([]byte(doc))
	if err != nil {
		t.Fatalf("ParseOtel: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("events = %d, want 5", len(events))
	}
	var logErr, logInfo, trace, metricNum, metricStr *Event
	for i := range events {
		switch events[i].Message {
		case "boom":
			logErr = &events[i]
		case "ok":
			logInfo = &events[i]
		case "span POST /checkout":
			trace = &events[i]
		case "http.duration.p99=250":
			metricNum = &events[i]
		case "queue.depth=3":
			metricStr = &events[i]
		}
	}
	if logErr == nil || logErr.Type != EventError || logErr.Service != "checkout" || logErr.Severity != "error" {
		t.Fatalf("error log event wrong: %+v", logErr)
	}
	if logInfo == nil || logInfo.Type != EventLog || logInfo.Service != "payments" {
		t.Fatalf("info log event wrong: %+v", logInfo)
	}
	if trace == nil || trace.Type != EventTrace || trace.TraceID != "t1" || trace.SpanID != "s1" || trace.Service != "checkout" {
		t.Fatalf("trace event wrong: %+v", trace)
	}
	if metricNum == nil || metricNum.Type != EventMetric || metricNum.Message != "http.duration.p99=250" {
		t.Fatalf("numeric metric wrong: %+v", metricNum)
	}
	if metricStr == nil || metricStr.Type != EventMetric || metricStr.Message != "queue.depth=3" {
		t.Fatalf("string metric wrong: %+v", metricStr)
	}
}

func TestParseOtelSkipsEmptyService(t *testing.T) {
	doc := `{"logs":[{"service":"","timestamp":"2026-08-18T10:00:00Z","body":"ignored"}]}`
	events, err := ParseOtel([]byte(doc))
	if err != nil {
		t.Fatalf("ParseOtel: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0", len(events))
	}
}

func TestParsePrometheus(t *testing.T) {
	payload := `# HELP http_requests_total The total number of HTTP requests.
# TYPE http_requests_total counter
http_requests_total{service="checkout",method="GET"} 12
http_requests_total{service="checkout",method="POST"} 3
free_lines{job="x"} 1
`
	events, err := ParsePrometheus([]byte(payload))
	if err != nil {
		t.Fatalf("ParsePrometheus: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3 (comments ignored)", len(events))
	}
	if events[0].Type != EventMetric || events[0].Service != "checkout" || events[0].Message != "http_requests_total=12" {
		t.Fatalf("first metric wrong: %+v", events[0])
	}
	if events[1].Service != "checkout" || events[1].Message != "http_requests_total=3" {
		t.Fatalf("second metric wrong: %+v", events[1])
	}
	// service label absent on the third line.
	if events[2].Service != "" || events[2].Message != "free_lines=1" {
		t.Fatalf("third metric wrong: %+v", events[2])
	}
}

func TestParsePrometheusMalformedSkipped(t *testing.T) {
	payload := "ok_metric 1\n# comment\nmalformed_line_no_value\n"
	events, err := ParsePrometheus([]byte(payload))
	if err != nil {
		t.Fatalf("ParsePrometheus: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
}

func TestParseKubernetes(t *testing.T) {
	doc := `{
		"deployments": [
			{"name":"checkout-7d8f9","service":"checkout","version":"v1.2.0","timestamp":"2026-08-18T10:00:00Z"}
		],
		"events": [
			{"namespace":"prod","service":"checkout","type":"Warning","reason":"CrashLoopBackOff","message":"restarting","timestamp":"2026-08-18T10:00:00Z"},
			{"namespace":"prod","service":"checkout","type":"Normal","reason":"Started","message":"container started","timestamp":"2026-08-18T10:00:00Z"}
		]
	}`
	deployments, events, err := ParseKubernetes([]byte(doc))
	if err != nil {
		t.Fatalf("ParseKubernetes: %v", err)
	}
	if len(deployments) != 1 {
		t.Fatalf("deployments = %d, want 1", len(deployments))
	}
	if deployments[0].Service != "checkout" || deployments[0].Version != "v1.2.0" || deployments[0].CommitSHA != "checkout-7d8f9" {
		t.Fatalf("deployment wrong: %+v", deployments[0])
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Type != EventError || events[0].Severity != "error" || events[0].Message != "CrashLoopBackOff: restarting" {
		t.Fatalf("warning event wrong: %+v", events[0])
	}
	if events[1].Type != EventLog || events[1].Severity != "info" {
		t.Fatalf("normal event wrong: %+v", events[1])
	}
}

func TestAdapterSourceNames(t *testing.T) {
	otel, err := OtelSource([]byte(`{"logs":[{"service":"checkout","body":"hi"}]}`))
	if err != nil {
		t.Fatalf("OtelSource: %v", err)
	}
	if got := otel.Name(); got != "otel" {
		t.Fatalf("otel Name = %q, want otel", got)
	}

	prom, err := PrometheusSource([]byte("up{service=\"checkout\"} 1\n"))
	if err != nil {
		t.Fatalf("PrometheusSource: %v", err)
	}
	if got := prom.Name(); got != "prometheus" {
		t.Fatalf("prometheus Name = %q, want prometheus", got)
	}

	kube, err := KubernetesSource([]byte(`{"deployments":[{"service":"checkout"}]}`))
	if err != nil {
		t.Fatalf("KubernetesSource: %v", err)
	}
	if got := kube.Name(); got != "kubernetes" {
		t.Fatalf("kubernetes Name = %q, want kubernetes", got)
	}
}

func TestKubernetesSourceFeedsEvents(t *testing.T) {
	src, err := KubernetesSource([]byte(`{"events":[{"service":"checkout","type":"Warning","reason":"OOMKilled","message":"mem","timestamp":"2026-08-18T10:00:00Z"}]}`))
	if err != nil {
		t.Fatalf("KubernetesSource: %v", err)
	}
	evs := src.Events("checkout")
	if len(evs) != 1 || !evs[0].IsError() {
		t.Fatalf("expected one error event, got %+v", evs)
	}
}

func TestPrometheusSourceFeedsEvents(t *testing.T) {
	src, err := PrometheusSource([]byte("cpu{service=\"worker\"} 0.5\n"))
	if err != nil {
		t.Fatalf("PrometheusSource: %v", err)
	}
	evs := src.Events("worker")
	if len(evs) != 1 || evs[0].Type != EventMetric {
		t.Fatalf("expected one metric event, got %+v", evs)
	}
}

func TestParseOtelDeterministicOrder(t *testing.T) {
	doc := `{"logs":[
		{"service":"a","timestamp":"2026-08-18T10:00:00Z","body":"one"},
		{"service":"b","timestamp":"2026-08-18T10:00:00Z","body":"two"}
	]}`
	events, err := ParseOtel([]byte(doc))
	if err != nil {
		t.Fatalf("ParseOtel: %v", err)
	}
	if len(events) != 2 || events[0].Service != "a" || events[1].Service != "b" {
		t.Fatalf("order not preserved: %+v", events)
	}
}
