package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// firstSymbolNodeID returns the ID of the first symbol node in the app's
// graph, which is a valid input for /v1/context and /v1/risk. It requires the
// fixture (built by fixtureRoot/newTestApp) to yield at least one symbol.
func firstSymbolNodeID(t *testing.T, app *App) string {
	t.Helper()
	for _, n := range app.graph.Nodes {
		if n.Symbol != nil && n.ID != "" {
			return n.ID
		}
	}
	t.Fatal("fixture graph contains no symbol node")
	return ""
}

func TestV1ContextEndpoint(t *testing.T) {
	app := newTestApp(t)
	sym := firstSymbolNodeID(t, app)
	rec := postJSON(t, app, "/v1/context", `{"change":"`+sym+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var pkt domain.ContextPacket
	if err := json.Unmarshal(rec.Body.Bytes(), &pkt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The packet's Task field reflects the requested change.
	if !strings.Contains(pkt.Task, sym) {
		t.Fatalf("packet task %q does not reflect change %q", pkt.Task, sym)
	}
}

func TestV1RiskEndpoint(t *testing.T) {
	app := newTestApp(t)
	sym := firstSymbolNodeID(t, app)
	rec := postJSON(t, app, "/v1/risk", `{"change":"`+sym+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["risks"]; !ok {
		t.Fatal("response body has no \"risks\" key")
	}
	var change string
	if err := json.Unmarshal(body["change"], &change); err != nil {
		t.Fatalf("decode change: %v", err)
	}
	if change != sym {
		t.Fatalf("change = %q, want %q", change, sym)
	}
}

func TestV1TaskNotFound(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/v1/tasks/unknown-id")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error == "" {
		t.Fatal("error message is empty")
	}
}
