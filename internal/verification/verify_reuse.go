package verification

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/scanners/duplication"
	"github.com/JayveerPrajapati/kern/internal/verdict"
)

// reuseThreshold is the minimum structural similarity at which a newly added
// function is flagged as a possible duplicate of an existing one. It is the
// duplication scanner's "warning" bucket tier (similarity.go Bucket):
// 0.85-0.95 is "warning", >0.95 is "block-candidate" (still advisory-only).
const reuseThreshold = 0.85

// VerifyReuse runs the reuse advisory (rung 2): newly added Go functions in
// changed/untracked files are fingerprinted with the in-process intel oracle
// and compared against every existing function in the tree; a structural
// similarity >= reuseThreshold emits a "possible duplicate" finding. Advisory
// by design (the duplication scanner never blocks, spec line 1084): OK stays
// true whenever the check ran, findings never fail the verdict, and a clean
// tree (no changed Go files) is a normal PASS — NOT a skip. Skipped is set
// only when the root is not a git repository or has no HEAD. Test files are
// never scanned on either side; unparsable files are skipped silently (the
// oracle is tolerant). Similarity applies the MinCandidateStatements size
// floor and is name-blind, so no name-based filtering is added here.
func (e *Engine) VerifyReuse() verdict.ReuseResult {
	res := verdict.ReuseResult{OK: true}
	if !gitHasHEAD(e.root) {
		res.Skipped = "reuse skipped: not a git repository"
		return res
	}
	changed, err := changedGoFiles(e.root)
	if err != nil {
		res.Skipped = "reuse skipped: not a git repository"
		return res
	}
	if len(changed) == 0 {
		// Clean tree is the normal state: an empty PASS, never a skip.
		return res
	}

	// New side: fingerprint every changed .go file. Deleted files and
	// unreadable/unparsable files contribute nothing.
	type reuseFunc struct {
		fp   duplication.Fingerprint
		file string
		line int
	}
	changedSet := make(map[string]bool, len(changed))
	var newFuncs []reuseFunc
	for _, rel := range changed {
		changedSet[rel] = true
		src, err := os.ReadFile(filepath.Join(e.root, rel))
		if err != nil {
			continue
		}
		fps, err := intel.ComputeFingerprint(string(src))
		if err != nil {
			continue // unparsable Go file: skipped silently
		}
		for _, fp := range fps {
			newFuncs = append(newFuncs, reuseFunc{fp: toDuplicationFingerprint(fp), file: rel, line: fp.Line})
		}
	}
	if len(newFuncs) == 0 {
		return res
	}

	// Existing side: every non-changed, non-test .go file under root,
	// mirroring the index's ignore conventions (vendor, node_modules, ...)
	// plus testdata.
	var existing []reuseFunc
	_ = filepath.WalkDir(e.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != e.root && (index.IgnoredDir(d.Name()) || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".go") || isTestPath(path) {
			return nil
		}
		rel, rerr := filepath.Rel(e.root, path)
		if rerr != nil || changedSet[rel] {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		fps, ferr := intel.ComputeFingerprint(string(src))
		if ferr != nil {
			return nil
		}
		for _, fp := range fps {
			existing = append(existing, reuseFunc{fp: toDuplicationFingerprint(fp), file: rel, line: fp.Line})
		}
		return nil
	})

	// Pair: for each new function, keep the best structural match.
	var findings []string
	for _, nf := range newFuncs {
		bestIdx := -1
		bestScore := 0.0
		for i := range existing {
			score := duplication.Similarity(nf.fp, existing[i].fp)
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		if bestIdx >= 0 && bestScore >= reuseThreshold {
			ef := existing[bestIdx]
			findings = append(findings, fmt.Sprintf("possible duplicate: %s:%d %s ≈ %s:%d %s (similarity %.2f)",
				nf.file, nf.line, nf.fp.FuncName, ef.file, ef.line, ef.fp.FuncName, bestScore))
		}
	}
	sort.Strings(findings)
	if len(findings) > duplication.MaxAdvisoryFindings {
		findings = findings[:duplication.MaxAdvisoryFindings]
	}
	res.Findings = findings
	return res
}

// changedGoFiles returns the sorted repo-relative paths of changed or
// untracked non-test Go files vs HEAD, from `git status --porcelain`.
// Deletions are excluded (no content to fingerprint); renames resolve to the
// new path.
func changedGoFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		// Porcelain "XY <path>": skip deletions in either column — the file
		// content is gone and there is nothing to fingerprint.
		if line[0] == 'D' || line[1] == 'D' {
			continue
		}
		path := line[3:]
		// Renames/copies: "R  old -> new" — the new path is the one that
		// exists on disk.
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		path = unquoteGitPath(path)
		if path == "" || !strings.HasSuffix(strings.ToLower(path), ".go") || isTestPath(path) {
			continue
		}
		files = append(files, path)
	}
	sort.Strings(files)
	return files, nil
}

// isTestPath reports whether a repo-relative path is a Go test file
// (mirrors the duplication scanner's isTestFile).
func isTestPath(rel string) bool {
	return strings.HasSuffix(strings.ToLower(rel), "_test.go")
}

// unquoteGitPath unquotes a git-quoted path ("a\tb.go" -> "a<TAB>b.go");
// bare paths pass through unchanged.
func unquoteGitPath(p string) string {
	if !strings.HasPrefix(p, "\"") {
		return p
	}
	if u, err := strconv.Unquote(p); err == nil {
		return u
	}
	return p
}

// toDuplicationFingerprint maps an intel fingerprint (the in-process oracle)
// onto the duplication scanner's Fingerprint so the same structural
// Similarity pipeline scores the pair. It mirrors fingerprintFromRecord
// (internal/scanners/duplication/check.go): every field maps 1:1, and
// ControlFlow maps intel's CFFingerprint onto duplication's CFFingerprint.
func toDuplicationFingerprint(fp intel.Fingerprint) duplication.Fingerprint {
	return duplication.Fingerprint{
		FuncName:       fp.FuncName,
		SignatureShape: fp.SignatureShape,
		ParamCount:     fp.ParamCount,
		ReturnCount:    fp.ReturnCount,
		ControlFlow: duplication.CFFingerprint{
			IfCount:     fp.ControlFlow.IfCount,
			ForCount:    fp.ControlFlow.ForCount,
			RangeCount:  fp.ControlFlow.RangeCount,
			SwitchCount: fp.ControlFlow.SwitchCount,
			ReturnCount: fp.ControlFlow.ReturnCount,
			DeferCount:  fp.ControlFlow.DeferCount,
			GoCount:     fp.ControlFlow.GoCount,
			AssignCount: fp.ControlFlow.AssignCount,
			CallCount:   fp.ControlFlow.CallCount,
		},
		CalledSymbols:  fp.CalledSymbols,
		LiteralCount:   fp.LiteralCount,
		StatementCount: fp.StatementCount,
	}
}
