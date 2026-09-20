package diff

import (
	"strings"
	"testing"
)

func TestSemanticMergeCleanMethodAdditions(t *testing.T) {
	t.Parallel()
	base := `package worker

type Job struct {
	ID string
}

func NewJob(id string) *Job {
	return &Job{ID: id}
}
`

	// Agent A adds Start() method
	local := `package worker

type Job struct {
	ID string
}

func NewJob(id string) *Job {
	return &Job{ID: id}
}

func (j *Job) Start() error {
	return nil
}
`

	// Agent B adds Stop() method
	remote := `package worker

type Job struct {
	ID string
}

func NewJob(id string) *Job {
	return &Job{ID: id}
}

func (j *Job) Stop() error {
	return nil
}
`

	res, err := SemanticMerge3Way("worker.go", []byte(base), []byte(local), []byte(remote))
	if err != nil {
		t.Fatalf("SemanticMerge3Way error: %v", err)
	}

	if !res.Clean {
		t.Fatalf("expected clean merge, got conflicts: %+v", res.Conflicts)
	}

	if !strings.Contains(res.MergedCode, "func (j *Job) Start() error") {
		t.Errorf("merged code missing Start method: %s", res.MergedCode)
	}
	if !strings.Contains(res.MergedCode, "func (j *Job) Stop() error") {
		t.Errorf("merged code missing Stop method: %s", res.MergedCode)
	}
}

func TestSemanticMergeStructFields(t *testing.T) {
	t.Parallel()
	base := `package config

type Config struct {
	Port int
}
`

	// Agent A adds Host
	local := `package config

type Config struct {
	Port int
	Host string
}
`

	// Agent B adds Timeout
	remote := `package config

type Config struct {
	Port int
	Timeout int
}
`

	res, err := SemanticMerge3Way("config.go", []byte(base), []byte(local), []byte(remote))
	if err != nil {
		t.Fatalf("SemanticMerge3Way error: %v", err)
	}

	if !res.Clean {
		t.Fatalf("expected clean merge for struct fields, got conflicts: %+v", res.Conflicts)
	}

	if !strings.Contains(res.MergedCode, "Host") || !strings.Contains(res.MergedCode, "string") {
		t.Errorf("missing Host field: %s", res.MergedCode)
	}
	if !strings.Contains(res.MergedCode, "Timeout") || !strings.Contains(res.MergedCode, "int") {
		t.Errorf("missing Timeout field: %s", res.MergedCode)
	}
}

func TestSemanticMergeConflictDetection(t *testing.T) {
	t.Parallel()
	base := `package calc

func Compute(x int) int {
	return x * 2
}
`

	// Agent A changes computation to + 10
	local := `package calc

func Compute(x int) int {
	return x + 10
}
`

	// Agent B changes computation to * 100
	remote := `package calc

func Compute(x int) int {
	return x * 100
}
`

	res, err := SemanticMerge3Way("calc.go", []byte(base), []byte(local), []byte(remote))
	if err != nil {
		t.Fatalf("SemanticMerge3Way error: %v", err)
	}

	if res.Clean {
		t.Fatalf("expected conflict on Compute function, but got clean merge")
	}

	if len(res.Conflicts) == 0 || res.Conflicts[0].Symbol != "func:Compute" {
		t.Errorf("expected conflict on func:Compute, got %+v", res.Conflicts)
	}

	if !strings.Contains(res.MergedCode, "<<<<<<< LOCAL") {
		t.Errorf("expected conflict marker in merged code: %s", res.MergedCode)
	}
}
