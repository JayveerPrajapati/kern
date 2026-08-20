// Package sdk is a thin stdlib-only Go client for the kern-server REST API.
package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// DefaultBaseURL is used when a client is created with an empty base URL.
const DefaultBaseURL = "http://localhost:8090"

// maxResponseBody caps how much of a server response the client will read,
// protecting against an unbounded (malicious or buggy) response body.
const maxResponseBody = 10 << 20 // 10 MB

// Client is a typed client for the kern-server REST API.
type Client struct {
	base string // e.g. "http://localhost:8090"
	http *http.Client
}

// New returns a Client for the given base URL (e.g. "http://localhost:8090").
// A trailing "/" is stripped and an empty base defaults to DefaultBaseURL.
func New(baseURL string) *Client {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		base: base,
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetTimeout overrides the HTTP client timeout (default 10s).
func (c *Client) SetTimeout(d time.Duration) {
	c.http.Timeout = d
}

// AnalyzeResult is the typed payload returned by /v1/analyze and /v1/plan.
type AnalyzeResult struct {
	Packet domain.ContextPacket `json:"packet"`
	Text   string               `json:"text"`
}

// Analyze calls POST /v1/analyze.
func (c *Client) Analyze(change string) (*AnalyzeResult, error) {
	var out AnalyzeResult
	if err := c.postJSON("/v1/analyze", map[string]string{"change": change}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Plan calls POST /v1/plan.
func (c *Client) Plan(change string) (*AnalyzeResult, error) {
	var out AnalyzeResult
	if err := c.postJSON("/v1/plan", map[string]string{"change": change}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WhatIf calls POST /v1/what-if and returns the parsed JSON as a map.
func (c *Client) WhatIf(change, kind, newTarget string) (map[string]any, error) {
	body := map[string]string{"change": change, "kind": kind}
	if newTarget != "" {
		body["new_target"] = newTarget
	}
	return c.postJSONMap("/v1/what-if", body)
}

// Impact calls POST /v1/impact.
func (c *Client) Impact(change string) (map[string]any, error) {
	return c.postJSONMap("/v1/impact", map[string]string{"change": change})
}

// Verify calls POST /v1/verify.
func (c *Client) Verify(types []string) (map[string]any, error) {
	return c.postJSONMap("/v1/verify", map[string]any{"types": types})
}

// IncidentInvestigation is the decoded response of POST
// /v1/incidents/investigate: the resulting incident, its hypotheses and the
// affected service.
type IncidentInvestigation struct {
	Incident        domain.Incident     `json:"incident"`
	Hypotheses      []domain.Hypothesis `json:"hypotheses"`
	AffectedService string              `json:"affected_service"`
}

// InvestigateIncident runs the incident engine (Workflow D) against an alert —
// IngestAlert + Correlate + RootCause — and returns the resulting incident,
// its hypotheses and the affected service (POST /v1/incidents/investigate).
func (c *Client) InvestigateIncident(alert domain.Alert) (*IncidentInvestigation, error) {
	var out IncidentInvestigation
	if err := c.postJSON("/v1/incidents/investigate", map[string]any{"alert": alert}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MemoryList calls GET /v1/memory.
func (c *Client) MemoryList() (map[string]any, error) {
	return c.getJSONMap("/v1/memory")
}

// MemoryAdd calls POST /v1/memory.
func (c *Client) MemoryAdd(content, memType, scope string, tags []string) (map[string]any, error) {
	body := map[string]any{
		"content": content,
		"type":    memType,
		"scope":   scope,
		"tags":    tags,
	}
	return c.postJSONMap("/v1/memory", body)
}

// Graph calls GET /v1/graph/{entity}.
func (c *Client) Graph(entity string) (map[string]any, error) {
	return c.getJSONMap("/v1/graph/" + url.PathEscape(entity))
}

// Context calls POST /v1/context and returns the raw context packet JSON as a map.
func (c *Client) Context(change string) (map[string]any, error) {
	return c.postJSONMap("/v1/context", map[string]string{"change": change})
}

// Risk calls POST /v1/risk and returns the parsed {"risks":...,"change":...} map.
func (c *Client) Risk(change string) (map[string]any, error) {
	return c.postJSONMap("/v1/risk", map[string]string{"change": change})
}

// Task calls GET /v1/tasks/{id}. A 404 surfaces as an error.
func (c *Client) Task(id string) (map[string]any, error) {
	return c.getJSONMap("/v1/tasks/" + url.PathEscape(id))
}

// Agents calls POST /v1/agents and returns the standard specialist team roster
// plus the current task states from the agent registry.
func (c *Client) Agents() (map[string]any, error) {
	return c.postJSONMap("/v1/agents", map[string]any{})
}

// Loop calls POST /v1/loop to run the closed autonomy loop against an intent.
// level is the autonomy level ("L0".."L5"); an empty level defaults to L0
// (read-only). The response carries the stage timeline plus the
// deployed / observed-healthy / learned outcome.
func (c *Client) Loop(intent, level string) (map[string]any, error) {
	return c.postJSONMap("/v1/loop", map[string]any{"intent": intent, "level": level})
}

// TaskSubmit calls POST /v1/tasks to submit a new task to the agent registry
// and returns its id and initial state.
func (c *Client) TaskSubmit(input, typ string) (map[string]any, error) {
	return c.postJSONMap("/v1/tasks", map[string]any{"input": input, "type": typ})
}

// postJSON marshals body, POSTs it to path, and decodes the response into out.
func (c *Client) postJSON(path string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

// postJSONMap is like postJSON but decodes the response into a map.
func (c *Client) postJSONMap(path string, body any) (map[string]any, error) {
	var out map[string]any
	if err := c.postJSON(path, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// getJSONMap GETs path and decodes the response into a map.
func (c *Client) getJSONMap(path string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// do sends req and decodes a 2xx JSON response into out. On a non-2xx status it
// returns an error including the status code and the server's {"error": ...}
// message if present.
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("kern sdk: %s %s returned %s: %s", req.Method, req.URL.Path, resp.Status, readErrorBody(io.LimitReader(resp.Body, maxResponseBody)))
	}

	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
			return fmt.Errorf("kern sdk: decode response for %s: %w", req.URL.Path, err)
		}
	}
	return nil
}

// readErrorBody reads the response body to surface a server-provided
// {"error": ...} message in the returned error.
func readErrorBody(r io.Reader) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(r).Decode(&payload); err != nil || payload.Error == "" {
		return "no error detail"
	}
	return payload.Error
}
