package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// liveSource is the shared base for all live polling adapters. It holds an
// HTTP client, a mutex-guarded store, and a stop channel. Concrete adapters
// (Prometheus, OTel, K8s) provide a fetch function that returns raw bytes for
// the adapter's specific endpoint, and the base polls it on an interval.
type liveSource struct {
	name     string
	client   *http.Client
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup

	mu    sync.RWMutex
	store *Store
}

// newLiveSource creates the shared base. The poll loop is NOT started here —
// the caller starts it via start() after wiring the fetch function.
func newLiveSource(name string, interval time.Duration, timeout time.Duration) *liveSource {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &liveSource{
		name:     name,
		client:   &http.Client{Timeout: timeout},
		interval: interval,
		stop:     make(chan struct{}),
		store:    NewStore(),
	}
}

// Name implements Source.
func (s *liveSource) Name() string { return s.name }

// Events implements Source (thread-safe).
func (s *liveSource) Events(service string) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store.Events(service)
}

// Deployments implements Source (thread-safe).
func (s *liveSource) Deployments(service string) []domain.Deployment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store.Deployments(service)
}

// Commits implements Source (thread-safe).
func (s *liveSource) Commits() []Commit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store.Commits()
}

// fetchAndIngest calls the fetch function, parses the result, and ingests into
// the store under a write lock. Errors are non-fatal (logged via the error
// channel or ignored — the adapter retries on the next interval).
func (s *liveSource) fetchAndIngest(fetch func() ([]byte, error), parse func([]byte) error) {
	data, err := fetch()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = parse(data) // parse functions ingest into s.store
}

// start launches the poll goroutine. Call exactly once.
func (s *liveSource) start(fetch func() ([]byte, error), parse func([]byte) error) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// Fetch immediately on start.
		s.fetchAndIngest(fetch, parse)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.fetchAndIngest(fetch, parse)
			case <-s.stop:
				return
			}
		}
	}()
}

// Close stops the poll loop and waits for it to exit. Safe to call once.
func (s *liveSource) Close() {
	close(s.stop)
	s.wg.Wait()
}

// ---------------------------------------------------------------------------
// Live Prometheus Source
// ---------------------------------------------------------------------------

// LivePrometheusSource polls a Prometheus HTTP API endpoint on an interval,
// fetches metric data, and exposes it as a Source. It reuses ParsePrometheus
// to parse the text-exposition format.
// The endpoint should return Prometheus text-exposition format (e.g.
// /api/v1/query?query=up or a metrics scrape endpoint).
type LivePrometheusSource struct {
	*liveSource
}

// NewLivePrometheusSource creates a live Prometheus source that polls the
// given URL (e.g. "http://prometheus:9090/api/v1/query?query=up") on the given
// interval. A nil/empty URL returns nil (caller should check). The source
// starts polling immediately.
func NewLivePrometheusSource(url string, interval time.Duration) *LivePrometheusSource {
	if url == "" {
		return nil
	}
	base := newLiveSource("prometheus", interval, 10*time.Second)
	src := &LivePrometheusSource{liveSource: base}
	fetch := func() ([]byte, error) {
		return httpGet(base.client, url)
	}
	parse := func(data []byte) error {
		events, err := ParsePrometheus(data)
		if err != nil {
			return err
		}
		base.store.IngestAll(events)
		return nil
	}
	base.start(fetch, parse)
	return src
}

// ---------------------------------------------------------------------------
// Live OpenTelemetry Source
// ---------------------------------------------------------------------------

// LiveOtelSource polls an OTLP HTTP collector endpoint on an interval, fetches
// telemetry, and exposes it as a Source. It reuses ParseOtel to parse the
// OTLP-style JSON document.
type LiveOtelSource struct {
	*liveSource
}

// NewLiveOtelSource creates a live OTel source that polls the given URL
// (e.g. "http://otel-collector:4318/v1/metrics") on the given interval. The
// endpoint should return OTLP-style JSON. The source starts polling immediately.
func NewLiveOtelSource(url string, interval time.Duration) *LiveOtelSource {
	if url == "" {
		return nil
	}
	base := newLiveSource("otel", interval, 10*time.Second)
	src := &LiveOtelSource{liveSource: base}
	fetch := func() ([]byte, error) {
		return httpGet(base.client, url)
	}
	parse := func(data []byte) error {
		events, err := ParseOtel(data)
		if err != nil {
			return err
		}
		base.store.IngestAll(events)
		return nil
	}
	base.start(fetch, parse)
	return src
}

// ---------------------------------------------------------------------------
// Live Kubernetes Source
// ---------------------------------------------------------------------------

// LiveKubernetesSource polls the Kubernetes API for events and deployments on
// an interval, and exposes them as a Source. It reuses ParseKubernetes to parse
// the JSON. Authentication is via a bearer token (read from the
// KERN_K8S_TOKEN env var or the service-account token file). The API server
// URL defaults to the in-cluster address but can be overridden.
type LiveKubernetesSource struct {
	*liveSource
}

// NewLiveKubernetesSource creates a live K8s source. The apiServer URL should
// be the Kubernetes API base (e.g. "https://kubernetes.default.svc"). The
// token is the bearer token for authentication (read from
// KERN_K8S_TOKEN or /var/run/secrets/kubernetes.io/serviceaccount/token).
// The namespace limits the events query (empty = all namespaces).
// The source polls two endpoints:
// - /apis/apps/v1/namespaces/{ns}/deployments (deployments)
// - /api/v1/namespaces/{ns}/events (events)
// Both are fetched on each poll, parsed, and ingested into the store.
func NewLiveKubernetesSource(apiServer, token, namespace string, interval time.Duration) *LiveKubernetesSource {
	if apiServer == "" {
		return nil
	}
	base := newLiveSource("kubernetes", interval, 10*time.Second)
	src := &LiveKubernetesSource{liveSource: base}

	fetch := func() ([]byte, error) {
		// Fetch deployments and events, merge into a single kubernetesDoc JSON.
		ns := namespace
		if ns == "" {
			ns = "default"
		}
		depURL := fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/deployments", apiServer, ns)
		evURL := fmt.Sprintf("%s/api/v1/namespaces/%s/events", apiServer, ns)

		var depData, evData []byte
		if token != "" {
			depData, _ = httpGetAuth(base.client, depURL, token)
			evData, _ = httpGetAuth(base.client, evURL, token)
		} else {
			depData, _ = httpGet(base.client, depURL)
			evData, _ = httpGet(base.client, evURL)
		}
		// Merge into a single JSON doc that ParseKubernetes can parse.
		// We wrap both responses into the expected {deployments:[],events:[]} shape.
		merged := mergeKubeResponses(depData, evData)
		return merged, nil
	}
	parse := func(data []byte) error {
		deployments, events, err := ParseKubernetes(data)
		if err != nil {
			return err
		}
		base.store.IngestAll(events)
		for _, d := range deployments {
			base.store.AddDeployment(d)
		}
		return nil
	}
	base.start(fetch, parse)
	return src
}

// mergeKubeResponses merges raw K8s API responses (which have different shapes:
// {items:[...]} for deployments and events) into the flat {deployments:[],events:[]}
// shape that ParseKubernetes expects. Best-effort: if parsing fails, returns
// an empty doc.
func mergeKubeResponses(depJSON, evJSON []byte) []byte {
	// K8s API returns {items:[{metadata:{name:..},spec:{template:{...}}},...]}
	// We extract the fields ParseKubernetes expects.
	// This is intentionally simple: we just build the flat doc.
	var result struct {
		Deployments []kubeDeployment `json:"deployments"`
		Events      []kubeEvent      `json:"events"`
	}
	// Parse deployments (best-effort).
	if len(depJSON) > 0 {
		var depResp struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					CreatedAt string `json:"creationTimestamp"`
				} `json:"metadata"`
				Spec struct {
					Template struct {
						Metadata struct {
							Labels map[string]string `json:"labels"`
						} `json:"metadata"`
					} `json:"template"`
				} `json:"spec"`
			} `json:"items"`
		}
		if err := json.Unmarshal(depJSON, &depResp); err == nil {
			for _, item := range depResp.Items {
				svc := item.Spec.Template.Metadata.Labels["app"]
				if svc == "" {
					svc = item.Spec.Template.Metadata.Labels["service"]
				}
				result.Deployments = append(result.Deployments, kubeDeployment{
					Name:      item.Metadata.Name,
					Service:   svc,
					Version:   item.Metadata.Name, // best-effort
					Timestamp: item.Metadata.CreatedAt,
				})
			}
		}
	}
	// Parse events (best-effort).
	if len(evJSON) > 0 {
		var evResp struct {
			Items []struct {
				Metadata struct {
					Namespace string `json:"namespace"`
					CreatedAt string `json:"creationTimestamp"`
				} `json:"metadata"`
				InvolvedObject struct {
					Name string `json:"name"`
				} `json:"involvedObject"`
				Type    string `json:"type"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"items"`
		}
		if err := json.Unmarshal(evJSON, &evResp); err == nil {
			for _, item := range evResp.Items {
				result.Events = append(result.Events, kubeEvent{
					Namespace: item.Metadata.Namespace,
					Service:   item.InvolvedObject.Name,
					Type:      item.Type,
					Reason:    item.Reason,
					Message:   item.Message,
					Timestamp: item.Metadata.CreatedAt,
				})
			}
		}
	}
	out, _ := json.Marshal(result)
	return out
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

// httpGet performs a simple GET request and returns the body bytes.
func httpGet(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("live source: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("live source: %s returned %d: %s", url, resp.StatusCode, string(body))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB cap
}

// httpGetAuth performs a GET request with a bearer token.
func httpGetAuth(client *http.Client, url, token string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("live source: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("live source: %s returned %d: %s", url, resp.StatusCode, string(body))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10<<20))
}
