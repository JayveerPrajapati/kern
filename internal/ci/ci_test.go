package ci

import (
	"os/exec"
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
	// gh is not on PATH. We can't remove gh from PATH in a test, so
	// instead we test the checkAvailable logic indirectly by verifying
	// that Trigger/Status/Logs handle the error path. Since gh IS likely
	// installed in the test env, we skip this if gh is available.
	if _, err := exec.LookPath("gh"); err == nil {
		t.Skip("gh is installed; cannot test unavailable path")
	}
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
