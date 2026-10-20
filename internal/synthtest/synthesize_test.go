package synthtest

import (
	"strings"
	"testing"
)

// TestSynthesizeAutoGapNoGapsDistinctError pins the B1 fix: auto-gap that
// fills nothing must report "no untested hotspots" — not the generic
// missing-source error that conflated it with "you passed no file". The
// wording matters: hotspot = untested symbol WITH production callers, so
// untested symbols can still exist when this fires.
func TestSynthesizeAutoGapNoGapsDistinctError(t *testing.T) {
	_, err := Synthesize(Request{AutoGap: true, Root: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "no untested hotspots") {
		t.Fatalf("want distinct no-hotspots error, got: %v", err)
	}
}

// TestSynthesizeNoSourceMessageNamesFix pins the B1 fix: target alone (no
// file, no code) must fail with the explanatory message that names the
// actual requirement, not the self-contradicting "must be provided".
func TestSynthesizeNoSourceMessageNamesFix(t *testing.T) {
	_, err := Synthesize(Request{Target: "Multiply", Root: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "target alone does not select a source file") {
		t.Fatalf("want explanatory no-source error, got: %v", err)
	}
}

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
