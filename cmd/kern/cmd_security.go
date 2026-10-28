package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/contextcli"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/sec"
	"github.com/JayveerPrajapati/kern/internal/secscan"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

func runMask(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	var in string
	if len(args) > 0 && args[0] != "" {
		in = args[0]
	}
	var b []byte
	if in == "" || in == "-" {
		b, err = cliflags.ReadStdin()
	} else if st, serr := os.Stat(in); serr == nil && !st.IsDir() {
		b, err = os.ReadFile(in)
	} else {
		// Not an existing file: treat the argument as inline text to mask.
		b = []byte(in)
	}
	if err != nil {
		fatal("mask: %v", err)
	}
	res := pii.MaskAllCustom(string(b), pii.DefaultPatterns, splitNames(f.names))
	fmt.Print(res.Text)
	if res.Replaced > 0 {
		fmt.Fprintf(os.Stderr, "\nkern: masked %d secrets: ", res.Replaced)
		var parts []string
		for k, v := range res.ByLabel {
			parts = append(parts, fmt.Sprintf("%s %d", k, v))
		}
		fmt.Fprint(os.Stderr, strings.Join(parts, ", "))
		fmt.Fprintln(os.Stderr)
	}

}

// secSummaryLine renders the one-line summary. Under the default error-only
// lens it reports the pre-filter total so a hidden warning/info finding is
// never presented as "0 findings (0 warning)".
func secSummaryLine(shown int, counts map[string]int, defaultLens bool) string {
	total := counts["error"] + counts["warning"] + counts["info"]
	if defaultLens {
		return fmt.Sprintf("kern sec: %d shown of %d findings (%d error, %d warning, %d info; default lens: error) [use --severity error,warning,info to view all]",
			shown, total, counts["error"], counts["warning"], counts["info"])
	}
	return fmt.Sprintf("kern sec: %d findings (%d error, %d warning, %d info)",
		shown, counts["error"], counts["warning"], counts["info"])
}

// runSec implements `kern sec` — the standalone security scan (the bridge
// command the blueprint standalone repo consumes via its adapters/kern
// client: `kern sec --json <path>`). Restored 2026-10-18 (P7 blueprint
// reconciliation) after the pivot CLI slim removed it: kern keeps the
// engines (internal/sec, internal/secscan) for `kern verify security`, and
// blueprint's 21k-LOC integration harness (G3/G17) tests this command
// against the real binary. Thin, shared logic — the scan itself is
// internal/secscan.
func runSec(rest []string) {
	f, args := parseFlagsOrDie(rest)
	root := projectRoot(f)
	if f.root == "" && len(args) > 0 {
		root = args[0]
	}
	// Single-file mode: `kern sec <file>` scans exactly that file. Previously
	// the positional was handed to the directory walker as a root, so every
	// lookup probed <file>/.kern/... and failed with "not a directory"
	// (P0-2). Keep the file's directory as the reporting/suppression root.
	target := root
	singleFile := false
	if info, statErr := os.Stat(root); statErr == nil && !info.IsDir() {
		abs, aerr := filepath.Abs(root)
		if aerr != nil {
			abs = root
		}
		singleFile = true
		target = abs
		root = filepath.Dir(abs)
	}
	maxN := f.max
	// Default gate is error-only: warnings/info are triage material, not CI
	// failures. Pass --severity explicitly (or --severity all) to widen the lens.
	allow := []string{"error"}
	if f.severity != "" {
		if strings.ToLower(f.severity) == "all" {
			allow = nil
		} else {
			allow = strings.Split(f.severity, ",")
		}
	}
	// --engine selects the scan engines: internal|gosec|all|none. The
	// default (all) runs the internal regex scanner plus gosec when a binary
	// resolves — gosec is advisory (Model C): a missing binary or timeout
	// degrades to SKIPPED, never a hard failure, and its findings flow
	// through the same severity lens as internal ones. "none" and "internal"
	// are internal-only; "gosec" forces the external engine (which reports
	// SKIPPED when unavailable).
	wantGosec := false
	switch f.engine {
	case "gosec":
		wantGosec = true
	case "all", "":
		wantGosec = sec.GosecAvailable()
	}
	// gosec is a package/directory scanner; a single-file request skips it
	// rather than scanning the whole parent directory.
	if singleFile {
		wantGosec = false
	}
	findings, serr := secscan.ScanOne(root, target)
	if serr != nil {
		fatal("kern sec: %v", serr)
	}
	// gosec findings append BEFORE the severity lens so the default
	// error-only gate governs them too (the safety: an external engine can
	// never widen a gate that was closed).
	var gres sec.GosecResult
	if wantGosec {
		gres = sec.RunGosec(root)
		findings = append(findings, gres.Findings...)
	}
	allCounts := secscan.Counts(findings)
	findings = secscan.FilterBySeverity(findings, allow)
	// Suppression triage (M-1, production-readiness audit 2026-10-17): apply
	// the SAME registry `kern verify security` uses, so a triaged finding
	// cannot fail one surface and pass the other. Suppressed findings stay
	// visible — marked [suppressed] with their reason — but never count
	// toward the error gate.
	reg, err := verification.LoadSuppressionRegistry(root)
	if err != nil {
		fatal("kern sec: %v", err)
	}
	type triagedFinding struct {
		secscan.Finding
		Reason string `json:"reason"`
	}
	suppressedFindings := []triagedFinding{}
	kept := findings[:0]
	for _, fd := range findings {
		if reason, ok := reg.Match(fd); ok {
			suppressedFindings = append(suppressedFindings, triagedFinding{Finding: fd, Reason: reason})
			continue
		}
		kept = append(kept, fd)
	}
	findings = kept
	counts := secscan.Counts(findings)
	if f.json {
		out := map[string]any{
			"schema_version": contextcli.KernJSONContractVersion,
			"findings":       findings,
			"suppressed":     suppressedFindings,
		}
		if wantGosec {
			out["engines"] = map[string]any{
				"gosec": map[string]any{
					"status": gres.Status,
					"detail": gres.Detail,
					"count":  len(gres.Findings),
				},
			}
		}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			fatal("sec: %v", err)
		}
	} else {
		fmt.Print(secscan.Render(findings, maxN))
		for _, s := range suppressedFindings {
			fmt.Printf("[suppressed] %s:%d [%s] %s — %s\n", s.File, s.Line, s.Rule, s.Message, s.Reason)
		}
		supSuffix := ""
		if len(suppressedFindings) > 0 {
			supSuffix = fmt.Sprintf("; %d suppressed (triaged — see [suppressed] lines)", len(suppressedFindings))
		}
		if f.severity == "" {
			fmt.Fprintln(os.Stderr, secSummaryLine(len(findings), allCounts, true)+supSuffix)
		} else {
			fmt.Fprintln(os.Stderr, secSummaryLine(len(findings), counts, false)+supSuffix)
		}
		if wantGosec {
			switch gres.Status {
			case sec.EngineStatusSkipped:
				fmt.Fprintf(os.Stderr, "kern sec: gosec skipped: %s\n", gres.Detail)
			case sec.EngineStatusRan:
				// Only the default error-only lens can hide gosec findings
				// silently; surface the filtered count with the same hint
				// pattern the summary line uses.
				if f.severity == "" {
					kept := 0
					for _, fd := range findings {
						if strings.HasPrefix(fd.Rule, "gosec:") {
							kept++
						}
					}
					if kept < len(gres.Findings) {
						fmt.Fprintf(os.Stderr, "kern sec: gosec: %d finding(s) filtered by the default error-only lens [use --severity error,warning,info to view all]\n", len(gres.Findings)-kept)
					}
				}
			}
		}
	}
	// The exit code must be the same in --json and text mode: error-severity
	// findings are a findings-tier outcome (exit 1, matching the review-family
	// convention — QA F2: `kern security` findings and `kern review` risk must
	// agree; a hard CI failure remains `kern verify --types security`'s FAIL → 1).
	// Suppressed (triaged) findings never reach this gate — they are excluded
	// from counts above, so `kern sec` and `kern verify security` agree on
	// the same finding.
	// The summary line prints only in text mode: --json keeps stderr empty
	// so machine consumers can still tell findings (exit 1, empty stderr)
	// from tool errors (exit 1, stderr populated — the message text differs).
	if counts["error"] > 0 {
		if f.json {
			panic(exitError{code: 1})
		}
		fatalFindings("sec: %d error-severity finding(s)", counts["error"])
	}
}
