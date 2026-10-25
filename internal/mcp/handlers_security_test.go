package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestSecuritySvcMask verifies the adapter facade redacts a high-entropy
// fake token via pii.DefaultPatterns. The sk-test-… dash form matches the
// STRIPE_DASH pattern. sk-abc123 and AKIAIOSFODNN7EXAMPLE are
// scanner-allowlisted example literals and must NOT be used here — a real
// credential never equals a published example. Note the input must not
// carry a token=/secret= keyword prefix: the TOKEN pattern
// (\b(?:token|secret)...[=:]...) wins the greedy longest-match over the
// value's own STRIPE_DASH label.
func TestSecuritySvcMask(t *testing.T) {
	t.Parallel()
	token := "sk-test-abcdefghijklmnopqrstuvwxyz012345"
	text := "credentials " + token + " for staging"
	res, err := (securitySvc{}).Mask(context.Background(), text, nil)
	if err != nil {
		t.Fatalf("Mask error: %v", err)
	}
	if res.Replaced < 1 {
		t.Errorf("Mask replaced %d secrets, want >= 1 (ByLabel: %v)", res.Replaced, res.ByLabel)
	}
	if strings.Contains(res.Text, token) {
		t.Errorf("masked text still contains the token: %q", res.Text)
	}
	if res.ByLabel["STRIPE_DASH"] < 1 {
		t.Errorf("expected STRIPE_DASH label in ByLabel, got %v", res.ByLabel)
	}
}

// TestHandleMaskPIISmoke drives handleMaskPII end-to-end: valid args
// produce masked output with the [kern] masked summary line and no leaked
// token.
func TestHandleMaskPIISmoke(t *testing.T) {
	t.Parallel()
	s := &Server{}
	token := "sk-test-abcdefghijklmnopqrstuvwxyz012345"
	out, err := s.handleMaskPII(context.Background(), map[string]any{"text": "token=" + token})
	if err != nil {
		t.Fatalf("handleMaskPII error: %v", err)
	}
	if !strings.Contains(out, "[kern] masked") {
		t.Errorf("output missing [kern] masked summary: %q", out)
	}
	if strings.Contains(out, token) {
		t.Errorf("output still contains the token: %q", out)
	}
}

// TestSecurityHandlersRejectMissingArgs verifies the security-family
// handler fails fast with a required-arg error. The check runs before any
// hook or index access, so a bare &Server{} is safe to exercise.
func TestSecurityHandlersRejectMissingArgs(t *testing.T) {
	t.Parallel()
	s := &Server{}
	ctx := context.Background()
	cases := []struct {
		name    string
		call    func() (string, error)
		wantErr string
	}{
		{"mask_pii", func() (string, error) { return s.handleMaskPII(ctx, map[string]any{}) }, "text is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.call()
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}
