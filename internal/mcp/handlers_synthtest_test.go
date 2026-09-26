package mcp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleSynthesizeTest(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// TestHandleSynthesizeTestSinks covers the sinks= mode of kern_synthesize_test:
// the former kern_taint generate=true scaffold emission moved here (surface
// consolidation T2a). A project with a tainted SQL-injection sink must yield
// one deterministic go test scaffold for the matching rule.
func TestHandleSynthesizeTestSinks(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	root := testRoot(t)
	app := filepath.Join(root, "app.go")
	src := `package main

import (
	"database/sql"
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/x", H)
}

func H(w http.ResponseWriter, r *http.Request) {
	lookup(r.URL.Query().Get("name"))
}

func lookup(name string) {
	var db *sql.DB
	_ = db.Query(fmt.Sprintf("SELECT * FROM users WHERE name = %s", name))
}
`
	if err := os.WriteFile(app, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := serveOne(t, toolsCallJSON(t, 70, "kern_synthesize_test", map[string]any{"root": root, "sinks": "sql-injection"}))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "write to:") {
		t.Fatalf("expected write-to line, got %q", out)
	}
	if !strings.Contains(out, "```go") {
		t.Fatalf("expected fenced go block, got %q", out)
	}
	if !strings.Contains(out, "TestTaintSQLInjection") {
		t.Fatalf("expected scaffold func TestTaintSQLInjection, got %q", out)
	}
	// A rule with no tainted sinks yields the no-match verdict.
	resp2 := serveOne(t, toolsCallJSON(t, 71, "kern_synthesize_test", map[string]any{"root": root, "sinks": "command-injection"}))
	out2, isErr2 := toolResultText(t, resp2)
	if isErr2 {
		t.Fatalf("unexpected error: %s", out2)
	}
	if !strings.Contains(out2, "no tainted sinks matched") {
		t.Fatalf("expected no-match verdict, got %q", out2)
	}
}
