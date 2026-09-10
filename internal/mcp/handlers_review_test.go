package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// TestHandleReviewLens drives the P1-002 lens arg through kern_review: a
// valid lens prepends the deterministic "lens: name (type=weight, ...)" line;
// an unknown lens is rejected; no lens arg leaves the output byte-identical.
// The file arg scopes the review to a known path so the test does not depend
// on git state.
func TestHandleReviewLens(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	// Drain the session's background index save before TempDir cleanup so the
	// .kern persistence goroutine cannot race the RemoveAll (pre-existing
	// flake: "TempDir RemoveAll cleanup: directory not empty").
	defer s.Close()
	args := func(lens string) map[string]any {
		m := map[string]any{"root": root, "file": "app.go"}
		if lens != "" {
			m["lens"] = lens
		}
		return m
	}

	base, err := s.handleReview(context.Background(), args(""))
	if err != nil {
		t.Fatalf("handleReview: %v", err)
	}

	lensed, err := s.handleReview(context.Background(), args("security"))
	if err != nil {
		t.Fatalf("handleReview with lens: %v", err)
	}
	if !strings.HasPrefix(lensed, "lens: security (") {
		t.Errorf("lensed output = %q, want 'lens: security (' prefix", lensed)
	}
	if !strings.HasSuffix(lensed, base) {
		t.Error("lensed output should keep the original review after the lens line")
	}

	if _, err := s.handleReview(context.Background(), args("bogus")); err == nil || !strings.Contains(err.Error(), "unknown lens") {
		t.Errorf("unknown lens: err = %v, want rejection with 'unknown lens'", err)
	}
}

// TestHandleReviewProfile drives the P1-005 profile arg through kern_review:
// machine-json wraps the review in a JSON envelope; an unknown profile is
// rejected; no profile arg leaves the output byte-identical.
func TestHandleReviewProfile(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()
	args := func(profile string) map[string]any {
		m := map[string]any{"root": root, "file": "app.go"}
		if profile != "" {
			m["profile"] = profile
		}
		return m
	}

	base, err := s.handleReview(context.Background(), args(""))
	if err != nil {
		t.Fatalf("handleReview: %v", err)
	}

	js, err := s.handleReview(context.Background(), args("machine-json"))
	if err != nil {
		t.Fatalf("handleReview machine-json: %v", err)
	}
	var m struct {
		Profile string `json:"profile"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("machine-json output not valid JSON: %v\n%s", err, js)
	}
	if m.Profile != "machine-json" || m.Content != base {
		t.Errorf("decoded profile=%q content-match=%v; want machine-json + original review", m.Profile, m.Content == base)
	}

	if _, err := s.handleReview(context.Background(), args("bogus")); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("unknown profile: err = %v, want rejection with 'unknown profile'", err)
	}
}
