package repair

import (
	"strings"
	"testing"
)

func TestRepairUnusedImport(t *testing.T) {
	src := `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("hello")
}
`
	compilerOutput := `main.go:4:2: imported and not used: "os"`
	diags := ParseDiagnostics(compilerOutput)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}

	res, err := RepairFile("main.go", []byte(src), diags)
	if err != nil {
		t.Fatalf("RepairFile failed: %v", err)
	}

	if !res.Repaired {
		t.Errorf("expected repaired true, got false")
	}
	if strings.Contains(res.Content, `"os"`) {
		t.Errorf("expected unused import os removed, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, `"fmt"`) {
		t.Errorf("expected used import fmt preserved, got:\n%s", res.Content)
	}
}

func TestRepairUndefinedPkgImport(t *testing.T) {
	src := `package main

func main() {
	fmt.Println("hello")
}
`
	compilerOutput := `main.go:4:2: undefined: fmt.Println`
	diags := ParseDiagnostics(compilerOutput)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}

	res, err := RepairFile("main.go", []byte(src), diags)
	if err != nil {
		t.Fatalf("RepairFile failed: %v", err)
	}

	if !res.Repaired {
		t.Errorf("expected repaired true, got false")
	}
	if !strings.Contains(res.Content, `"fmt"`) {
		t.Errorf("expected missing import fmt added, got:\n%s", res.Content)
	}
}

func TestRepairUnusedVariable(t *testing.T) {
	src := `package main

func Calc() int {
	x := 42
	return 10
}
`
	compilerOutput := `main.go:4:2: x declared and not used`
	diags := ParseDiagnostics(compilerOutput)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diags))
	}

	res, err := RepairFile("main.go", []byte(src), diags)
	if err != nil {
		t.Fatalf("RepairFile failed: %v", err)
	}

	if !res.Repaired {
		t.Errorf("expected repaired true, got false")
	}
	if !strings.Contains(res.Content, `_ = x`) {
		t.Errorf("expected unused var x silenced with _ = x, got:\n%s", res.Content)
	}
}
