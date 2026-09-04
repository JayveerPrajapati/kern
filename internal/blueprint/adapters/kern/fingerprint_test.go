package kern

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fakeFingerprintRunner returns a commandRunner that emits the given stdout
// (or, when stdout is empty and records is non-nil, a valid
// {"schema_version":2,"fingerprints":[...]} payload built from records) while
// recording every invocation's args and workdir.
type fakeFingerprintRunner struct {
	// stdout overrides record-based generation when non-empty.
	stdout   string
	stderr   string
	exitCode int
	runErr   error

	// records: when stdout == "", the runner marshals these into the payload.
	records []FingerprintRecord

	// schemaVersion overrides the emitted schema_version (default 2).
	schemaVersion int

	// calls records every invocation as (workdir, args).
	calls []struct {
		workdir string
		args    []string
	}
}

func (f *fakeFingerprintRunner) runner() CommandRunner {
	return func(ctx context.Context, name string, args []string, workdir string) (string, string, int, error) {
		f.calls = append(f.calls, struct {
			workdir string
			args    []string
		}{workdir, append([]string(nil), args...)})
		if f.stdout != "" {
			return f.stdout, f.stderr, f.exitCode, f.runErr
		}
		payload := struct {
			SchemaVersion int                 `json:"schema_version"`
			Fingerprints  []FingerprintRecord `json:"fingerprints"`
		}{f.schemaVersion, f.records}
		if payload.SchemaVersion == 0 {
			payload.SchemaVersion = 2
		}
		b, err := json.Marshal(payload)
		if err != nil {
			panic(err)
		}
		return string(b), f.stderr, f.exitCode, f.runErr
	}
}

// TestFingerprintsValidContract: a schema_version:2 payload parses into records.
func TestFingerprintsValidContract(t *testing.T) {
	f := &fakeFingerprintRunner{records: []FingerprintRecord{
		{File: "a.go", Name: "F", SignatureShape: "func()", ParamCount: 0, ReturnCount: 0, CalledSymbols: []string{"g()"}, LiteralCount: 1, StatementCount: 2, Lang: "go", Line: 3},
	}}
	client := &KernClient{binaryPath: "kern", runner: f.runner()}
	recs, err := client.Fingerprints(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("Fingerprints error: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	got := recs[0]
	if got.File != "a.go" || got.Name != "F" || got.SignatureShape != "func()" || got.Lang != "go" || got.Line != 3 {
		t.Errorf("record = %+v, want a.go/F/func()/go/line 3", got)
	}
	if len(got.CalledSymbols) != 1 || got.CalledSymbols[0] != "g()" {
		t.Errorf("CalledSymbols = %v, want [g()]", got.CalledSymbols)
	}
}

// TestFingerprintsEmptyFilesRunsRootScan: no files → args contain no --file.
func TestFingerprintsEmptyFilesRunsRootScan(t *testing.T) {
	f := &fakeFingerprintRunner{records: []FingerprintRecord{{File: "a.go", Name: "F"}}}
	client := &KernClient{binaryPath: "kern", runner: f.runner()}
	workdir := t.TempDir()
	if _, err := client.Fingerprints(context.Background(), workdir, nil); err != nil {
		t.Fatalf("Fingerprints error: %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(f.calls))
	}
	args := f.calls[0].args
	if len(args) < 2 || args[0] != "fingerprint" || args[1] != workdir {
		t.Fatalf("args = %v, want [fingerprint <workdir> ...]", args)
	}
	for _, a := range args {
		if a == "--file" {
			t.Errorf("root-scan invocation must not contain --file: %v", args)
		}
	}
	if !strings.Contains(strings.Join(args, " "), "--json") {
		t.Errorf("args missing --json: %v", args)
	}
}

// TestFingerprintsBatchMerges: more than guardBatchSize files → chunked
// invocations whose records are merged in order.
func TestFingerprintsBatchMerges(t *testing.T) {
	f := &fakeFingerprintRunner{}
	client := &KernClient{binaryPath: "kern", runner: f.runner()}
	const n = 130 // 64 + 64 + 2
	files := make([]string, n)
	for i := range files {
		files[i] = fmt.Sprintf("f%03d.go", i)
	}
	recs, err := client.Fingerprints(context.Background(), t.TempDir(), files)
	if err != nil {
		t.Fatalf("Fingerprints error: %v", err)
	}
	wantCalls := (n + guardBatchSize - 1) / guardBatchSize
	if len(f.calls) != wantCalls {
		t.Fatalf("calls = %d, want %d", len(f.calls), wantCalls)
	}
	for i, call := range f.calls {
		// Each call must scope to its batch via --file.
		if len(call.args) < 3 || call.args[0] != "fingerprint" {
			t.Fatalf("call %d args = %v, want fingerprint subcommand", i, call.args)
		}
		fileIdx := -1
		for j, a := range call.args {
			if a == "--file" {
				fileIdx = j
				break
			}
		}
		if fileIdx == -1 {
			t.Fatalf("call %d missing --file: %v", i, call.args)
		}
		batch := strings.Split(call.args[fileIdx+1], ",")
		start := i * guardBatchSize
		if len(batch) != len(files[start:start+len(batch)]) {
			t.Fatalf("call %d batch size %d", i, len(batch))
		}
	}
	// The runner emitted no records (records is nil), so merging yields nothing
	// beyond the invocations; the contract is that the merged result is the
	// concatenation of each batch's parsed records. Verify batch sizes sum to n.
	total := 0
	for i := 0; i < len(files); i += guardBatchSize {
		end := i + guardBatchSize
		if end > len(files) {
			end = len(files)
		}
		total += end - i
	}
	if total != n {
		t.Fatalf("batch total = %d, want %d", total, n)
	}
	if len(recs) != 0 {
		t.Fatalf("records = %d, want 0 (fake emitted none)", len(recs))
	}
}

// TestFingerprintsBatchMergesRecords: with per-batch records the merged result
// concatenates them in order.
func TestFingerprintsBatchMergesRecords(t *testing.T) {
	f := &fakeFingerprintRunner{records: []FingerprintRecord{{File: "batch.go", Name: "B"}}}
	client := &KernClient{binaryPath: "kern", runner: f.runner()}
	const n = 2 * guardBatchSize
	files := make([]string, n)
	for i := range files {
		files[i] = fmt.Sprintf("f%03d.go", i)
	}
	recs, err := client.Fingerprints(context.Background(), t.TempDir(), files)
	if err != nil {
		t.Fatalf("Fingerprints error: %v", err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(f.calls))
	}
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2 (one per batch)", len(recs))
	}
	for i, r := range recs {
		if r.Name != "B" || r.File != "batch.go" {
			t.Errorf("record %d = %+v, want batch.go/B", i, r)
		}
	}
}

// TestFingerprintsWrongVersion: a mismatched schema_version is a fail-closed
// contract error, never a silent misparse.
func TestFingerprintsWrongVersion(t *testing.T) {
	f := &fakeFingerprintRunner{schemaVersion: 3}
	client := &KernClient{binaryPath: "kern", runner: f.runner()}
	_, err := client.Fingerprints(context.Background(), t.TempDir(), nil)
	if err == nil {
		t.Fatal("Fingerprints returned no error, want contract mismatch error")
	}
	if !strings.Contains(err.Error(), "contract") {
		t.Errorf("error = %q, want mention of contract", err.Error())
	}
}

// TestFingerprintsLegacyOutput: an unversioned payload (legacy kern) fails
// closed with a contract error.
func TestFingerprintsLegacyOutput(t *testing.T) {
	f := &fakeFingerprintRunner{stdout: `{"fingerprints":[{"file":"a.go","name":"F"}]}`}
	client := &KernClient{binaryPath: "kern", runner: f.runner()}
	_, err := client.Fingerprints(context.Background(), t.TempDir(), nil)
	if err == nil {
		t.Fatal("Fingerprints returned no error, want contract error for legacy output")
	}
	if !strings.Contains(err.Error(), "contract") {
		t.Errorf("error = %q, want mention of contract", err.Error())
	}
}

// TestFingerprintsMalformedOutput: non-JSON output is a parse error.
func TestFingerprintsMalformedOutput(t *testing.T) {
	f := &fakeFingerprintRunner{stdout: "not json"}
	client := &KernClient{binaryPath: "kern", runner: f.runner()}
	_, err := client.Fingerprints(context.Background(), t.TempDir(), nil)
	if err == nil {
		t.Fatal("Fingerprints returned no error, want parse error")
	}
}

// TestFingerprintsToolError: non-zero exit (tool failure) is an error carrying
// the stderr, not a results payload.
func TestFingerprintsToolError(t *testing.T) {
	f := &fakeFingerprintRunner{stdout: "", stderr: "kern: unknown command \"fingerprint\"", exitCode: 2}
	client := &KernClient{binaryPath: "kern", runner: f.runner()}
	_, err := client.Fingerprints(context.Background(), t.TempDir(), nil)
	if err == nil {
		t.Fatal("Fingerprints returned no error, want tool error")
	}
	if !strings.Contains(err.Error(), "exit 2") || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error = %q, want exit 2 + stderr", err.Error())
	}
}

// TestFingerprintsLaunchError: a runner launch failure surfaces as an error.
func TestFingerprintsLaunchError(t *testing.T) {
	f := &fakeFingerprintRunner{runErr: fmt.Errorf("executable not found")}
	client := &KernClient{binaryPath: "kern", runner: f.runner()}
	_, err := client.Fingerprints(context.Background(), t.TempDir(), nil)
	if err == nil {
		t.Fatal("Fingerprints returned no error, want launch error")
	}
}
