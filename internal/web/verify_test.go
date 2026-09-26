package web

import (
	"strings"
	"testing"
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

// TestSDKVerifyRoundTrip moved to sdk_contract_test.go (external web_test
// package): internal/web's in-package test build cannot import internal/sdk
// since the SDK passthrough made sdk depend on internal/mcp (sdk → mcp →
// org → enterprise → web cycle).
