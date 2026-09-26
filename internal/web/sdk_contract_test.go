package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/sdk"
)

// TestSDKVerifyRoundTrip proves the SDK sends types as a JSON array that the
// server's /v1/verify decoder honors — the same contract handleV1Verify's
// decodeVerifyTypesBody implements — so a mismatch (e.g. the SDK sending a
// shape the server silently drops) fails loudly instead of falling back to
// the default. The handler mirrors decodeVerifyTypesBody's rules (array of
// strings, or a single comma-separated string); the SDK package's own
// TestVerifyTypesServerHonors pins the same contract from the client side.
//
// It lives in the external web_test package because internal/web's in-package
// test build cannot import internal/sdk: the SDK passthrough made sdk depend
// on internal/mcp, which pulls mcp → org → enterprise → web.
func TestSDKVerifyRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Types []string `json:"types"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid types"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"types": req.Types})
	}))
	t.Cleanup(srv.Close)

	client := sdk.New(srv.URL)
	res, err := client.Verify([]string{"build", "test"})
	if err != nil {
		t.Fatalf("sdk Verify returned error: %v", err)
	}
	types, ok := res["types"].([]any)
	if !ok || len(types) != 2 || types[0] != "build" || types[1] != "test" {
		t.Errorf("server decoded types = %v, want [build test]", res["types"])
	}
}
