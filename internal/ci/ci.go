// Package ci provides a vendor-agnostic CI/CD adapter interface and a
// GitHub Actions implementation. The interface is minimal: trigger a
// pipeline, poll status, fetch logs. When no adapter is configured, callers
// degrade gracefully (no CI data, not an error).
package ci

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// JobStatus is the lifecycle state of a CI job.
type JobStatus string

const (
	StatusQueued     JobStatus = "queued"
	StatusInProgress JobStatus = "in_progress"
	StatusSuccess    JobStatus = "success"
	StatusFailure    JobStatus = "failure"
	StatusCancelled  JobStatus = "cancelled"
)

// Job is a CI job identifier and its current state.
type Job struct {
	ID        string
	Status    JobStatus
	StartedAt time.Time
	EndedAt   time.Time
	URL       string
}

// Pipeline identifies a CI pipeline to trigger.
type Pipeline struct {
	// Name is the workflow filename (e.g. "ci.yml") or pipeline name.
	Name string
	// Ref is the git ref to run against (branch or tag, e.g. "main").
	Ref string
	// Inputs are optional workflow_dispatch inputs (key-value).
	Inputs map[string]string
}

// CIAdapter is the vendor-agnostic CI/CD interface. Implementations wrap
// vendor APIs (gh CLI for GitHub Actions). All methods return an error
// when the adapter is unavailable or the vendor API fails.
type CIAdapter interface {
	// Trigger starts a pipeline and returns the job ID.
	Trigger(p Pipeline) (jobID string, err error)
	// Status polls the current state of a job.
	Status(jobID string) (*Job, error)
	// Logs fetches the log output of a completed (or in-progress) job.
	Logs(jobID string) ([]byte, error)
}

// ErrAdapterUnavailable is returned when no CI adapter is configured
// (e.g. gh CLI not installed, no auth token). Callers should treat this
// as a graceful degradation, not a hard failure.
var ErrAdapterUnavailable = errors.New("ci: adapter unavailable")

// GitHubActionsAdapter implements CIAdapter using the gh CLI. It requires
// gh to be installed and authenticated. When gh is missing, all methods
// return ErrAdapterUnavailable.
type GitHubActionsAdapter struct {
	repo string // "owner/name" or empty for auto-detect
}

// NewGitHubActionsAdapter returns an adapter for the given repo
// (format "owner/name"). An empty repo triggers auto-detection via
// `gh repo view`.
func NewGitHubActionsAdapter(repo string) *GitHubActionsAdapter {
	return &GitHubActionsAdapter{repo: repo}
}

// Trigger runs `gh workflow run <name> --ref <ref>` and returns the
// triggered run ID (polled via `gh run list`).
func (a *GitHubActionsAdapter) Trigger(p Pipeline) (string, error) {
	if err := a.checkAvailable(); err != nil {
		return "", err
	}
	args := []string{"workflow", "run", p.Name, "--ref", p.Ref}
	if a.repo != "" {
		args = append(args, "-R", a.repo)
	}
	for k, v := range p.Inputs {
		args = append(args, "-f", k+"="+v)
	}
	if out, err := exec.Command("gh", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("ci: gh workflow run: %w: %s", err, out)
	}
	// Poll for the triggered run. A short sleep lets the new run appear in
	// `gh run list` before we query it, and we filter by the requested ref so a
	// concurrently-started run for another branch can't be mistaken for ours.
	time.Sleep(2 * time.Second)
	listArgs := []string{"run", "list", "--workflow", p.Name, "--ref", p.Ref, "--limit", "5", "--json", "databaseId,status,createdAt,headBranch"}
	if a.repo != "" {
		listArgs = append(listArgs, "-R", a.repo)
	}
	out, err := exec.Command("gh", listArgs...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ci: gh run list: %w: %s", err, out)
	}
	id := parseRunIDForRef(out, p.Ref)
	if id == "" {
		return "", errors.New("ci: could not find triggered run")
	}
	return id, nil
}

// Status runs `gh run view <id> --json status,conclusion,url` and maps
// it to a Job.
func (a *GitHubActionsAdapter) Status(jobID string) (*Job, error) {
	if err := a.checkAvailable(); err != nil {
		return nil, err
	}
	args := []string{"run", "view", jobID, "--json", "status,conclusion,url,createdAt,updatedAt"}
	if a.repo != "" {
		args = append(args, "-R", a.repo)
	}
	out, err := exec.Command("gh", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ci: gh run view: %w: %s", err, out)
	}
	return parseRunStatus(out, jobID), nil
}

// Logs runs `gh run view <id> --log` and returns the raw log bytes.
func (a *GitHubActionsAdapter) Logs(jobID string) ([]byte, error) {
	if err := a.checkAvailable(); err != nil {
		return nil, err
	}
	args := []string{"run", "view", jobID, "--log"}
	if a.repo != "" {
		args = append(args, "-R", a.repo)
	}
	out, err := exec.Command("gh", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ci: gh run view --log: %w: %s", err, out)
	}
	return out, nil
}

// checkAvailable returns ErrAdapterUnavailable when gh is not on PATH.
func (a *GitHubActionsAdapter) checkAvailable() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return ErrAdapterUnavailable
	}
	return nil
}

// parseRunID extracts the databaseId from a `gh run list --json` output.
// Uses encoding/json for correct parsing.
func parseRunID(jsonOut []byte) string {
	type runEntry struct {
		DatabaseID int    `json:"databaseId"`
		Status     string `json:"status"`
		CreatedAt  string `json:"createdAt"`
	}
	var runs []runEntry
	if err := json.Unmarshal(jsonOut, &runs); err != nil || len(runs) == 0 {
		return ""
	}
	return fmt.Sprintf("%d", runs[0].DatabaseID)
}

// parseRunIDForRef extracts the databaseId of the newest run whose
// headBranch matches ref from a `gh run list --json` output (which is sorted
// newest-first). Returns "" when no run for the ref is present. This avoids
// returning a concurrently-started run for another branch.
func parseRunIDForRef(jsonOut []byte, ref string) string {
	type runEntry struct {
		DatabaseID int    `json:"databaseId"`
		Status     string `json:"status"`
		CreatedAt  string `json:"createdAt"`
		HeadBranch string `json:"headBranch"`
	}
	var runs []runEntry
	if err := json.Unmarshal(jsonOut, &runs); err != nil {
		return ""
	}
	for _, r := range runs {
		if r.HeadBranch == ref {
			return fmt.Sprintf("%d", r.DatabaseID)
		}
	}
	return ""
}

// parseRunStatus maps `gh run view --json` output to a Job.
func parseRunStatus(jsonOut []byte, jobID string) *Job {
	type view struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		URL        string `json:"url"`
		CreatedAt  string `json:"createdAt"`
		UpdatedAt  string `json:"updatedAt"`
	}
	var v view
	if err := json.Unmarshal(jsonOut, &v); err != nil {
		return &Job{ID: jobID, Status: StatusInProgress}
	}
	job := &Job{ID: jobID, URL: v.URL}
	switch strings.ToLower(v.Status) {
	case "queued":
		job.Status = StatusQueued
	case "in_progress", "inprogress":
		job.Status = StatusInProgress
	default:
		// Map conclusion to final status
		switch strings.ToLower(v.Conclusion) {
		case "success":
			job.Status = StatusSuccess
		case "failure":
			job.Status = StatusFailure
		case "cancelled":
			job.Status = StatusCancelled
		default:
			job.Status = StatusInProgress
		}
	}
	job.StartedAt = parseTime(v.CreatedAt)
	job.EndedAt = parseTime(v.UpdatedAt)
	return job
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
