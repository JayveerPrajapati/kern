package review

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

func TestReviewChangesErrorHandling(t *testing.T) {
	ctx := context.Background()
	h := Hooks{
		ChangedContext: func(ctx context.Context, args map[string]any) ([]intel.FileChange, *index.Index, error) {
			return nil, nil, context.Canceled
		},
	}
	_, err := Changes(ctx, h, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled error, got: %v", err)
	}
}

func TestReviewHooksErrorHandling(t *testing.T) {
	ctx := context.Background()
	h := Hooks{
		LoadIndex: func(ctx context.Context, root string) (*index.Index, error) {
			return nil, context.Canceled
		},
	}
	_, err := Hubs(ctx, h, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled error, got: %v", err)
	}

	_, err = TestGaps(ctx, h, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled error, got: %v", err)
	}
}
