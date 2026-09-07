package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestHandleSemanticMergeClean(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)

	base := `package worker

type Job struct {
	ID string
}
`
	local := `package worker

type Job struct {
	ID string
}

func (j *Job) Start() error { return nil }
`
	remote := `package worker

type Job struct {
	ID string
}

func (j *Job) Stop() error { return nil }
`

	res, err := s.handleSemanticMerge(context.Background(), map[string]any{
		"base":   base,
		"local":  local,
		"remote": remote,
	})
	if err != nil {
		t.Fatalf("handleSemanticMerge failed: %v", err)
	}

	if !strings.Contains(res, "Clean Merge**: `true`") {
		t.Errorf("expected clean merge, got: %s", res)
	}
	if !strings.Contains(res, "func (j *Job) Start() error") || !strings.Contains(res, "func (j *Job) Stop() error") {
		t.Errorf("merged result missing methods: %s", res)
	}
}

func TestHandleSemanticMergeConflictJSON(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)

	base := `package calc

func Add(a, b int) int { return a + b }
`
	local := `package calc

func Add(a, b int) int { return a + b + 1 }
`
	remote := `package calc

func Add(a, b int) int { return a + b + 2 }
`

	res, err := s.handleSemanticMerge(context.Background(), map[string]any{
		"base":   base,
		"local":  local,
		"remote": remote,
		"format": "json",
	})
	if err != nil {
		t.Fatalf("handleSemanticMerge conflict failed: %v", err)
	}

	if !strings.Contains(res, `"clean": false`) {
		t.Errorf("expected clean: false in json output: %s", res)
	}
	if !strings.Contains(res, "func:Add") {
		t.Errorf("expected conflict symbol func:Add in json: %s", res)
	}
}
