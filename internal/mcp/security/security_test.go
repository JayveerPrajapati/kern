package security

import (
	"context"
	"strings"
	"testing"
)

func TestMaskPII(t *testing.T) {
	ctx := context.Background()
	_, err := MaskPII(ctx, nil, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("expected text is required error, got: %v", err)
	}
}

func TestSchemaValidate(t *testing.T) {
	ctx := context.Background()
	_, err := SchemaValidate(ctx, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected required error, got: %v", err)
	}

	res, err := SchemaValidate(ctx, map[string]any{
		"data":   `{"name": "test"}`,
		"schema": `{"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res, "schema OK") {
		t.Fatalf("expected schema OK, got: %q", res)
	}
}

func TestSafeDeleteMissingSymbol(t *testing.T) {
	ctx := context.Background()
	_, err := SafeDelete(ctx, Hooks{}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "symbol is required") {
		t.Fatalf("expected symbol is required error, got: %v", err)
	}
}
