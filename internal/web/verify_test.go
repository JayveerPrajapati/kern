package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/sdk"
)

// TestDecodeVerifyTypesBody covers the /v1/verify request-body decoder: the
// canonical array shape, the legacy comma-separated string, an absent field,
// and that malformed bodies surface a decode error instead of being silently
// swallowed.
func TestDecodeVerifyTypesBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []string
		wantErr bool
	}{
		{"array", `{"types":["build","test"]}`, []string{"build", "test"}, false},
		{"single element array", `{"types":["build"]}`, []string{"build"}, false},
		{"legacy comma string", `{"types":"build,test"}`, []string{"build", "test"}, false},
		{"legacy single string", `{"types":"security"}`, []string{"security"}, false},
		{"empty array", `{"types":[]}`, nil, false},
		{"absent field", `{}`, nil, false},
		{"extra whitespace", `{"types":[" build ","test"]}`, []string{"build", "test"}, false},
		{"malformed body", `{"types":}`, nil, true},
		{"not json", `not-json`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeVerifyTypesBody(strings.NewReader(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("decodeVerifyTypesBody(%q) = %v, want error", tc.body, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeVerifyTypesBody(%q) returned error: %v", tc.body, err)
			}
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("decodeVerifyTypesBody(%q) = %v, want empty", tc.body, got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("decodeVerifyTypesBody(%q) = %v, want %v", tc.body, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("decodeVerifyTypesBody(%q)[%d] = %q, want %q", tc.body, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestSDKVerifyRoundTrip proves the SDK sends types as a JSON array that the
// server's real /v1/verify decoder (decodeVerifyTypesBody) honors — the same
// decoder handleV1Verify uses. It exercises the actual SDK client against the
// actual server-side decode so a mismatch (e.g. the SDK sending a shape the
// server silently drops) would fail loudly instead of falling back to the
// default.
func TestSDKVerifyRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		types, err := decodeVerifyTypesBody(r.Body)
		if err != nil {
			http.Error(w, `{"error":"invalid types"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"types": types})
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
