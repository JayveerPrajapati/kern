package resilience

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScenariosFile writes content to <dir>/.blueprint/scenarios/<name>.
func writeScenariosFile(t *testing.T, dir, name, content string) {
	t.Helper()
	scenariosDir := filepath.Join(dir, ".blueprint", "scenarios")
	if err := os.MkdirAll(scenariosDir, 0o755); err != nil {
		t.Fatalf("mkdir scenarios dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scenariosDir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write scenarios file: %v", err)
	}
}

// TestLoad_ValidationError covers the hard validation errors with a missing
// scenarios directory base case.
func TestLoad_ValidationError(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string // substring expected in the error; "" = no error
	}{
		{
			name: "missing scenarios dir is empty",
			yaml: "",
		},
		{
			name: "valid yaml applies params",
			yaml: "scenarios:\n  - id: payments-timeout\n    kind: http\n    params:\n      status: 500\n      delay_seconds: 10\n      path: /api/v1/payments\n",
		},
		{
			name:    "unknown kind",
			yaml:    "scenarios:\n  - id: x\n    kind: grpc\n    params:\n      status: 500\n",
			wantErr: "unknown scenario kind \"grpc\"",
		},
		{
			name:    "missing kind",
			yaml:    "scenarios:\n  - id: x\n    params:\n      status: 500\n",
			wantErr: "missing kind",
		},
		{
			name:    "missing id",
			yaml:    "scenarios:\n  - kind: http\n    params:\n      status: 500\n",
			wantErr: "scenario missing id",
		},
		{
			name:    "status too low",
			yaml:    "scenarios:\n  - id: x\n    kind: http\n    params:\n      status: 99\n      path: /\n",
			wantErr: "invalid HTTP status 99",
		},
		{
			name:    "status too high",
			yaml:    "scenarios:\n  - id: x\n    kind: http\n    params:\n      status: 600\n      path: /\n",
			wantErr: "invalid HTTP status 600",
		},
		{
			name:    "path without leading slash",
			yaml:    "scenarios:\n  - id: x\n    kind: http\n    params:\n      status: 500\n      path: api/v1\n",
			wantErr: `path "api/v1" must start with /`,
		},
		{
			name:    "negative delay",
			yaml:    "scenarios:\n  - id: x\n    kind: http\n    params:\n      status: 500\n      delay_seconds: -1\n      path: /\n",
			wantErr: "negative delay_seconds",
		},
		{
			name:    "extra param key",
			yaml:    "scenarios:\n  - id: x\n    kind: http\n    params:\n      status: 500\n      path: /\n      body: nope\n",
			wantErr: `unknown param "body"`,
		},
		{
			name:    "unknown scenario key",
			yaml:    "scenarios:\n  - id: x\n    kind: http\n    params:\n      status: 500\n    delay: 5\n",
			wantErr: `unknown scenario key "delay"`,
		},
		{
			name:    "unknown top-level key",
			yaml:    "scenarios: []\nversion: 2\n",
			wantErr: `unknown top-level key "version"`,
		},
		{
			name:    "duplicate id across yaml entries",
			yaml:    "scenarios:\n  - id: dup\n    kind: http\n    params:\n      status: 500\n      path: /\n  - id: dup\n    kind: http\n    params:\n      status: 501\n      path: /\n",
			wantErr: `duplicate scenario id "dup"`,
		},
		{
			name:    "duplicate id colliding with built-in",
			yaml:    "scenarios:\n  - id: go:http-timeout\n    kind: http\n    params:\n      status: 500\n      path: /\n",
			wantErr: `duplicate scenario id "go:http-timeout"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.yaml != "" {
				writeScenariosFile(t, dir, "scenarios.yaml", tc.yaml)
			}
			scen, err := Load(dir)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Load() unexpected error: %v", err)
				}
				if tc.name == "valid yaml applies params" {
					if len(scen) != 1 {
						t.Fatalf("Load() = %d scenarios, want 1", len(scen))
					}
					h, ok := scen[0].(*HTTPFault)
					if !ok {
						t.Fatalf("scenario is %T, want *HTTPFault", scen[0])
					}
					if h.id != "payments-timeout" || h.status != 500 || h.delay.String() != "10s" || h.path != "/api/v1/payments" {
						t.Fatalf("params not applied: id=%q status=%d delay=%s path=%q", h.id, h.status, h.delay, h.path)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("Load() = %v, want error containing %q", scen, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestLoad_MultipleFiles ensures scenarios accumulate across several YAML
// files and unreadable content is a hard error.
func TestLoad_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeScenariosFile(t, dir, "a.yaml", "scenarios:\n  - id: one\n    kind: http\n    params:\n      status: 503\n      path: /a\n")
	writeScenariosFile(t, dir, "b.yaml", "scenarios:\n  - id: two\n    kind: http\n    params:\n      status: 500\n      delay_seconds: 2\n      path: /b\n")

	scen, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(scen) != 2 {
		t.Fatalf("Load() = %d scenarios, want 2", len(scen))
	}
	got := map[string]bool{}
	for _, s := range scen {
		got[s.ID()] = true
	}
	if !got["one"] || !got["two"] {
		t.Fatalf("Load() ids = %v, want one and two", got)
	}
}

// TestLoad_MalformedYAML ensures a malformed YAML file is a hard error.
func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeScenariosFile(t, dir, "bad.yaml", "scenarios:\n  - id: [unclosed\n")

	if _, err := Load(dir); err == nil {
		t.Fatal("Load() = nil error, want parse error")
	}
}

// TestLoadAll combines built-ins and YAML with unique ids.
func TestLoadAll(t *testing.T) {
	dir := t.TempDir()
	writeScenariosFile(t, dir, "s.yaml", "scenarios:\n  - id: payments-timeout\n    kind: http\n    params:\n      status: 500\n      delay_seconds: 0\n      path: /api/v1/payments\n")

	scen, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll() error: %v", err)
	}
	if len(scen) != 6 {
		t.Fatalf("LoadAll() = %d scenarios, want 6 (5 built-ins + 1 yaml)", len(scen))
	}
	seen := map[string]bool{}
	for _, s := range scen {
		if seen[s.ID()] {
			t.Fatalf("LoadAll() duplicate id %q", s.ID())
		}
		seen[s.ID()] = true
	}
	if !seen["go:http-timeout"] || !seen["go:http-500"] || !seen["payments-timeout"] ||
		!seen["shell:unhandled-exit"] || !seen["shell:unset-variable"] || !seen["shell:missing-error-handling"] {
		t.Fatalf("LoadAll() ids = %v, want go:http-timeout, go:http-500, payments-timeout, shell:unhandled-exit, shell:unset-variable, shell:missing-error-handling", seen)
	}

	// Missing scenarios dir → built-ins only, nil error.
	empty := t.TempDir()
	scen, err = LoadAll(empty)
	if err != nil {
		t.Fatalf("LoadAll() on empty dir error: %v", err)
	}
	if len(scen) != 5 {
		t.Fatalf("LoadAll() on empty dir = %d scenarios, want 5 built-ins", len(scen))
	}
}

// TestDefaultScenariosFresh ensures LoadAll returns fresh instances so
// concurrent/sequential checks never share fault-server state.
func TestDefaultScenariosFresh(t *testing.T) {
	a := DefaultScenarios()
	b := DefaultScenarios()
	if len(a) != 5 || len(b) != 5 {
		t.Fatalf("DefaultScenarios() = %d, %d; want 5, 5", len(a), len(b))
	}
	for i := range a {
		if a[i] == b[i] {
			t.Fatalf("DefaultScenarios() returned the same instance twice (index %d)", i)
		}
	}
}
