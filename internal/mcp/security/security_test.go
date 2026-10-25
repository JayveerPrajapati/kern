package security

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/pii"
)

type fakeSecSvc struct {
	maskFn   func(ctx context.Context, text string, names []string) (pii.Result, error)
	gotNames []string
}

func (f *fakeSecSvc) Mask(ctx context.Context, text string, names []string) (pii.Result, error) {
	f.gotNames = names
	return f.maskFn(ctx, text, names)
}

func TestMaskPII(t *testing.T) {
	ctx := context.Background()
	_, err := MaskPII(ctx, nil, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("expected text is required error, got: %v", err)
	}
}

func TestMaskPIIHappy(t *testing.T) {
	ctx := context.Background()
	svc := &fakeSecSvc{
		maskFn: func(ctx context.Context, text string, names []string) (pii.Result, error) {
			return pii.Result{
				Text:     "connect to [MASKED_TOKEN_1]",
				Replaced: 1,
				ByLabel:  map[string]int{"TOKEN": 1},
			}, nil
		},
	}
	out, err := MaskPII(ctx, svc, map[string]any{"text": "connect to sk-secret"})
	if err != nil {
		t.Fatalf("MaskPII failed: %v", err)
	}
	if !strings.Contains(out, "connect to [MASKED_TOKEN_1]") {
		t.Errorf("expected masked text in output, got: %q", out)
	}
	if !strings.Contains(out, "masked 1 secrets") || !strings.Contains(out, "TOKEN 1") {
		t.Errorf("expected summary line with label counts, got: %q", out)
	}
	if !strings.Contains(out, "[kern]") {
		t.Errorf("expected [kern] marker in output, got: %q", out)
	}
}

func TestMaskPIINamesParsing(t *testing.T) {
	ctx := context.Background()
	svc := &fakeSecSvc{
		maskFn: func(ctx context.Context, text string, names []string) (pii.Result, error) {
			return pii.Result{Text: text}, nil
		},
	}
	_, err := MaskPII(ctx, svc, map[string]any{
		"text": "hello", "mask_names": " alice , bob ,,",
	})
	if err != nil {
		t.Fatalf("MaskPII failed: %v", err)
	}
	want := []string{"alice", "bob"}
	if len(svc.gotNames) != len(want) || svc.gotNames[0] != want[0] || svc.gotNames[1] != want[1] {
		t.Errorf("expected names %v, got %v", want, svc.gotNames)
	}
}

func TestMaskPIIError(t *testing.T) {
	ctx := context.Background()
	svc := &fakeSecSvc{
		maskFn: func(ctx context.Context, text string, names []string) (pii.Result, error) {
			return pii.Result{}, context.DeadlineExceeded
		},
	}
	_, err := MaskPII(ctx, svc, map[string]any{"text": "hello"})
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("expected propagated mask error, got: %v", err)
	}
}
