package kern

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// FingerprintRecord is one function fingerprint emitted by
// `kern fingerprint --json` ({"schema_version":2,"fingerprints":[...]}).
// It is a structural summary of a function, independent of identifier names.
type FingerprintRecord struct {
	File           string      `json:"file"`
	Name           string      `json:"name"`
	SignatureShape string      `json:"signature_shape"`
	ParamCount     int         `json:"param_count"`
	ReturnCount    int         `json:"return_count"`
	CalledSymbols  []string    `json:"called_symbols"`
	LiteralCount   int         `json:"literal_count"`
	StatementCount int         `json:"statement_count"`
	Lang           string      `json:"lang"`
	Line           int         `json:"line"`
	ControlFlow    ControlFlow `json:"control_flow"`
}

// ControlFlow captures structural control-flow counts (kern fingerprint's
// control_flow object). Snake-case keys match kern's emission exactly.
type ControlFlow struct {
	If     int `json:"if"`
	For    int `json:"for"`
	Range  int `json:"range"`
	Switch int `json:"switch"`
	Return int `json:"return"`
	Defer  int `json:"defer"`
	Go     int `json:"go"`
	Assign int `json:"assign"`
	Call   int `json:"call"`
}

// Fingerprints runs `kern fingerprint [root] [--file f1,f2] --json` in workdir
// and returns the parsed records. It is the duplication oracle: blueprint no
// longer parses Go itself; the fingerprint computation lives in kern and
// blueprint consumes its output.
//
// With an empty files argument it runs a whole-root scan
// (`kern fingerprint <workdir> --json`). With non-empty files it batches the
// set into chunks of at most guardBatchSize files per exec (the same chunking
// GuardCheckFiles uses) via `kern fingerprint <workdir> --file f1,f2 --json`
// and merges the batches.
//
// The JSON contract is checked like GuardCheck: any missing/wrong schema
// version fails closed so a version skew surfaces as an error, never as a
// silent misparse. Exit code 0 is success; exit code 2 is a tool error and any
// other non-zero code is treated the same way. Records with a missing or
// non-"go" lang are kept — the similarity pipeline decides what to compare.
func (c *KernClient) Fingerprints(ctx context.Context, workdir string, files []string) ([]FingerprintRecord, error) {
	if len(files) == 0 {
		return c.fingerprintsOnce(ctx, workdir, nil)
	}

	var all []FingerprintRecord
	for i := 0; i < len(files); i += guardBatchSize {
		end := i + guardBatchSize
		if end > len(files) {
			end = len(files)
		}
		recs, err := c.fingerprintsOnce(ctx, workdir, files[i:end])
		if err != nil {
			return all, fmt.Errorf("kern fingerprint (batch %d-%d): %w", i+1, end, err)
		}
		all = append(all, recs...)
	}
	return all, nil
}

// fingerprintsOnce runs a single `kern fingerprint` invocation for the given
// files (nil files means a whole-root scan) and parses the versioned payload.
func (c *KernClient) fingerprintsOnce(ctx context.Context, workdir string, files []string) ([]FingerprintRecord, error) {
	args := []string{"fingerprint", workdir}
	if len(files) > 0 {
		args = append(args, "--file", strings.Join(files, ","))
	}
	args = append(args, "--json")

	out, errOut, code, runErr := c.runner(ctx, c.binaryPath, args, workdir)
	if runErr != nil {
		return nil, fmt.Errorf("kern fingerprint: %w", runErr)
	}
	if code != 0 {
		// kern fingerprint: exit 0 = success, exit 2 = tool error; any other
		// code is treated the same way (tool failure, never a results payload).
		return nil, fmt.Errorf("kern fingerprint failed (exit %d): %s", code, strings.TrimSpace(errOut))
	}

	var payload struct {
		SchemaVersion int                 `json:"schema_version"`
		Fingerprints  []FingerprintRecord `json:"fingerprints"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return nil, fmt.Errorf("kern fingerprint: parse output: %w", err)
	}
	if payload.SchemaVersion != KernContractVersion {
		return nil, fmt.Errorf("kern fingerprint: contract mismatch: expected schema_version %d, got %d (missing schema_version means the installed kern is too old — upgrade kern or pin KERN_BINARY)", KernContractVersion, payload.SchemaVersion)
	}
	return payload.Fingerprints, nil
}
