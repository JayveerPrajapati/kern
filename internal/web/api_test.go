package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	// Alias the internal/app import: the web test package already names its
	// App instances `app`, so the package-level identifier would collide.
	kernapp "github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
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

func TestV1AnalyzeMissingSymbol(t *testing.T) {
	app := newTestApp(t)
	rec := postJSON(t, app, "/v1/analyze", `{"change":"Add a SaveUser function to the service layer"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if !strings.Contains(body["error"], "no symbol named") {
		t.Fatalf("body must carry the real message, got: %s", rec.Body.String())
	}
	if !strings.Contains(body["error"], "candidates") {
		t.Fatalf("body must carry candidate hints, got: %s", rec.Body.String())
	}
}

func TestV1AnalyzeNoSymbolIdentified(t *testing.T) {
	app := newTestApp(t)
	rec := postJSON(t, app, "/v1/analyze", `{"change":"!!! ??? ###"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if !strings.Contains(body["error"], "could not identify a symbol") {
		t.Fatalf("body must carry the real message, got: %s", rec.Body.String())
	}
}

func TestV1AnalyzeValidSymbolStill200(t *testing.T) {
	app := newTestApp(t)
	rec := postJSON(t, app, "/v1/analyze", `{"change":"helper"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestV1AnalyzePersistsTaskRecord locks the F9 regression fix on the web
// surface: handleV1Analyze's comment promises an authoritative Task record is
// created and the response returns the TaskID, so the record must be written
// to the persisted store — a fresh TaskService (a new process) must resolve
// the returned TaskID via Get.
func TestV1AnalyzePersistsTaskRecord(t *testing.T) {
	app := newTestApp(t)
	rec := postJSON(t, app, "/v1/analyze", `{"change":"helper"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp v1AnalyzeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if resp.TaskID == "" {
		t.Fatal("v1/analyze response has no task_id")
	}
	if !strings.HasPrefix(resp.TaskID, "t-") {
		t.Fatalf("task_id = %q, want store-assigned t-<n> (authoritative record)", resp.TaskID)
	}
	// A fresh service reads the same persisted store `kern task <id>` reads.
	fresh := kernapp.NewTaskService(app.platform, eventbus.New())
	if got, ok := fresh.Get(resp.TaskID); !ok {
		t.Fatalf("task %q not queryable from a fresh TaskService after POST /v1/analyze", resp.TaskID)
	} else if got.State == "" {
		t.Fatalf("task %q loaded from store has no state", resp.TaskID)
	}
}

func TestV1GraphSymbolNameResolution(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                      "module example.com/demo\n\ngo 1.20\n",
		"internal/repo/repo.go":       "package repo\n\nfunc Query(id int) string { return \"u\" }\n",
		"internal/service/service.go": "package service\n\nimport \"example.com/demo/internal/repo\"\n\nfunc FindUser(id int) string {\n\treturn repo.Query(id)\n}\n",
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app, err := New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	rec := get(t, app, "/v1/graph/FindUser")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Node struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Symbol *struct {
				Name string `json:"name"`
			} `json:"symbol"`
		} `json:"node"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if body.Node.Kind != "symbol" || body.Node.Symbol == nil || body.Node.Symbol.Name != "FindUser" {
		t.Fatalf("expected FindUser symbol node, got %+v", body.Node)
	}
}

func TestV1GraphUnknownEntityStill404(t *testing.T) {
	app := newTestApp(t)
	rec := get(t, app, "/v1/graph/NoSuchEntityAnywhere")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestV1ExecuteInvalidPatchReturns400 pins the invalid-patch HTTP contract:
// a garbage / non-applicable patch is a CLIENT error and must return 400 (not
// the 500 "internal error" QA reproduced), with a message that names the
// patch failure. The empty-patch 400 and the valid-patch 200 contracts are
// asserted alongside so all three branches of /v1/execute stay pinned.
func TestV1ExecuteInvalidPatchReturns400(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1") // Execute runs under the governance exec gate
	app := newTestApp(t)

	// 1. Empty patch → 400 "patch is required" (pre-existing contract).
	rec := postJSON(t, app, "/v1/execute", `{"patch": ""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /v1/execute empty patch = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "patch is required") {
		t.Fatalf("empty patch body = %s, want %q", rec.Body.String(), "patch is required")
	}

	// 2. Garbage patch → 400 (NOT 500), message names the patch failure.
	rec = postJSON(t, app, "/v1/execute", `{"patch": "this is not a patch at all"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /v1/execute garbage patch = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "patch") {
		t.Fatalf("garbage patch body = %s, want it to mention the patch failure", rec.Body.String())
	}

	// 3. A real patch -> 200 with the diff. json.Marshal escapes the literal
	// tabs in the patch so the JSON body round-trips them correctly (raw tabs
	// are invalid inside JSON strings).
	patch := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,5 +1,5 @@
 package main

 func helper() string {
-	return "h"
+	return "hh"
 }
`
	bodyBytes, err := json.Marshal(struct {
		Patch string `json:"patch"`
	}{Patch: patch})
	if err != nil {
		t.Fatalf("marshal patch body: %v", err)
	}
	rec = postJSON(t, app, "/v1/execute", string(bodyBytes))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/execute valid patch = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode valid-patch response: %v (body: %s)", err, rec.Body.String())
	}
	if !strings.Contains(body.Diff, "main.go") {
		t.Fatalf("valid-patch diff = %q, want it to mention main.go", body.Diff)
	}
}
