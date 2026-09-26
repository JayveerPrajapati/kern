// Prediction log — Feature Batch C (calibration: prediction vs reality).
// Records runtime predictions; outcome.go scores them against incidents.
package calibrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/fsutil"
)

// Prediction is one deterministic what-if/impact/verify prediction.
type Prediction struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"` // "impact" (what-if folds in) | "verify"
	Change         string    `json:"change,omitempty"`
	Target         string    `json:"target,omitempty"`
	Subsystem      string    `json:"subsystem"`
	PredictedFiles []string  `json:"predicted_files,omitempty"`
	Verdict        string    `json:"verdict,omitempty"`
	TS             time.Time `json:"ts"`
}

// predictionLogCap trims the prediction/outcome logs to newest entries.
const predictionLogCap = 5000

func predictionLogPath(root string) string { return filepath.Join(root, ".kern", "predictions.jsonl") }

// SubsystemOf maps a repo-relative file path to the ARCHITECTURE.md subsystem
// name: paths under "internal/" use the first TWO segments ("internal/loop/loop.go"
// → "internal/loop"); every other path uses its FIRST segment ("cmd/kern/flags.go"
// → "cmd", "go.mod" → "go.mod").
func SubsystemOf(file string) string {
	parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(file), "./"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	if parts[0] == "internal" && len(parts) > 1 {
		return "internal/" + parts[1]
	}
	return parts[0]
}

// predictionID is the short deterministic ID over kind|change|target|ts.
func predictionID(p Prediction) string {
	return shortHash(p.Kind, p.Change, p.Target, p.TS.UTC().Format(time.RFC3339Nano))
}

// shortHash derives the deterministic short hex ID over the parts.
func shortHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:6])
}

// RecordPrediction appends one prediction to <root>/.kern/predictions.jsonl
// (append-only JSONL, 0600), keeping the newest predictionLogCap entries.
func RecordPrediction(root string, p Prediction) error {
	if p.TS.IsZero() {
		p.TS = time.Now()
	}
	p.ID = predictionID(p)
	line, err := json.Marshal(p)
	if err != nil {
		return err
	}
	path := predictionLogPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return appendJSONLLine(path, line, predictionLogCap)
}

// appendJSONLLine appends one JSON line to path, keeping at most cap lines.
func appendJSONLLine(path string, line []byte, cap int) error {
	lines, err := readJSONLines(path)
	if err != nil {
		return err
	}
	if len(lines) >= cap {
		lines = lines[len(lines)-cap+1:]
	}
	lines = append(lines, line)
	var b strings.Builder
	for _, ln := range lines {
		b.Write(ln)
		b.WriteByte('\n')
	}
	return fsutil.WriteFileAtomic(path, []byte(b.String()), 0o600)
}

func readJSONLines(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out [][]byte
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, []byte(ln))
		}
	}
	return out, nil
}
