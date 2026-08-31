package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// fingerprintRecord is the machine-readable shape emitted by `kern fingerprint
// --json`. It is the contract consumed by the blueprint duplication pipeline;
// field names and semantics stay in lockstep with intel.Fingerprint.
type fingerprintRecord struct {
	File           string              `json:"file"`
	Name           string              `json:"name"`
	SignatureShape string              `json:"signature_shape"`
	ParamCount     int                 `json:"param_count"`
	ReturnCount    int                 `json:"return_count"`
	CalledSymbols  []string            `json:"called_symbols"`
	LiteralCount   int                 `json:"literal_count"`
	StatementCount int                 `json:"statement_count"`
	Lang           string              `json:"lang"`
	Line           int                 `json:"line"`
	ControlFlow    intel.CFFingerprint `json:"control_flow"`
}

// fatal2 prints an error to stderr and exits with code 2 (tool error). Used by
// runFingerprint, which is a data command: it never gates, so a tool error is
// a usage-style failure (exit 2) rather than a gate failure.
func fatal2(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 2})
}

// runFingerprint implements `kern fingerprint [root] [--file f1,f2] [--json]`.
//
// It emits structural fingerprints for Go functions (signature shape,
// control-flow shape, called symbols, literal and statement counts) — the
// input blueprint's duplication similarity pipeline consumes. The command is a
// pure data command: it exits 0 on success and never acts as a gate.
//
// File selection: --file paths are relative to root and joined with it;
// otherwise every *.go file under root is walked (hidden dirs and
// vendor/node_modules-style trees are skipped, mirroring the index's policy).
// Unparsable Go files are skipped silently (the index is tolerant of broken
// files); unreadable files are a tool error (exit 2). Non-Go files are skipped
// entirely: structural fingerprints are Go-only in v1 — non-Go coverage is
// future work.
func runFingerprint(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
		if len(args) > 0 {
			root = args[0]
		}
	}

	var files []string
	if f.file != "" {
		for _, p := range strings.Split(f.file, ",") {
			if p = strings.TrimSpace(p); p != "" {
				files = append(files, p)
			}
		}
	} else {
		files, err = collectGoFiles(root)
		if err != nil {
			fatal2("%v", err)
		}
	}

	var out []fingerprintRecord
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			fatal2("fingerprint: %s: %v", rel, err)
		}
		fps, err := intel.ComputeFingerprint(string(src))
		if err != nil {
			// Unparsable Go file: skip silently — the index is tolerant of
			// broken files and so is the fingerprint oracle.
			continue
		}
		for _, fp := range fps {
			out = append(out, fingerprintRecord{
				File:           rel,
				Name:           fp.FuncName,
				SignatureShape: fp.SignatureShape,
				ParamCount:     fp.ParamCount,
				ReturnCount:    fp.ReturnCount,
				CalledSymbols:  fp.CalledSymbols,
				LiteralCount:   fp.LiteralCount,
				StatementCount: fp.StatementCount,
				Lang:           "go",
				Line:           fp.Line,
				ControlFlow:    fp.ControlFlow,
			})
		}
	}

	if f.json {
		printJSON(map[string]any{
			"schema_version": kernJSONContractVersion,
			"fingerprints":   out,
		})
		return
	}
	for _, r := range out {
		cf := r.ControlFlow
		fmt.Printf("%s:%d %s (%s) [%d params, %d returns, %d stmts, %d literals, %d calls] cf{if:%d,for:%d,range:%d,switch:%d,return:%d,defer:%d,go:%d,assign:%d,call:%d}\n",
			r.File, r.Line, r.Name, r.SignatureShape,
			r.ParamCount, r.ReturnCount, r.StatementCount, r.LiteralCount, len(r.CalledSymbols),
			cf.IfCount, cf.ForCount, cf.RangeCount, cf.SwitchCount, cf.ReturnCount,
			cf.DeferCount, cf.GoCount, cf.AssignCount, cf.CallCount)
	}
}

// collectGoFiles walks root and returns every *.go file path relative to root,
// skipping hidden directories and vendored/generated trees (the same policy the
// index uses via index.IgnoredDir, plus any dot-prefixed directory).
func collectGoFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (index.IgnoredDir(d.Name()) || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return rerr
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
