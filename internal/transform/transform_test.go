package transform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
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

// TestImplementInterfaceFromSourceFallback verifies implement_interface
// resolves a project interface declared in source when the index has no
// method symbols for it (the `kern ast-transform` CLI regression: the index
// records the interface declaration but not its methods).
func TestImplementInterfaceFromSourceFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "contracts.go"), []byte(`package sample

type Greeter interface {
	Greet(name string) string
	Bye()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Name: "Greeter", Kind: "interface", File: "contracts.go", Line: 3},
		},
	}
	code := `package sample
type Robot struct{}
`
	req := Request{
		Action:        "implement_interface",
		Code:          code,
		TargetSymbol:  "Robot",
		InterfaceName: "Greeter",
		Root:          dir,
		Index:         ix,
	}
	res, err := Transform(req)
	if err != nil {
		t.Fatalf("Transform error: %v", err)
	}
	if len(res.Added) != 2 {
		t.Fatalf("expected 2 methods added, got %+v", res.Added)
	}
	if !strings.Contains(res.NewCode, "func (r *Robot) Greet(name string) string") {
		t.Errorf("missing Greet signature: %s", res.NewCode)
	}
	if !strings.Contains(res.NewCode, "func (r *Robot) Bye()") {
		t.Errorf("missing Bye signature: %s", res.NewCode)
	}
}

// TestInterfaceMethodsFromIndexUnknown returns nil for an interface not in
// the index so the caller produces the existing "unknown interface" error.
func TestInterfaceMethodsFromIndexUnknown(t *testing.T) {
	ix := &index.Index{Symbols: []index.Symbol{}}
	if got := interfaceMethodsFromIndex(ix, "Nope", ""); got != nil {
		t.Fatalf("expected nil for unknown interface, got %+v", got)
	}
}

// TestApplyRefusesInvalidOutput guards the apply-time parse validation
// (QA F-6): a mistyped -target (e.g. a path instead of a struct name) must
// never write syntactically invalid Go over the target file.
func TestApplyRefusesInvalidOutput(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "server.go")
	src := `package sample

type Server struct{}
`
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	req := Request{
		Action:        "implement_interface",
		File:          target,
		Root:          dir,             // confinement root: production callers always set it (the MCP leaf resolves cwd)
		TargetSymbol:  "src/server.go", // path-like: yields an invalid receiver type
		InterfaceName: "io.Writer",
		Apply:         true,
	}
	_, err := Transform(req)
	if err == nil {
		t.Fatal("expected apply to be refused for invalid generated code")
	}
	if !strings.Contains(err.Error(), "refusing to apply") {
		t.Fatalf("error = %v, want refusing-to-apply message", err)
	}
	got, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != src {
		t.Fatalf("target file was modified despite refused apply:\n%s", got)
	}
}
