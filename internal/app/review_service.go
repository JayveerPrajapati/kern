// Review-family application services: the deterministic, no-LLM review
// orchestration shared by the CLI surface (`kern review`, `kern plan`,
// `kern impact`, `kern what-if`). These flows were previously carried by
// cmd/kern/cmd_review.go; they live here so the interface layer stays a thin
// shell over the app layer (invariant: interfaces contain no core
// orchestration).

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/lenses"
	"github.com/JayveerPrajapati/kern/internal/profiles"
	"github.com/JayveerPrajapati/kern/internal/runtime"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// ReviewOptions configures the `kern review` orchestration: the token budget
// for the rendered review, the runtime-overlay flag, and the lens/profile
// filters (both deterministic, no LLM).
type ReviewOptions struct {
	Max     int    // token budget for the prioritized review context
	Runtime bool   // overlay each changed file with its service profile
	Lens    string // named review lens ("" = no lens line prepended)
	Profile string // named presentation profile ("" = no profile)
	Root    string // project root, for runtime overlay + user profiles
}

// ReviewChanges orchestrates the `kern review` flow: analyze the changed
// files for risk (the ChangesReport), render the prioritized review context,
// and apply the runtime-overlay / lens / profile filters. Deterministic, no
// LLM. The JSON surface skips this entirely (report-only), so lens/profile
// resolution errors are only reachable on the text path — same contract the
// CLI previously had.
func ReviewChanges(ix *index.Index, changes []intel.FileChange, opts ReviewOptions) (*intel.ChangesReport, string, error) {
	// --runtime overlays each changed file with its service profile
	// (directory base name matched against runtime service names).
	var out string
	if opts.Runtime {
		out = intel.ReviewRanged(ix, changes, opts.Max, runtime.Overlay(runtime.LoadSource(opts.Root)))
	} else {
		out = intel.ReviewRanged(ix, changes, opts.Max)
	}
	// Review lens (mirrors the kern_review MCP tool): a named lens
	// prepends its evidence-priority line so the caller knows which
	// review posture the context is sized for. No lens arg -> output
	// unchanged.
	if opts.Lens != "" {
		l, err := lenses.Resolve(opts.Lens)
		if err != nil {
			return nil, "", err
		}
		out = fmt.Sprintf("lens: %s (%s)\n", l.Name, lenses.RenderPriorities(l)) + out
	}
	// --profile shapes how the review is presented without changing the
	// findings (deterministic, no LLM).
	if opts.Profile != "" {
		p, ok := profiles.NewRegistryWithUserProfiles(opts.Root).Select(opts.Profile)
		if !ok {
			return nil, "", fmt.Errorf("unknown profile %q", opts.Profile)
		}
		out = profiles.ApplyProfile(p, out)
	}
	return intel.AnalyzeChangesRanged(ix, changes), out, nil
}

// ParseChangeKind derives the hypothetical change kind from the first word of
// a change description (case-insensitive). Falls back to RemoveSymbol, matching
// the historical default, when the wording is unrecognized.
func ParseChangeKind(change string) whatif.ChangeKind {
	fields := strings.Fields(change)
	if len(fields) == 0 {
		return whatif.RemoveSymbol
	}
	word := strings.ToLower(fields[0])
	switch word {
	case "remove", "delete", "drop":
		return whatif.RemoveSymbol
	case "change", "modify", "update", "refactor", "rewrite", "replace":
		return whatif.ChangeSignature
	case "add", "create", "introduce", "new":
		return whatif.AddSymbol
	case "rename":
		return whatif.RenameSymbol
	case "move":
		return whatif.MoveModule
	case "split":
		return whatif.SplitService
	default:
		return whatif.RemoveSymbol
	}
}

// AnnotateImpactCallees relabels the "What it calls" section of a rendered
// impact report, marking each entry "(direct)" or "(transitive)" using the
// index's direct call edges (the report listed transitive callees of
// the target — callees of callees — alongside direct ones with no way to tell
// them apart). The text is returned unchanged when the index is unavailable
// or the target has no recorded call edges, so the annotation never degrades
// the report.
func AnnotateImpactCallees(text, target, root string) string {
	ix, err := index.Load(root)
	if err != nil {
		return text
	}
	direct := map[string]bool{}
	for _, ce := range ix.Calls[target] {
		direct[simpleSymName(ce.Target)] = true
	}
	if len(direct) == 0 {
		// The target may be typed qualified while the index keys it by the
		// simple name (or vice versa); retry with the bare name.
		for _, ce := range ix.Calls[simpleSymName(target)] {
			direct[simpleSymName(ce.Target)] = true
		}
	}
	if len(direct) == 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	inCalls := false
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "What it calls:") {
			inCalls = true
			continue
		}
		if !inCalls {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			inCalls = false // next section
			continue
		}
		entry := strings.TrimSpace(trimmed[2:])
		// skip renderer summary lines (stdlib collapse, "+N more") so
		// they are never mislabeled as transitive callees.
		if strings.HasPrefix(entry, "stdlib:") || strings.Contains(entry, "use --json for full list") {
			continue
		}
		name := simpleSymName(entry)
		if direct[name] {
			lines[i] = ln + " (direct)"
		} else {
			lines[i] = ln + " (transitive)"
		}
	}
	return strings.Join(lines, "\n")
}

// simpleSymName returns the part of a name after the last '.', so qualified
// ("repo.Query") and bare ("Query") spellings compare equal.
func simpleSymName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

var (
	backtickCmdRe = regexp.MustCompile("`kern ([a-z0-9-]+)`")
	// planRenameRe / planRemoveRe detect explicit change kinds in a plan
	// intent (mirror internal/app's assemblePlan detection so CLI and MCP
	// plan outputs agree on the leading concrete steps).
	planRenameRe = regexp.MustCompile(`(?i)rename\s+([\w.]+)\s+to\s+([\w.]+)`)
	planRemoveRe = regexp.MustCompile(`(?i)(?:remove|delete)\s+([\w.]+)`)
	plainCmdRe   = regexp.MustCompile(`\bkern ([a-z0-9-]+) (?:cli )?(?:sub)?command`)
)

// cliCommandName extracts a new CLI subcommand name from a plan intent, e.g.
// "Add a `kern dogfood` CLI command ..." → "dogfood". Detection is
// conservative: a backticked `kern <name>` reference, or a "kern <name>
// command/subcommand" phrasing (with optional "CLI"). Returns "" when the
// intent is not about a new kern CLI command, so generic net-new feature
// plans keep the generic steps.
func cliCommandName(change string) string {
	if m := backtickCmdRe.FindStringSubmatch(change); m != nil {
		return m[1]
	}
	if m := plainCmdRe.FindStringSubmatch(strings.ToLower(change)); m != nil {
		return m[1]
	}
	return ""
}

// planLayout describes the target repository layout detected for adaptive
// plan rendering (dogfooding F6): kern-style CLI conventions are only emitted
// when the target repo actually has them, so a plan never leaks kern's own
// structure into a foreign codebase.
type planLayout struct {
	kernCLI   bool // cmd/kern/ directory exists at root
	changelog bool // CHANGELOG.md exists at root
	gomodRoot bool // go.mod exists at root (module root)
}

// detectPlanLayout probes the target repo root for the layout markers that
// gate kern-style plan steps. Deterministic, no LLM: pure filesystem checks.
func detectPlanLayout(root string) planLayout {
	if root == "" {
		root = "."
	}
	var l planLayout
	if st, err := os.Stat(filepath.Join(root, "cmd", "kern")); err == nil && st.IsDir() {
		l.kernCLI = true
	}
	if st, err := os.Stat(filepath.Join(root, "CHANGELOG.md")); err == nil && !st.IsDir() {
		l.changelog = true
	}
	if st, err := os.Stat(filepath.Join(root, "go.mod")); err == nil && !st.IsDir() {
		l.gomodRoot = true
	}
	return l
}

// RenderStatelessPlan renders a domain.Plan-shaped text from a context packet
// for the stateless `kern plan` path (no --task flag). It mirrors the
// TaskService.Plan output shape so callers see the same sections regardless
// of whether a Task was created. Implementation/verification steps are
// adaptive to the target repo layout (F6): the concrete kern CLI conventions
// (cmd/kern/cmd_%s.go, dispatch_table.go, CHANGELOG.md [Unreleased], ./cmd/kern
// tests) are only emitted when the target repo matches that layout, otherwise
// generic implement/test/document steps are used.
func RenderStatelessPlan(change string, pkt domain.ContextPacket, root string) string {
	layout := detectPlanLayout(root)
	kernStyle := layout.kernCLI && layout.changelog && layout.gomodRoot
	var b strings.Builder
	fmt.Fprintf(&b, "PLAN\n")
	fmt.Fprintf(&b, "Objective: %s\n", change)
	if whatif.IsNetNewFeature(change) {
		fmt.Fprintf(&b, "Scope: net-new feature (no existing components affected)\n")
	} else {
		fmt.Fprintf(&b, "Scope: %d symbols, %d files\n", len(pkt.Symbols), len(pkt.Files))
	}
	risk := "low"
	for _, r := range pkt.Risks {
		if r.Level == domain.RiskCritical || r.Level == domain.RiskHigh {
			risk = "high"
			break
		}
		if r.Level == domain.RiskMedium {
			risk = "medium"
		}
	}
	fmt.Fprintf(&b, "Risk: %s\n", risk)
	if !whatif.IsNetNewFeature(change) {
		fmt.Fprintf(&b, "Affected components:\n")
		for _, sym := range pkt.Symbols {
			fmt.Fprintf(&b, "  - %s\n", sym.Name)
		}
		for _, f := range pkt.Files {
			fmt.Fprintf(&b, "  - %s\n", f.Path)
		}
	}
	fmt.Fprintf(&b, "Implementation steps:\n")
	if whatif.IsNetNewFeature(change) {
		if name := cliCommandName(change); name != "" && kernStyle {
			fmt.Fprintf(&b, "  1. Implement the command in a new file cmd/kern/cmd_%s.go.\n", name)
			fmt.Fprintf(&b, "  2. Register %q in the command table cmd/kern/dispatch_table.go, following the existing entry pattern ({run: func(cmd string, rest []string) int {...}}).\n", name)
			fmt.Fprintf(&b, "  3. Add tests in cmd/kern/cmd_%s_test.go (house helpers: captureStdout, exitError sentinel).\n", name)
			fmt.Fprintf(&b, "  4. Document the command in CHANGELOG.md [Unreleased].\n")
		} else {
			fmt.Fprintf(&b, "  1. Implement the feature in a new file under the relevant package.\n")
			fmt.Fprintf(&b, "  2. Add unit tests alongside the new code.\n")
			if layout.changelog {
				fmt.Fprintf(&b, "  3. Document the change in CHANGELOG.md [Unreleased].\n")
			} else {
				fmt.Fprintf(&b, "  3. Document the change in the project's release notes or changelog, if any.\n")
			}
		}
	} else {
		stepN := 0
		emitStep := func(s string) {
			stepN++
			fmt.Fprintf(&b, "  %d. %s\n", stepN, s)
		}
		// Concrete steps mirror the internal/app assemblePlan detection so
		// CLI and MCP plans agree: explicit rename/remove kinds lead, then
		// per-symbol updates, then the validation steps under Tests.
		if m := planRenameRe.FindStringSubmatch(change); m != nil {
			emitStep(fmt.Sprintf("Rename %s to %s (definition in the affected components above)", m[1], m[2]))
			emitStep("Update all references to " + m[1])
		} else if m := planRemoveRe.FindStringSubmatch(change); m != nil {
			emitStep("Remove " + m[1] + " and update its callers")
		}
		for i, sym := range pkt.Symbols {
			if i >= 5 {
				break
			}
			if strings.Contains(sym.File, "_test") {
				continue
			}
			emitStep(fmt.Sprintf("Update %s (%s:%d)", sym.Name, sym.File, sym.Line))
		}
		if stepN == 0 {
			emitStep("Implement the requested change.")
		}
	}
	// Packet validation items are rendered once, under Tests below — the old
	// version repeated every item under Implementation steps as well.
	if whatif.IsNetNewFeature(change) {
		fmt.Fprintf(&b, "Verification:\n")
		switch {
		case kernStyle && cliCommandName(change) != "":
			fmt.Fprintf(&b, "  - go build ./...\n")
			fmt.Fprintf(&b, "  - go test ./cmd/kern/ -count=1\n")
			fmt.Fprintf(&b, "  - go vet ./cmd/kern/\n")
		case layout.gomodRoot:
			fmt.Fprintf(&b, "  - go build ./...\n")
			fmt.Fprintf(&b, "  - go test ./... -count=1 (or the affected package)\n")
			fmt.Fprintf(&b, "  - go vet ./...\n")
		default:
			fmt.Fprintf(&b, "  - run the project's own build and test commands\n")
		}
	}
	if len(pkt.Risks) > 0 {
		fmt.Fprintf(&b, "Rollback: revert the commit")
		if risk == "high" {
			b.WriteString(" and redeploy previous version")
		}
		b.WriteString("\n")
	}
	if len(pkt.RequiredValidation) > 0 {
		fmt.Fprintf(&b, "Tests:\n")
		for _, t := range pkt.RequiredValidation {
			fmt.Fprintf(&b, "  - %s\n", t)
		}
	}
	return b.String()
}
