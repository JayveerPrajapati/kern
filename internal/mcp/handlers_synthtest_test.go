package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestHandleSynthesizeTest(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)

	code := `package converter

func HexToInt(hex string) (int, error) {
	return 0, nil
}
`

	res, err := s.handleSynthesizeTest(context.Background(), map[string]any{
		"target": "HexToInt",
		"code":   code,
	})
	if err != nil {
		t.Fatalf("handleSynthesizeTest failed: %v", err)
	}

	if !strings.Contains(res, "Synthesize Test Report") {
		t.Errorf("missing report header: %s", res)
	}
	if !strings.Contains(res, "TestHexToInt") {
		t.Errorf("missing TestHexToInt in report: %s", res)
	}
	if !strings.Contains(res, "HexToInt(tt.hex)") {
		t.Errorf("missing HexToInt call with tt.hex: %s", res)
	}
}

func TestHandleSynthesizeTestJSON(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)

	code := `package auth

type Service struct{}

func (s *Service) Login(user, pass string) bool {
	return true
}
`

	res, err := s.handleSynthesizeTest(context.Background(), map[string]any{
		"target": "Service.Login",
		"code":   code,
		"format": "json",
	})
	if err != nil {
		t.Fatalf("handleSynthesizeTest JSON failed: %v", err)
	}

	if !strings.Contains(res, `"test_function": "TestService_Login"`) {
		t.Errorf("expected TestService_Login in json: %s", res)
	}
	if !strings.Contains(res, `"target_symbol": "Service.Login"`) {
		t.Errorf("expected Service.Login target in json: %s", res)
	}
}
