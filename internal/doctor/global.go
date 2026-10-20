package doctor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/setup"
)

// globalHealthInput is everything checkGlobalHealth needs, gathered up front
// so the decision logic is a pure function.
type globalHealthInput struct {
	Root           string
	GuardPath      string
	GuardInstalled []byte // nil when the guard script is not installed
	GuardEmbedded  []byte
	KernResolvable bool // the guard's own kern lookup would succeed
	Env            func(string) string
	RepoOptOut     bool   // <root>/.kern/off exists
	UnsafeReason   string // non-empty: kern refuses to auto-index root
	HasIndex       bool
	RepoHooksPath  string // repo-local core.hooksPath ("" when unset)
	KernHooksDir   string // kern's global hooks dir
}

func globalHealthFindings(in globalHealthInput) []Finding {
	var out []Finding

	switch {
	case in.GuardInstalled == nil:
		out = append(out, Finding{Check: "global-guard", Level: "warn",
			Detail: in.GuardPath + " not installed — machine-wide hooks are off (project-only setups are fine); run `kern setup --global-only` to enable them"})
	case !bytes.Equal(in.GuardInstalled, in.GuardEmbedded):
		out = append(out, Finding{Check: "global-guard", Level: "warn",
			Detail: in.GuardPath + " is stale (differs from this kern's embedded guard) — run `kern setup --global-only` to refresh it"})
	default:
		out = append(out, Finding{Check: "global-guard", Level: "ok", Detail: in.GuardPath + " matches this kern"})
		if !in.KernResolvable {
			out = append(out, Finding{Check: "global-guard-active", Level: "warn",
				Detail: "the guard cannot find a kern binary (not on PATH, $KERN_BIN, or ~/.local/bin/kern), so it fails open and redirects nothing"})
		}
	}

	if in.Env("KERN_DISABLED") == "1" {
		out = append(out, Finding{Check: "kern-opt-out", Level: "warn", Detail: "KERN_DISABLED=1 is set — the hook guard is off for this environment"})
	}
	if in.Env("KERN_BYPASS") == "1" || in.Env("KERN_ENFORCE") == "0" {
		out = append(out, Finding{Check: "kern-opt-out", Level: "warn", Detail: "KERN_BYPASS=1 / KERN_ENFORCE=0 is set — hook enforcement is bypassed (audited to ~/.kern/audit/bypass.jsonl)"})
	}
	if in.RepoOptOut {
		out = append(out, Finding{Check: "kern-opt-out", Level: "warn", Detail: filepath.Join(in.Root, ".kern", "off") + " exists — the hook guard is off for this repo; delete it to re-enable"})
	}

	if in.UnsafeReason != "" && !in.HasIndex {
		out = append(out, Finding{Check: "auto-index", Level: "warn",
			Detail: "kern will not auto-index " + in.Root + ": " + in.UnsafeReason + " — open a project directory, or run `kern index` deliberately (override: KERN_ALLOW_BROAD_INDEX=1)"})
	}

	if in.RepoHooksPath != "" && in.KernHooksDir != "" && filepath.Clean(in.RepoHooksPath) != filepath.Clean(in.KernHooksDir) {
		out = append(out, Finding{Check: "git-hooks-shadow", Level: "warn",
			Detail: "this repo sets core.hooksPath=" + in.RepoHooksPath + ", which shadows kern's machine-wide git hooks (pre-commit gate, push guard) here"})
	}
	return out
}

// checkGlobalHealth reports the state of the machine-wide install as seen
// from root: guard script freshness, whether the guard can actually reach
// kern, active opt-outs, the safe-root refusal, and repo-local git hook
// shadowing.
func checkGlobalHealth(root string) []Finding {
	in := globalHealthInput{
		Root:          root,
		GuardPath:     setup.GlobalGuardPath(),
		GuardEmbedded: setup.GuardScriptAsset(),
		Env:           os.Getenv,
		UnsafeReason:  index.UnsafeAutoIndexRoot(root),
		HasIndex:      index.HasPersistedIndex(root),
	}
	if b, err := os.ReadFile(in.GuardPath); err == nil {
		in.GuardInstalled = b
	}
	in.KernResolvable = kernResolvableLikeGuard()
	if _, err := os.Stat(filepath.Join(root, ".kern", "off")); err == nil {
		in.RepoOptOut = true
	}
	if home, err := os.UserHomeDir(); err == nil {
		in.KernHooksDir = filepath.Join(home, ".kern", "git-hooks")
	}
	if _, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command("git", "-C", root, "config", "--local", "--get", "core.hooksPath")
		if out, err := cmd.Output(); err == nil {
			in.RepoHooksPath = strings.TrimSpace(string(out))
		}
	}
	return globalHealthFindings(in)
}

// kernResolvableLikeGuard mirrors kern_unavailable() in kern-guard.sh: kern on
// PATH, or an executable at $KERN_BIN / ~/.local/bin/kern.
func kernResolvableLikeGuard() bool {
	if _, err := exec.LookPath("kern"); err == nil {
		return true
	}
	bin := os.Getenv("KERN_BIN")
	if bin == "" {
		if home, err := os.UserHomeDir(); err == nil {
			bin = filepath.Join(home, ".local", "bin", "kern")
		}
	}
	if bin == "" {
		return false
	}
	fi, err := os.Stat(bin)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// checkGlobalRulesFreshness compares every installed global agent rules file
// (the six host GLOBAL instructions slots `kern setup --global-rules`
// manages) against the canonical content THIS kern ships. The wiring check
// only proves a kern-managed block EXISTS; it does not prove the block is
// CURRENT — an old binary's block (e.g. one still claiming the deleted
// removed tools, or a stale historical tool surface)
// loads stale policy into every agent session until the operator resyncs.
// Each installed file that does not carry the canonical rules is reported
// stale with the fix command; files the user never wired are left to the
// wiring check.
func checkGlobalRulesFreshness() []Finding {
	var stale []string
	compared := 0
	for _, p := range agentwire.GlobalRulesPaths() {
		if !globalRulesSlotInstalled(p) {
			continue // not kern-wired — installation presence is the wiring check's job
		}
		compared++
		if globalRulesSlotFresh(p) {
			continue
		}
		stale = append(stale, p)
	}
	if compared == 0 {
		return nil // no installed slots on this machine — nothing to compare
	}
	if len(stale) == 0 {
		return []Finding{{Check: "global-rules-freshness", Level: "ok",
			Detail: fmt.Sprintf("%d installed global agent rules file(s) match this kern's canonical content", compared)}}
	}
	var out []Finding
	for _, p := range stale {
		out = append(out, Finding{Check: "global-rules-freshness", Level: "warn",
			Detail: fmt.Sprintf("%s is STALE (does not match this kern's canonical rules) — run: kern setup --global-rules", p)})
	}
	return out
}

// globalRulesSlotInstalled reports whether a global rules path carries a
// kern-managed section at all (marked block, or the legacy unmarked
// "# kern usage rules" format). A user-only file is not an installation and
// is not this check's concern.
func globalRulesSlotInstalled(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, agentwire.GlobalRulesMarkerOpen) || strings.Contains(s, "# kern usage rules")
}

// globalRulesSlotFresh reports whether an installed global rules file carries
// the current canonical content. Marked slots are compared via
// agentwire.GlobalSlotCarriesCanonicalRules (managed block vs the condensed
// canonical rules, modulo the version stamp). Legacy unmarked slots — written
// by the old `kern setup --global` writer with the FULL canonical asset — are
// compared by extracting the kern section and matching it against the
// embedded full asset, modulo the version stamp and the stamp-induced heading
// split.
func globalRulesSlotFresh(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(b)
	if strings.Contains(content, agentwire.GlobalRulesMarkerOpen) {
		return agentwire.GlobalSlotCarriesCanonicalRules(path)
	}
	return unmarkedSlotCarriesCanonical(content)
}

// unmarkedKernSection returns the legacy "# kern usage rules" section of a
// global rules file (heading through the canonical asset-tail anchor), or ""
// when the file does not carry one. The tail anchors are the same ones
// internal/rulesblock recognizes for the unmarked format, so user content
// after the section is never included.
func unmarkedKernSection(content string) string {
	lines := strings.Split(content, "\n")
	start := -1
	end := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "# kern usage rules") {
			start = i
		}
		trimmed := strings.TrimSuffix(line, "\r")
		if start >= 0 && (trimmed == "<!-- kern:global-omit:end -->" || trimmed == "(`KERN_MCP_FULL=1` only).") {
			end = i
		}
	}
	if start < 0 {
		return ""
	}
	if end < start {
		// No recognizable asset-tail anchor (an unusual legacy layout):
		// the section runs to EOF rather than being mis-detected as absent.
		end = len(lines) - 1
	}
	return strings.Join(lines[start:end+1], "\n")
}

// unmarkedSlotCarriesCanonical compares a legacy unmarked kern section against
// the embedded full canonical asset. The old writer inserted the version stamp
// MID-heading ("# kern usage rules\n<!-- kern-version: ... --> for agents —
// READ FIRST"), so normalization drops every stamp line and rejoins the split
// heading before the byte comparison.
func unmarkedSlotCarriesCanonical(content string) bool {
	sec := unmarkedKernSection(content)
	if sec == "" {
		return false
	}
	want, err := agentwire.RulesFS().ReadFile("assets/AGENTS.md")
	if err != nil {
		return false
	}
	norm := func(s string) string {
		s = agentwire.KernVersionStampRe.ReplaceAllString(s, "")
		s = strings.ReplaceAll(s, "# kern usage rules\n for agents — READ FIRST", "# kern usage rules for agents — READ FIRST")
		return strings.TrimSpace(s)
	}
	return norm(sec) == norm(string(want))
}
