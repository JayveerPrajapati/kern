package evidence

import (
	"context"
	"strings"
	"testing"
)

func TestEvidenceMissingSource(t *testing.T) {
	ctx := context.Background()
	_, err := Handle(ctx, Hooks{}, map[string]any{
		"action": "verify",
	})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected error on missing file/url, got: %v", err)
	}
}
