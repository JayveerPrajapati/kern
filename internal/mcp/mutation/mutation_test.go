package mutation

import (
	"context"
	"strings"
	"testing"
)

func TestMutationDryRun(t *testing.T) {
	ctx := context.Background()
	res, err := Test(ctx, map[string]any{
		"dry_run": "true",
	})
	if err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if !strings.Contains(res, "Mutation Testing Report") {
		t.Errorf("expected report header, got: %s", res)
	}
}
