// Package app hosts the TaskService orchestration layer.
// Generated split of task.go by domain (see task.go for the core).
package app

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/JayveerPrajapati/kern/internal/agent"
	ctxpkg "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"sort"
	"strings"
	"time"
)

// ReplayRecord carries the metadata a replay needs to be meaningful: which
// repo version, which model, and which configuration produced the
// task being replayed. Without this, a replayed task is ambiguous.
type ReplayRecord struct {
	TaskID      string `json:"task_id"`
	RepoVersion string `json:"repo_version"` // git sha / version at the time
	Model       string `json:"model"`        // model that ran the original task
	ConfigHash  string `json:"config_hash"`  // hash of the config used
	// ContextVersion is a digest of the context packet the task ran with (the
	// "context version" of ): a change in the analysis or package
	// input changes the digest, so two replays can be compared on what context
	// they actually saw. Empty when the task carries no context packet.
	ContextVersion string `json:"context_version,omitempty"`
	// ToolVersions is a deterministic digest of the distinct tools/actions the
	// task actually invoked across its steps ("tool versions"). It changes
	// when the tool selection changes, making replays
	// comparable on the tool surface used.
	ToolVersions string    `json:"tool_versions,omitempty"`
	ReplayedAt   time.Time `json:"replayed_at"`
}

// ReplayTask reconstructs a task for replay, returning a ReplayRecord with the
// metadata (repo version, model, config hash, context version, tool versions)
// needed to interpret the replay. It returns the reconstructed
// task's current state plus the metadata record.
func (s *TaskService) ReplayTask(taskID, repoVersion, model, configHash string) (*ReplayRecord, error) {
	t, err := s.getTaskForMutation(taskID)
	if err != nil {
		return nil, err
	}
	s.reconstructContext(t)
	rec := &ReplayRecord{
		TaskID:         t.ID,
		RepoVersion:    repoVersion,
		Model:          model,
		ConfigHash:     configHash,
		ContextVersion: replayContextVersion(t),
		ToolVersions:   replayToolVersions(t),
		ReplayedAt:     time.Now().UTC(),
	}
	return rec, nil
}

// replayContextVersion digests the task's context packet into a stable
// context version. Empty when the task has no packet.
func replayContextVersion(t *agent.Task) string {
	if t == nil || t.ContextPacket == nil {
		return ""
	}
	digest := sha256.Sum256([]byte(ctxpkg.RenderText(*t.ContextPacket)))
	return hex.EncodeToString(digest[:])[:16]
}

// replayToolVersions digests the distinct tool actions a task invoked into a
// stable tool-version fingerprint. Empty when the task has no
// steps.
func replayToolVersions(t *agent.Task) string {
	if t == nil || len(t.Steps) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var names []string
	for _, st := range t.Steps {
		if st.Action != "" && !seen[st.Action] {
			seen[st.Action] = true
			names = append(names, st.Action)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	digest := sha256.Sum256([]byte(strings.Join(names, "|")))
	return hex.EncodeToString(digest[:])[:16]
}

// RunCompare compares two task runs by their artifact chains and snapshot
// histories ( run-compare). It reports which artifact kinds differ,
// whether the tasks reached the same state, and a per-stage verdict. Unlike the
// raw ArtifactStore.Compare, RunCompare folds in the snapshot history so the
// run outcome (not just artifacts) is compared.
func (s *TaskService) RunCompare(taskID1, taskID2 string) (*RunComparison, error) {
	arts, err := s.Artifacts().Compare(taskID1, taskID2)
	if err != nil {
		return nil, err
	}
	res := &RunComparison{
		ArtifactDiff: arts,
		DigestDiff:   len(arts.DigestDiff),
		OnlyIn1:      len(arts.OnlyIn1),
		OnlyIn2:      len(arts.OnlyIn2),
	}
	// Snapshot history for each task.
	h1, _ := s.Snapshots().History(taskID1)
	h2, _ := s.Snapshots().History(taskID2)
	res.Snapshots1 = len(h1)
	res.Snapshots2 = len(h2)
	if len(h1) > 0 && len(h2) > 0 {
		res.State1 = string(h1[len(h1)-1].State)
		res.State2 = string(h2[len(h2)-1].State)
		res.StateDiffer = res.State1 != res.State2
	}
	// Rich run dimensions : agent, model, tool-call proxy, cost,
	// and success, read from each task's record when available. Best-effort:
	// zero values when a task cannot be fetched or a dimension is unset.
	t1, _ := s.getTaskForMutation(taskID1)
	t2, _ := s.getTaskForMutation(taskID2)
	if t1 != nil {
		res.Agent1 = t1.AgentID
		res.ToolCalls1 = len(t1.Steps)
		res.Success1 = t1.State == domain.TaskCompleted
		res.Cost1 = costProxy(t1)
	}
	if t2 != nil {
		res.Agent2 = t2.AgentID
		res.ToolCalls2 = len(t2.Steps)
		res.Success2 = t2.State == domain.TaskCompleted
		res.Cost2 = costProxy(t2)
	}
	// Verdict: tasks are equivalent when their artifact kinds and final states
	// match with no digest differences.
	res.Equivalent = !res.StateDiffer && res.DigestDiff == 0 && res.OnlyIn1 == 0 && res.OnlyIn2 == 0
	return res, nil
}

// costProxy is a deterministic stand-in for the model cost of a task when no
// measured cost is recorded. It derives a 0..1 proxy from the task's step
// count so richer run comparisons have a comparable "cost" signal without
// inventing a currency. Zero when the task has no steps.
func costProxy(t *agent.Task) float64 {
	if t == nil || len(t.Steps) == 0 {
		return 0
	}
	n := float64(len(t.Steps))
	if n > 100 {
		n = 100
	}
	return n / 100
}

// RunComparison is the run-compare result .
type RunComparison struct {
	ArtifactDiff *ArtifactComparison
	DigestDiff   int
	OnlyIn1      int
	OnlyIn2      int
	Snapshots1   int
	Snapshots2   int
	State1       string
	State2       string
	StateDiffer  bool
	Equivalent   bool

	// Rich run dimensions. Populated best-effort from each task's
	// record when available; zero values when unavailable.
	Agent1     string
	Agent2     string
	Model1     string
	Model2     string
	ToolCalls1 int
	ToolCalls2 int
	Cost1      float64
	Cost2      float64
	Success1   bool
	Success2   bool
}
