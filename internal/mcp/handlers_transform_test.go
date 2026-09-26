package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestHandleAstTransformImplementInterface(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), io.Discard)

	code := `package main

type CustomBuffer struct {
	buf []byte
}
`

	res, err := s.handleAstTransform(context.Background(), map[string]any{
		"action":         "implement_interface",
		"code":           code,
		"target_symbol":  "CustomBuffer",
		"interface_name": "io.Writer",
	})
	if err != nil {
		t.Fatalf("handleAstTransform error: %v", err)
	}

	if !strings.Contains(res, "AST Transform Report: implement_interface") {
		t.Errorf("missing report header: %s", res)
	}
	if !strings.Contains(res, "+func (c *CustomBuffer) Write") {
		t.Errorf("expected Write method stub in diff: %s", res)
	}
}

func TestHandleAstTransformAddField(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), io.Discard)

	code := `package main

type User struct {
	Name string ` + "`json:\"name\"`" + `
}
`

	res, err := s.handleAstTransform(context.Background(), map[string]any{
		"action":        "add_field",
		"code":          code,
		"target_symbol": "User",
		"field_name":    "Email",
		"field_type":    "string",
		"field_tag":     `json:"email"`,
	})
	if err != nil {
		t.Fatalf("handleAstTransform add_field error: %v", err)
	}

	if !strings.Contains(res, "+	Email string `json:\"email\"`") {
		t.Errorf("expected Email field in diff: %s", res)
	}
}
