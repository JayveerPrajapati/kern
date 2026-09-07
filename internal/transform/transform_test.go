package transform

import (
	"strings"
	"testing"
)

func TestImplementInterfaceStandard(t *testing.T) {
	code := `package sample

type FileLogger struct {
	path string
}
`

	req := Request{
		Action:        "implement_interface",
		Code:          code,
		TargetSymbol:  "FileLogger",
		InterfaceName: "io.Writer",
	}

	res, err := Transform(req)
	if err != nil {
		t.Fatalf("Transform error: %v", err)
	}

	if len(res.Added) != 1 || res.Added[0] != "Write" {
		t.Fatalf("expected Write to be added, got %+v", res.Added)
	}

	if !strings.Contains(res.NewCode, "func (f *FileLogger) Write(p []byte) (n int, err error)") {
		t.Errorf("missing generated method signature in code: %s", res.NewCode)
	}

	if !strings.Contains(res.Diff, "+func (f *FileLogger) Write") {
		t.Errorf("expected Write addition in diff: %s", res.Diff)
	}
}

func TestAddField(t *testing.T) {
	code := `package sample

type Config struct {
	Port int ` + "`json:\"port\"`" + `
}
`

	req := Request{
		Action:       "add_field",
		Code:         code,
		TargetSymbol: "Config",
		FieldName:    "Timeout",
		FieldType:    "string",
		FieldTag:     `json:"timeout,omitempty"`,
	}

	res, err := Transform(req)
	if err != nil {
		t.Fatalf("Transform error: %v", err)
	}

	if len(res.Added) != 1 || res.Added[0] != "Timeout" {
		t.Fatalf("expected Timeout added, got %+v", res.Added)
	}

	if !strings.Contains(res.NewCode, `Timeout string `+"`json:\"timeout,omitempty\"`") {
		t.Errorf("missing new field in transformed code: %s", res.NewCode)
	}
}

func TestAddMethod(t *testing.T) {
	code := `package sample

type Worker struct {
	id int
}
`

	req := Request{
		Action:          "add_method",
		Code:            code,
		TargetSymbol:    "Worker",
		MethodSignature: "Stop() error",
		MethodBody:      "return nil",
	}

	res, err := Transform(req)
	if err != nil {
		t.Fatalf("Transform error: %v", err)
	}

	if len(res.Added) != 1 {
		t.Fatalf("expected 1 method added, got %+v", res.Added)
	}

	if !strings.Contains(res.NewCode, "func (w *Worker) Stop() error") {
		t.Errorf("missing method signature in transformed code: %s", res.NewCode)
	}
}

func TestImplementInterfaceSkipExisting(t *testing.T) {
	code := `package sample

type Streamer struct{}

func (s *Streamer) Read(p []byte) (n int, err error) {
	return 0, nil
}
`

	req := Request{
		Action:        "implement_interface",
		Code:          code,
		TargetSymbol:  "Streamer",
		InterfaceName: "io.ReadCloser",
	}

	res, err := Transform(req)
	if err != nil {
		t.Fatalf("Transform error: %v", err)
	}

	// Should only add Close since Read is already implemented
	if len(res.Added) != 1 || res.Added[0] != "Close" {
		t.Fatalf("expected only Close to be added, got %+v", res.Added)
	}

	if !strings.Contains(res.NewCode, "func (s *Streamer) Close() error") {
		t.Errorf("missing Close method in code: %s", res.NewCode)
	}
}
