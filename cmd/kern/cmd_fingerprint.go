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

// fingerprintRecord is the JSON output shape for kern fingerprint.
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

// fatal2 prints an error and exits with code 2 (tool error).
func fatal2(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kern: "+format+"\n", args...)
	panic(exitError{code: 2})
}

// runFingerprint emits structural fingerprints for Go functions.
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

	out := make([]fingerprintRecord, 0, len(files))
	noFunc, unparsable, nonGo := 0, 0, 0
	for _, rel := range files {
		if f.file != "" && !strings.HasSuffix(strings.ToLower(rel), ".go") {
			// An explicitly requested --file that is not Go source:
			// fingerprints are Go-only. Say so instead of silently
			// contributing nothing (the e2e round-2 null no-op).
			fmt.Fprintf(os.Stderr, "kern: fingerprint: %s: not a Go source file — skipped (fingerprints cover Go only)\n", rel)
			nonGo++
			continue
		}
		path := rel
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, rel)
		}
		src, err := os.ReadFile(path)
		if err != nil {
			fatal2("fingerprint: %s: %v", rel, err)
		}
		fps, err := intel.ComputeFingerprint(string(src))
		if err != nil {
			// Unparsable Go file: skip — the index is tolerant of broken
			// files and so is the fingerprint oracle. Counted for the
			// empty-result summary below so the silence is explainable.
			unparsable++
			continue
		}
		if len(fps) == 0 {
			noFunc++
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

	// Empty results must be loud, not silent: a null/blank no-op looks
	// like a broken tool (e2e round 2 hit exactly that on every non-Go
	// repo). Explain WHY nothing was emitted.
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "kern: fingerprint: no Go source files found under %s (fingerprints cover Go only)\n", root)
	} else if len(out) == 0 {
		fmt.Fprintf(os.Stderr, "kern: fingerprint: no fingerprints emitted (%d file(s) scanned: %d without top-level functions, %d unparsable, %d not Go)\n",
			len(files), noFunc, unparsable, nonGo)
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
