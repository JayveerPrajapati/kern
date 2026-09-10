package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadSamplesFromDir loads one Sample per *.json file in dir (non-recursive,
// via os.ReadDir). Each file maps the Sample fields by name: "name",
// "baseline", "candidate", "critical_evidence". Non-.json files are ignored.
// A malformed JSON file returns an error naming the file. An empty (or
// json-free) dir returns an empty slice with nil error.
func LoadSamplesFromDir(dir string) ([]Sample, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var samples []Sample
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var js jsonSample
		if err := json.Unmarshal(data, &js); err != nil {
			return samples, fmt.Errorf("eval: load sample %s: %w", e.Name(), err)
		}
		samples = append(samples, Sample{
			Name:             js.Name,
			Baseline:         js.Baseline,
			Candidate:        js.Candidate,
			CriticalEvidence: js.CriticalEvidence,
		})
	}
	return samples, nil
}

// jsonSample is the on-disk shape of a Sample (explicit json tags; the
// Sample struct itself is tag-free and must not be modified).
type jsonSample struct {
	Name             string   `json:"name"`
	Baseline         string   `json:"baseline"`
	Candidate        string   `json:"candidate"`
	CriticalEvidence []string `json:"critical_evidence"`
}
