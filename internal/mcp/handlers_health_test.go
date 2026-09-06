package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

func TestHandleHealthTool(t *testing.T) {
	// Record some sample metrics to verify they appear in health snapshot
	metrics.Default().RecordCacheHit()
	metrics.Default().RecordCacheMiss()
	metrics.Default().RecordApproval()

	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{t.TempDir()}
	s.audit = governance.NewAuditLog()
	s.audit.Record(governance.AuditEntry{
		Action: "test_action",
	})

	out, err := s.handleHealth(context.Background(), map[string]any{"root": s.roots[0]})
	if err != nil {
		t.Fatalf("handleHealth error: %v", err)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("unmarshal health JSON: %v; raw=%s", err, out)
	}

	if data["status"] != "ok" {
		t.Errorf("status = %v, want 'ok'", data["status"])
	}

	toolsMap, ok := data["tools"].(map[string]any)
	if !ok {
		t.Fatalf("missing or invalid 'tools' object: %v", data["tools"])
	}
	if registered, ok := toolsMap["registered"].(float64); !ok || registered < 50 {
		t.Errorf("tools.registered = %v, expected >= 50", toolsMap["registered"])
	}

	cacheMap, ok := data["cache"].(map[string]any)
	if !ok {
		t.Fatalf("missing or invalid 'cache' object: %v", data["cache"])
	}
	if hits, ok := cacheMap["hits"].(float64); !ok || hits < 1 {
		t.Errorf("cache.hits = %v, expected >= 1", cacheMap["hits"])
	}

	govMap, ok := data["governance"].(map[string]any)
	if !ok {
		t.Fatalf("missing or invalid 'governance' object: %v", data["governance"])
	}
	if auditLen, ok := govMap["audit_chain_length"].(float64); !ok || auditLen < 1 {
		t.Errorf("governance.audit_chain_length = %v, expected >= 1", govMap["audit_chain_length"])
	}
}

func TestHttpHealthEndpoint(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{t.TempDir()}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		res, err := s.handleHealth(r.Context(), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, res+"\n")
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var data map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatalf("failed to decode JSON response from /health: %v", err)
	}
	if data["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", data["status"])
	}
}
