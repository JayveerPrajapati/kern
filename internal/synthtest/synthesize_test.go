package synthtest

import (
	"strings"
	"testing"
)

func TestSynthesizeStandaloneFunction(t *testing.T) {
	code := `package mathutil

func Multiply(a int, b int) (int, error) {
	return a * b, nil
}
`

	req := Request{
		Target: "Multiply",
		Code:   code,
	}

	res, err := Synthesize(req)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	if res.TestFunction != "TestMultiply" {
		t.Errorf("expected TestMultiply, got %s", res.TestFunction)
	}

	if !strings.Contains(res.TestCode, "func TestMultiply(t *testing.T)") {
		t.Errorf("test code missing function declaration: %s", res.TestCode)
	}

	if !strings.Contains(res.TestCode, "Multiply(tt.a, tt.b)") {
		t.Errorf("test code missing Multiply call with args: %s", res.TestCode)
	}

	if !strings.Contains(res.TestCode, "wantErr") {
		t.Errorf("test code should include wantErr since Multiply returns error: %s", res.TestCode)
	}
}

func TestSynthesizeMethod(t *testing.T) {
	code := `package service

type Worker struct {
	ID string
}

func (w *Worker) Process(task string) error {
	return nil
}
`

	req := Request{
		Target: "Worker.Process",
		Code:   code,
	}

	res, err := Synthesize(req)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	if res.TestFunction != "TestWorker_Process" {
		t.Errorf("expected TestWorker_Process, got %s", res.TestFunction)
	}

	if !strings.Contains(res.TestCode, "receiver := &Worker{}") {
		t.Errorf("test code missing pointer receiver initialization: %s", res.TestCode)
	}

	if !strings.Contains(res.TestCode, "receiver.Process(tt.task)") {
		t.Errorf("test code missing receiver method call: %s", res.TestCode)
	}
}

func TestSynthesizeAppendToExistingTest(t *testing.T) {
	existingTest := `package service

import (
	"testing"
)

func TestExisting(t *testing.T) {
}
`
	newTestCode := `func TestAdded(t *testing.T) {
}`

	merged, err := appendTestToFile([]byte(existingTest), newTestCode)
	if err != nil {
		t.Fatalf("appendTestToFile failed: %v", err)
	}

	if !strings.Contains(merged, "func TestExisting(t *testing.T)") {
		t.Errorf("lost TestExisting in merged: %s", merged)
	}
	if !strings.Contains(merged, "func TestAdded(t *testing.T)") {
		t.Errorf("missing TestAdded in merged: %s", merged)
	}
}
