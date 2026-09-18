package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/index"
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

// TestHandleHealthReportsLowEdgeCounters: the index section of the health
// snapshot carries the finalize-time LOW-edge reconciliation counters
// (CG-P0-5), so an agent can see at a glance how many phantom references the
// index admits.
func TestHandleHealthReportsLowEdgeCounters(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{dir}
	ix, err := s.loadIndex(context.Background(), dir)
	if err != nil {
		t.Fatalf("loadIndex: %v", err)
	}
	// The counters are recorded on the index by the build finalize; the
	// handler must surface them in the snapshot regardless of their value.
	if ix.PromotedLowEdges < 0 || ix.UnresolvedLowEdges < 0 {
		t.Fatal("build should record non-negative reconciliation counters")
	}
	out, err := s.handleHealth(context.Background(), map[string]any{"root": dir})
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("unmarshal health JSON: %v", err)
	}
	idx, ok := data["index"].(map[string]any)
	if !ok {
		t.Fatalf("missing index section: %v", data)
	}
	if _, ok := idx["promoted_low_edges"]; !ok {
		t.Errorf("index section missing promoted_low_edges: %v", idx)
	}
	if _, ok := idx["unresolved_low_edges"]; !ok {
		t.Errorf("index section missing unresolved_low_edges: %v", idx)
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

// TestHandleHealthFallsBackToDiskIndex: a server with a cold session cache
// (no index ever loaded in-process) must report the persisted disk index in
// the "index" block instead of all-zero "not built" values — otherwise agents
// reading health conclude the index is missing and rebuild it. Regression for
// the dogfooding finding where a long-lived MCP server reported
// fresh=false/symbols=0/files=0 while searches returned fresh disk data.
func TestHandleHealthFallsBackToDiskIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("index build: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("index save: %v", err)
	}

	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{dir}
	// Deliberately do NOT call s.loadIndex: the session cache must stay cold
	// so the handler exercises the disk fallback path.

	out, err := s.handleHealth(context.Background(), map[string]any{"root": dir})
	if err != nil {
		t.Fatalf("handleHealth: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		t.Fatalf("unmarshal health JSON: %v", err)
	}
	idx, ok := data["index"].(map[string]any)
	if !ok {
		t.Fatalf("missing index block: %v", data)
	}
	if idx["built"] != true {
		t.Errorf("index.built = %v, want true (disk fallback)", idx["built"])
	}
	if idx["fresh"] != true {
		t.Errorf("index.fresh = %v, want true", idx["fresh"])
	}
	if n, _ := idx["symbols"].(float64); n <= 0 {
		t.Errorf("index.symbols = %v, want > 0", idx["symbols"])
	}
	if idx["note"] == nil {
		t.Errorf("expected fallback note in index block, got %v", idx)
	}
	if _, ok := idx["builds_count"]; !ok {
		t.Errorf("index block missing builds_count: %v", idx)
	}
}
