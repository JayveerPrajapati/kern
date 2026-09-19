package transform

import (
	"context"
	"strings"
	"testing"
)

func TestTransformEmptyCode(t *testing.T) {
	ctx := context.Background()
	_, err := Transform(ctx, Hooks{}, map[string]any{
		"code": "",
	})
	if err == nil || !strings.Contains(err.Error(), "ast transform") {
		t.Fatalf("expected error on empty code, got: %v", err)
	}
}

func TestTransformAddField(t *testing.T) {
	ctx := context.Background()
	code := "package main\n\ntype User struct {\n\tID int\n}\n"
	res, err := Transform(ctx, Hooks{}, map[string]any{
		"action":        "add_field",
		"code":          code,
		"target_symbol": "User",
		"field_name":    "Name",
		"field_type":    "string",
	})
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}
	if !strings.Contains(res, "AST Transform Report: add_field") {
		t.Errorf("expected report header, got: %s", res)
	}
}
