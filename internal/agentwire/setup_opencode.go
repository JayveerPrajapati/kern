package agentwire

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// WireMCPJSON writes the project-level .mcp.json kern MCP server entry.
func WireMCPJSON(root, bin string) Status {
	path := filepath.Join(root, ".mcp.json")
	err := MergeJSON(path, "mcpServers", map[string]any{
		"command": bin,
		"args":    []string{},
		// No KERN_ALLOW_EXEC default: exec is opt-in per host. The shipped
		// .mcp.json must not silently enable arbitrary command execution for
		// every agent that opens the repo; governance.CheckExec already fails
		// closed without it, and the approval workflow still gates.
		"env": map[string]string{},
	})
	if err != nil {
		return Status{Agent: "mcp", Path: path, Note: err.Error()}
	}
	return Status{Agent: "mcp", Installed: true, Path: path, Note: "project .mcp.json written (auto-discovered by Claude Code, Cursor, Windsurf, …)"}
}

// WireOpencode writes the project opencode.json kern MCP entry.
func WireOpencode(root string) Status {
	path := filepath.Join(root, "opencode.json")
	// PortableMCPCommand returns bare "kern-mcp" when it is on PATH (the agent
	// re-resolves it at launch, so the config survives relocation) and falls
	// back to the absolute sibling Bin() path otherwise. It never emits the
	// fragile "bin/kern-mcp" relative path, which breaks agents whenever the
	// repo has no bin/kern-mcp binary.
	cmd := PortableMCPCommand()
	entry := map[string]any{
		"type":    "local",
		"command": []string{cmd},
		"enabled": true,
		// Per-call tool timeout (ms). opencode's MCP client applies a ~2min
		// default ceiling to local-server tool calls, which silently kills
		// long kern_verify/verify-family runs (observed live: a full
		// go test -short ./... command-mode run was client-killed at 120s
		// while the server completed it and kept the anchor). 30 minutes
		// matches the server's own per-call ceiling (mcp toolCallResponseDecoded)
		// and the plugin's MAX_CEILING_MS — the host ceiling must never be the
		// effective cap below the server's.
		"timeout": 1_800_000,
		// No KERN_ALLOW_EXEC default (C1): exec is opt-in per host, matching
		// WireMCPJSON's contract — `kern setup` must not silently enable
		// arbitrary command execution for every agent session in the repo.
		// governance.CheckExec fails closed without it, and hosts that want
		// exec set KERN_ALLOW_EXEC=1 themselves. MergeJSON replaces the kern
		// entry wholesale, so re-running setup also removes a stale
		// KERN_ALLOW_EXEC=1 written by an older kern.
		"environment": map[string]string{},
		// No cwd field: opencode resolves opencode.json from the project root
		// and launches MCP servers with cwd = project root by default. Writing
		// an absolute cwd here would leak a machine-specific path into a
		// committed config file, breaking portability.
	}
	err := MergeJSON(path, "mcp", entry)
	if err != nil {
		return Status{Agent: "opencode", Path: path, Note: err.Error()}
	}
	return Status{Agent: "opencode", Installed: true, Path: path, Note: "opencode.json kern MCP entry present"}
}

// WireGlobal writes the global opencode config kern MCP entry.
func WireGlobal(bin string) Status {
	path := GlobalOpencodePath()
	err := MergeJSON(path, "mcp", map[string]any{
		"type":    "local",
		"command": []string{bin},
		"enabled": true,
		// Per-call tool timeout (ms): same 30-minute ceiling as the project
		// opencode.json entry — see the WireOpencode note for the rationale.
		"timeout": 1_800_000,
		// No KERN_ALLOW_EXEC default (C1): same opt-in contract as the
		// project-level wiring — the global config is machine-wide, so
		// silently enabling exec there would be even broader.
		"environment": map[string]string{},
	})
	if err != nil {
		return Status{Agent: "opencode-global", Path: path, Note: err.Error()}
	}
	return Status{Agent: "opencode-global", Installed: true, Path: path, Note: "global opencode MCP entry present"}
}

// BackupDivergentPlugin preserves a divergent plugin copy before a refresh:
// the bytes are written to <path>.kern-bak-<unix-ts> next to the original so
// a refresh never destroys user edits. It backs the backup-then-refresh
// policy every plugin writer shares: a copy whose hash matches NO shipped
// version used to be left untouched forever, which made kern doctor's
// "run: kern setup --global" remedy non-convergent (the stale copy kept
// silently winning opencode's plugin load). Refreshing under a backup keeps
// the user's bytes recoverable while letting the remedy land.
func BackupDivergentPlugin(path string, cur []byte) (string, error) {
	bak := fmt.Sprintf("%s.kern-bak-%d", path, time.Now().Unix())
	if err := os.WriteFile(bak, cur, 0o644); err != nil {
		return "", err
	}
	return bak, nil
}

// WirePlugin installs the opencode plugin into the project's
// .opencode/plugins directory.
func WirePlugin(root string) Status {
	dir := filepath.Join(root, ".opencode", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Status{Agent: "opencode-plugin", Note: err.Error()}
	}
	path := filepath.Join(dir, "kern.ts")
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		return Status{Agent: "opencode-plugin", Path: path, Note: err.Error()}
	}
	// Compare-and-write so re-running setup never torches user edits.
	if cur, rerr := os.ReadFile(path); rerr == nil {
		if bytes.Equal(cur, src) {
			return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "plugin already current"}
		}
		// F-DR1: a copy matching a previously SHIPPED version is kern's own
		// deployment (from an older kern), not user customization — safe and
		// necessary to update. An unrecognized copy is treated as customized:
		// backed up next to the original, then refreshed, so the deployed
		// plugin always converges on the embedded asset while the user's
		// bytes stay recoverable in the backup.
		if !IsShippedPluginVersion(cur) {
			bak, berr := BackupDivergentPlugin(path, cur)
			if berr != nil {
				return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "customized — left untouched (backup failed: " + berr.Error() + ")"}
			}
			if err := os.WriteFile(path, src, 0o644); err != nil {
				return Status{Agent: "opencode-plugin", Path: path, Note: err.Error()}
			}
			return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "customized — backed up to " + TildePath(bak) + " and refreshed (user edits preserved in the backup)"}
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			return Status{Agent: "opencode-plugin", Path: path, Note: err.Error()}
		}
		return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "plugin updated from an older kern version (was a previously shipped copy)"}
	}
	if err := os.WriteFile(path, src, 0o644); err != nil {
		return Status{Agent: "opencode-plugin", Path: path, Note: err.Error()}
	}
	return Status{Agent: "opencode-plugin", Installed: true, Path: path, Note: "plugin installed"}
}

// WireGlobalPlugin installs the opencode plugin into every global plugin
// location (GlobalPluginPaths: ~/.config/opencode/plugins and ~/.opencode/
// plugins). opencode 1.18.x loads plugins from the config root
// (~/.opencode/plugins), where a stale copy silently wins over the project
// one — installing both keeps every copy in sync with the embedded asset.
// Uses the same compare-and-write semantics as WirePlugin: copies identical
// to the embedded asset are left alone, previously-shipped copies are updated
// (F-DR1), and user-customized copies are backed up next to the original and
// then refreshed, so the copy that silently wins the plugin load always
// converges on the embedded asset while the user's edits stay recoverable.
func WireGlobalPlugin() Status {
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		return Status{Agent: "opencode-plugin-global", Note: err.Error()}
	}
	var notes []string
	installed := false
	for _, path := range GlobalPluginPaths() {
		if cur, rerr := os.ReadFile(path); rerr == nil {
			if bytes.Equal(cur, src) {
				notes = append(notes, TildePath(path)+" current")
				installed = true
				continue
			}
			// F-DR1: a previously shipped version is kern's own deployment
			// (from an older kern) — update it instead of leaving doctor's
			// prescribed remedy unable to converge. An unrecognized copy is
			// customized: back it up next to the original, then refresh, so the
			// copy that silently wins opencode's plugin load always converges on
			// the embedded asset while the user's bytes stay recoverable.
			if IsShippedPluginVersion(cur) {
				if err := os.WriteFile(path, src, 0o644); err != nil {
					notes = append(notes, TildePath(path)+": "+err.Error())
					continue
				}
				notes = append(notes, TildePath(path)+" updated from an older kern version")
				installed = true
				continue
			}
			bak, berr := BackupDivergentPlugin(path, cur)
			if berr != nil {
				notes = append(notes, TildePath(path)+" customized — left untouched (backup failed: "+berr.Error()+")")
				installed = true
				continue
			}
			if err := os.WriteFile(path, src, 0o644); err != nil {
				notes = append(notes, TildePath(path)+": "+err.Error())
				continue
			}
			notes = append(notes, TildePath(path)+" customized — backed up to "+TildePath(bak)+" and refreshed (user edits preserved in the backup)")
			installed = true
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			notes = append(notes, TildePath(path)+": "+err.Error())
			continue
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			notes = append(notes, TildePath(path)+": "+err.Error())
			continue
		}
		notes = append(notes, TildePath(path)+" installed")
		installed = true
	}
	return Status{Agent: "opencode-plugin-global", Installed: installed, Note: strings.Join(notes, "; ")}
}

// TildePath renders an absolute path with the home directory abbreviated as
// "~" for human-readable status notes.
func TildePath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, h) {
		return filepath.Join("~", strings.TrimPrefix(p, h))
	}
	return p
}

// HostRuleFiles are the per-agent rule files that peer agents read directly
// (Claude Code: CLAUDE.md, Gemini/CodeBuddy: GEMINI.md). AGENTS.md is the
// universal source; the same single-source block is instantiated into these
// host files so every agent sees the rules, but only when the file already
// exists — setup never creates new rule files unprompted.
var HostRuleFiles = []string{"CLAUDE.md", "GEMINI.md"}

// HostGlobalManaged reports whether the GLOBAL slot corresponding to a
// project rule file name carries an INTACT kern-managed block — not just
// the markers (deep-verify L5-8: a gutted-but-marked slot used to count as
// managed, the repo AGENTS.md then went thin, and no rules loaded anywhere,
// silently). The content between the markers must match the canonical
// condensed rules modulo the version stamp; a mismatch degrades to "not
// managed" so the repo file carries the full rules (safe: they load,
// merely redundantly) and doctor flags the slot. The mapping follows file
// basenames: CLAUDE.md → ~/.claude/CLAUDE.md, GEMINI.md →
// ~/.gemini/GEMINI.md, AGENTS.md → both AGENTS-named global slots
// (~/.codex/AGENTS.md and the opencode global AGENTS.md — all of those
// hosts merge global + project AGENTS.md, so any intact managed slot
// justifies the thin repo file). Files without a matching global slot
// (kern.md, instruction docs) report false and always carry the full
// rules.
func HostGlobalManaged(name string) bool {
	for _, p := range GlobalRulesPaths() {
		if filepath.Base(p) != name {
			continue
		}
		if GlobalSlotCarriesCanonicalRules(p) {
			return true
		}
	}
	return false
}

// GlobalSlotCarriesCanonicalRules verifies a global slot's managed block is
// intact: markers present AND the content between them matching the
// canonical condensed rules (assets/AGENTS.md minus the global-omit
// sections) modulo the binary-version stamp line. When the canonical block
// cannot be derived (embed failure), the old marker-presence verdict
// stands — the comparison must never make things worse than before.
func GlobalSlotCarriesCanonicalRules(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), GlobalRulesMarkerOpen) {
		return false
	}
	want, err := CondenseGlobalRules()
	if err != nil {
		return true
	}
	got := ExtractManagedBlock(string(b))
	if got == "" {
		return false // markers present but no block between them: gutted
	}
	return NormalizeManagedBlock(got) == NormalizeManagedBlock(string(want))
}

// ExtractManagedBlock returns the content from the open marker through the
// close marker (inclusive), or "" when either marker is missing.
func ExtractManagedBlock(s string) string {
	i := strings.Index(s, GlobalRulesMarkerOpen)
	if i < 0 {
		return ""
	}
	rest := s[i+len(GlobalRulesMarkerOpen):]
	j := strings.Index(rest, GlobalRulesMarkerClose)
	if j < 0 {
		return ""
	}
	return s[i : i+len(GlobalRulesMarkerOpen)+j+len(GlobalRulesMarkerClose)]
}

// StampLineRe matches the managed version-stamp line (see stamp.go)
// including its trailing newline, so stripping it restores the canonical
// block shape regardless of which binary wrote the slot.
var StampLineRe = regexp.MustCompile(`(?m)^<!--\s*kern-version:\s*[^>\s]+\s*-->\n?`)

// NormalizeManagedBlock strips the version-stamp line and any trailing
// newline so blocks written by different binaries compare by content.
func NormalizeManagedBlock(s string) string {
	return strings.TrimRight(StampLineRe.ReplaceAllString(s, ""), "\n")
}

// RulesVariant resolves the variant for ONE project rule file: an explicit
// choice (--agents-md flag or a persisted .kern/config.json preference) wins;
// the default writes thin only when the corresponding host global slot
// carries the kern-managed block — the full rules then load once via the
// global slot, so a thin repo file avoids loading the same rules twice — and
// full otherwise (an absent or unmanaged global slot means the repo file is
// the only delivery, so the full rules must be there).
func RulesVariant(name, mode string, explicitThin bool) string {
	if mode == "full" {
		return "full"
	}
	if explicitThin || HostGlobalManaged(name) {
		return "thin"
	}
	return "full"
}

// WireRuleVariant writes one project rule file in the resolved variant. The
// run's explicitThin choice is passed through to the thin writer: an existing
// FULL rules file is kept (not downgraded) when the thin variant was resolved
// by the global-managed DEFAULT rather than an explicit choice (a
// tracked full AGENTS.md — e.g. the kern repo's own — must stay byte-stable
// across `kern setup` runs); an explicit thin choice still downgrades, and a
// fresh root in a global-managed environment still wires thin.
func WireRuleVariant(root, name, mode, wired string, explicitThin bool) Status {
	if RulesVariant(name, mode, explicitThin) == "thin" {
		return WireThinRulesFile(root, name, wired, explicitThin)
	}
	return WireRulesFile(root, name)
}

// WireAgentRules writes the kern usage rules to the universal repo AGENTS.md
// (and any existing per-host rule files). The variant is resolved PER FILE:
// an explicit choice wins; the default is thin when the corresponding host's
// GLOBAL slot carries the kern-managed block (full rules load once globally —
// a thin repo file avoids loading them twice) and full when it does not
// (rules must still reach agents). wired names the agents this run wired,
// used by the thin variant's wiring-facts line. explicitThin marks a
// deliberate thin choice.
func WireAgentRules(root, mode, wired string, explicitThin bool) Status {
	status := WireRuleVariant(root, "AGENTS.md", mode, wired, explicitThin)
	// Same variant, per host. Errors here are informational: the universal
	// AGENTS.md is the primary delivery mechanism.
	for _, name := range HostRuleFiles {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		WireRuleVariant(root, name, mode, wired, explicitThin)
	}
	return status
}
