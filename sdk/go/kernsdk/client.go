// Package kernsdk is the Go SDK for kern-server's REST API. It is
// a thin, stdlib-only HTTP client over the SAME /v1 control-plane endpoints
// the Python and TypeScript SDKs use — and those delegate to the same
// TaskService application services the CLI and MCP server use, so every
// interface shares one control plane ( exit gate).
package kernsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DefaultBaseURL is the kern-server default listen address.
const DefaultBaseURL = "http://localhost:8090"

// Client is the kern REST SDK client.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New creates a client. A nil httpClient defaults to a 10s-timeout client.
func New(baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{BaseURL: baseURL, HTTP: httpClient}
}

// Err is an API error carrying the HTTP status.
type Err struct {
	Status int
	Body   string
}

func (e *Err) Error() string {
	return fmt.Sprintf("kernsdk: HTTP %d: %s", e.Status, e.Body)
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return &Err{Status: resp.StatusCode, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Health checks the server.
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/api/health", &out)
	return out, err
}

// Analyze produces a context packet for a change (POST /v1/analyze).
func (c *Client) Analyze(ctx context.Context, change string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/analyze", map[string]string{"change": change}, &out)
	return out, err
}

// Plan produces an implementation plan for a change (POST /v1/plan).
func (c *Client) Plan(ctx context.Context, change string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/plan", map[string]string{"change": change}, &out)
	return out, err
}

// WhatIf simulates a change (POST /v1/what-if).
func (c *Client) WhatIf(ctx context.Context, kind, change, newTarget string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/what-if", map[string]string{
		"kind": kind, "change": change, "new_target": newTarget,
	}, &out)
	return out, err
}

// Impact computes change impact (POST /v1/impact).
func (c *Client) Impact(ctx context.Context, change string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/impact", map[string]string{"change": change}, &out)
	return out, err
}

// Verify runs verification checks (POST /v1/verify).
func (c *Client) Verify(ctx context.Context, types []string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/verify", map[string]any{"types": types}, &out)
	return out, err
}

// InvestigateIncident ingests an alert and produces an incident report.
func (c *Client) InvestigateIncident(ctx context.Context, alert any) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/incidents/investigate", map[string]any{"alert": alert}, &out)
	return out, err
}

// MemoryList returns engineering memories (GET /v1/memory). The server
// responds with an object; the returned map's "memories" key (when present)
// holds the list.
func (c *Client) MemoryList(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/v1/memory", &out)
	return out, err
}

// MemoryAdd stores an engineering memory (POST /v1/memory).
func (c *Client) MemoryAdd(ctx context.Context, content, memType, scope string, tags []string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/memory", map[string]any{
		"content": content, "type": memType, "scope": scope, "tags": tags,
	}, &out)
	return out, err
}

// Graph returns the neighborhood of an entity (GET /v1/graph/{entity}).
func (c *Client) Graph(ctx context.Context, entity string) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/v1/graph/"+url.PathEscape(entity), &out)
	return out, err
}

// Context builds the context packet for a change (POST /v1/context).
func (c *Client) Context(ctx context.Context, change string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/context", map[string]string{"change": change}, &out)
	return out, err
}

// Risk assesses a change's risk (POST /v1/risk).
func (c *Client) Risk(ctx context.Context, change string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/risk", map[string]string{"change": change}, &out)
	return out, err
}

// Task fetches a task by ID (GET /v1/tasks/{id}).
func (c *Client) Task(ctx context.Context, taskID string) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/v1/tasks/"+url.PathEscape(taskID), &out)
	return out, err
}

// Agents lists the specialist team roster (POST /v1/agents). The server
// responds with an object whose "specialists" key holds the roster.
func (c *Client) Agents(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/agents", map[string]any{}, &out)
	return out, err
}

// Loop runs the closed autonomy loop (POST /v1/loop).
func (c *Client) Loop(ctx context.Context, intent, level string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/loop", map[string]string{"intent": intent, "level": level}, &out)
	return out, err
}

// TaskSubmit submits a task (POST /v1/tasks).
func (c *Client) TaskSubmit(ctx context.Context, input, taskType string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/tasks", map[string]string{"input": input, "type": taskType}, &out)
	return out, err
}

// Execute runs a patch in a sandbox (POST /v1/execute).
func (c *Client) Execute(ctx context.Context, patch string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/execute", map[string]string{"patch": patch}, &out)
	return out, err
}

// Correlate correlates an alert (POST /v1/correlate).
func (c *Client) Correlate(ctx context.Context, alert any, snapshot string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/correlate", map[string]any{"alert": alert, "snapshot": snapshot}, &out)
	return out, err
}

// Learn extracts learning patterns (POST /v1/learn).
func (c *Client) Learn(ctx context.Context, threshold int) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/learn", map[string]int{"threshold": threshold}, &out)
	return out, err
}

// Modernize runs modernization analysis (POST /v1/modernize).
func (c *Client) Modernize(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/modernize", map[string]any{}, &out)
	return out, err
}

// ArtifactsList lists artifacts (GET /v1/artifacts).
func (c *Client) ArtifactsList(ctx context.Context) ([]any, error) {
	var out []any
	err := c.get(ctx, "/v1/artifacts", &out)
	return out, err
}

// ArtifactGet fetches an artifact by ID (GET /v1/artifacts/{id}).
func (c *Client) ArtifactGet(ctx context.Context, artifactID string) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/v1/artifacts/"+url.PathEscape(artifactID), &out)
	return out, err
}

// Audit returns a task's audit trail (GET /v1/audit/{taskID}).
func (c *Client) Audit(ctx context.Context, taskID string) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/v1/audit/"+url.PathEscape(taskID), &out)
	return out, err
}

// Approve resolves a pending approval (POST /v1/approve).
func (c *Client) Approve(ctx context.Context, approvalID, approver string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/approve", map[string]string{"id": approvalID, "approver": approver}, &out)
	return out, err
}

// Reject rejects a pending approval (POST /v1/reject).
func (c *Client) Reject(ctx context.Context, approvalID, approver string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/reject", map[string]string{"id": approvalID, "approver": approver}, &out)
	return out, err
}

// Deploy deploys a task (POST /v1/tasks/{id}/deploy).
func (c *Client) Deploy(ctx context.Context, taskID, version string) (map[string]any, error) {
	var out map[string]any
	err := c.post(ctx, "/v1/tasks/"+url.PathEscape(taskID)+"/deploy", map[string]string{"version": version}, &out)
	return out, err
}
