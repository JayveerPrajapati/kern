package ci

import (
	"testing"
)

func TestParseRunID(t *testing.T) {
	jsonOut := []byte(`[{"databaseId":12345,"status":"in_progress","createdAt":"2026-01-01T00:00:00Z"}]`)
	id := parseRunID(jsonOut)
	if id != "12345" {
		t.Errorf("parseRunID = %q, want 12345", id)
	}
}

func TestParseRunIDEmpty(t *testing.T) {
	if id := parseRunID([]byte(`[]`)); id != "" {
		t.Errorf("parseRunID([]) = %q, want empty", id)
	}
}

func TestParseRunStatus(t *testing.T) {
	jsonOut := []byte(`{"status":"completed","conclusion":"success","url":"https://github.com/owner/repo/actions/runs/123","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:05:00Z"}`)
	job := parseRunStatus(jsonOut, "123")
	if job.Status != StatusSuccess {
		t.Errorf("status = %q, want success", job.Status)
	}
	if job.URL != "https://github.com/owner/repo/actions/runs/123" {
		t.Errorf("URL = %q", job.URL)
	}
}

func TestParseRunStatusFailure(t *testing.T) {
	jsonOut := []byte(`{"status":"completed","conclusion":"failure","url":"","createdAt":"","updatedAt":""}`)
	job := parseRunStatus(jsonOut, "456")
	if job.Status != StatusFailure {
		t.Errorf("status = %q, want failure", job.Status)
	}
}

func TestGitHubActionsAdapterUnavailableWithoutGH(t *testing.T) {
	// This test verifies the adapter returns ErrAdapterUnavailable when
	// gh is not on PATH. We point PATH at an empty temp dir so gh is never
	// found regardless of the machine's installed tools, making the error
	// path deterministic.
	t.Setenv("PATH", t.TempDir())
	a := NewGitHubActionsAdapter("")
	if _, err := a.Trigger(Pipeline{Name: "ci.yml", Ref: "main"}); err != ErrAdapterUnavailable {
		t.Errorf("Trigger without gh = %v, want ErrAdapterUnavailable", err)
	}
}

func TestPipelineFields(t *testing.T) {
	p := Pipeline{Name: "ci.yml", Ref: "main", Inputs: map[string]string{"env": "staging"}}
	if p.Name != "ci.yml" || p.Ref != "main" || p.Inputs["env"] != "staging" {
		t.Error("Pipeline fields not set correctly")
	}
}
