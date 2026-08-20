package runtime

import (
	"encoding/json"
	"os"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Snapshot is the on-disk shape of a local production snapshot: a deterministic
// JSON document of telemetry events, deployments and commits. This is the
// local-first, offline path for production intelligence (no network, no vendor).
type Snapshot struct {
	Events      []Event             `json:"events"`
	Deployments []domain.Deployment `json:"deployments"`
	Commits     []Commit            `json:"commits"`
}

// LoadJSON reads a local runtime snapshot from disk into a Store. A missing or
// malformed file is an error (fail-closed); an empty valid document yields an
// empty store.
func LoadJSON(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseSnapshot(data)
}

// ParseSnapshot decodes a JSON runtime snapshot into a Store.
func ParseSnapshot(data []byte) (*Store, error) {
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	st := NewStore()
	st.IngestAll(snap.Events)
	for _, d := range snap.Deployments {
		st.AddDeployment(d)
	}
	for _, c := range snap.Commits {
		st.AddCommit(c)
	}
	return st, nil
}
