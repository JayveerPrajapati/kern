package setup

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPluginShadowExemptionParity pins family parity for the simple-command /
// simple-read exemptions: the shell guard (kern-guard.sh — consumed by 8
// hook-based agents) and the opencode plugin's TS predicates
// (isSimpleCommand/isSimpleRead — consumed by opencode) MUST reach the same
// exempt/governed decision on the probe matrix. The TS predicates are
// extracted verbatim from the shipped plugin asset and executed under node;
// any divergence fails the build.
//
// The extraction mirrors the manual verification harness: slice the predicate
// block out of the plugin source, strip the two TS type annotations, prepend
// the node requires, and run the same probes through both implementations.
func TestPluginShadowExemptionParity(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v — skipping TS predicate parity check", err)
	}
	script := writeGuardScriptFile(t)

	// Probes shared by both implementations. Commands are the bash-branch
	// matrix; files are the read-branch matrix (created below).
	commands := []string{
		// exempt (guard exit 0 == TS simple)
		"git status", "git status --short", "git log", "git log --oneline", "git log --pretty=oneline",
		"pwd", "true", "whoami", "date -u", "echo hello", "ls -la", "which gcc",
		// governed (guard exit 2 == TS non-simple)
		"git log -p", "git log --patch", "git log -u", "git log --raw", "git log --oneline -p",
		"git diff", "git show HEAD:main.go", "git blame file.go",
		"git statusX", "pwd123", "echo $HOME",
		"pwd\nmake", "git status\nrm -rf /tmp/x",
		"git log --oneline | head -5", "echo $(whoami)", "ls; rm -rf /tmp/x", "grep foo file.go > out.txt",
		"sed -n '1,5p' file.go", "cat source.go", "make", "go test ./...",
	}

	dir := t.TempDir()
	readme := filepath.Join(dir, "README.md")
	// smallGo is a <2KB code file: governed on BOTH sides (size is irrelevant
	// for code extensions — only non-code files serve raw).
	smallGo := filepath.Join(dir, "small.go")
	bigGo := filepath.Join(dir, "big.go")
	bigHh := filepath.Join(dir, "big.hh")
	for _, f := range []struct {
		path string
		data string
	}{
		{readme, "# readme\n"},
		{smallGo, "package main\n"},
		{bigGo, strings.Repeat("package main\n", 400)},
		{bigHh, strings.Repeat("// header\n", 600)},
	} {
		if err := os.WriteFile(f.path, []byte(f.data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{readme, smallGo, bigGo, bigHh, filepath.Join(dir, "nope.go")}

	// --- TS side: extract the predicates verbatim and run them under node ---
	pluginSrc, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(pluginSrc)
	start := strings.Index(src, "const CODE_EXTENSIONS = new Set([")
	if start < 0 {
		t.Fatal("predicate block marker 'const CODE_EXTENSIONS' not found in plugin")
	}
	end := strings.Index(src, "// Raw read: verbatim file contents")
	if end < 0 || end < start {
		t.Fatal("predicate block end marker '// Raw read:' not found in plugin")
	}
	block := src[start:end]
	// Strip the two TS type annotations so plain node can eval the block.
	block = strings.Replace(block, "async function isSimpleRead(filePath: string): Promise<boolean>", "async function isSimpleRead(filePath)", 1)
	block = strings.Replace(block, "function isSimpleCommand(cmd: string): boolean {", "function isSimpleCommand(cmd) {", 1)

	probes := struct {
		Commands []string `json:"commands"`
		Files    []string `json:"files"`
	}{commands, files}
	probeJSON, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	probeFile := filepath.Join(t.TempDir(), "probes.json")
	if err := os.WriteFile(probeFile, probeJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	nodeScript := filepath.Join(t.TempDir(), "preds.js")
	nodeSrc := `const { stat } = require("node:fs/promises");
const { resolve } = require("node:path");
const fs = require("node:fs");
` + block + `
const probes = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
(async () => {
const out = { simpleCommand: {}, simpleRead: {} };
for (const c of probes.commands) out.simpleCommand[c] = isSimpleCommand(c);
for (const f of probes.files) out.simpleRead[f] = await isSimpleRead(f);
process.stdout.write(JSON.stringify(out));
})();
`
	if err := os.WriteFile(nodeScript, []byte(nodeSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", nodeScript, probeFile).Output()
	if err != nil {
		t.Fatalf("node predicate harness failed: %v", err)
	}
	var tsRes struct {
		SimpleCommand map[string]bool `json:"simpleCommand"`
		SimpleRead    map[string]bool `json:"simpleRead"`
	}
	if err := json.Unmarshal(out, &tsRes); err != nil {
		t.Fatalf("unmarshal node output %q: %v", out, err)
	}

	// --- Guard side: same probes through the shell hook ---
	guardExempt := func(stdin string) bool {
		code, _ := runGuard(t, script, stdin, nil)
		return code == 0
	}
	cmdPayload := func(cmd string) string {
		b, err := json.Marshal(map[string]any{"tool_name": "bash", "tool_input": map[string]string{"command": cmd}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	readPayload := func(path string) string {
		b, err := json.Marshal(map[string]any{"tool_name": "Read", "tool_input": map[string]string{"file_path": path}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	for _, c := range commands {
		tsSimple := tsRes.SimpleCommand[c]
		guardEx := guardExempt(cmdPayload(c))
		if tsSimple != guardEx {
			t.Errorf("DIVERGENCE on command %q: TS isSimpleCommand=%v but guard exempt=%v", c, tsSimple, guardEx)
		}
	}
	for _, f := range files {
		tsSimple := tsRes.SimpleRead[f]
		guardEx := guardExempt(readPayload(f))
		if tsSimple != guardEx {
			t.Errorf("DIVERGENCE on read %q: TS isSimpleRead=%v but guard exempt=%v", filepath.Base(f), tsSimple, guardEx)
		}
	}

	// Sanity: the node harness actually ran the predicates (empty results
	// would make the parity trivially pass).
	if len(tsRes.SimpleCommand) != len(commands) || len(tsRes.SimpleRead) != len(files) {
		t.Fatalf("node harness returned %d command + %d file results, want %d + %d — extraction drift?",
			len(tsRes.SimpleCommand), len(tsRes.SimpleRead), len(commands), len(files))
	}
	// Sanity: the exemption surface is non-trivial (both sides exempt at
	// least one command and govern at least one).
	exempt := 0
	governed := 0
	for _, c := range commands {
		if tsRes.SimpleCommand[c] {
			exempt++
		} else {
			governed++
		}
	}
	if exempt == 0 || governed == 0 {
		t.Fatalf("probe matrix degenerate: %d exempt / %d governed", exempt, governed)
	}
}

// TestPluginShadowPredicateMarkersPresent pins the predicate markers the
// node-based parity harness relies on: a rename of isSimpleCommand /
// isSimpleRead or the block boundaries silently disables the parity check, so
// pin them here too.
var (
	tsPredicateMarkerRe = regexp.MustCompile(`function isSimpleCommand\(cmd`)
	tsReadMarkerRe      = regexp.MustCompile(`async function isSimpleRead\(filePath`)
)

func TestPluginShadowPredicateMarkersPresent(t *testing.T) {
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, re := range []*regexp.Regexp{tsPredicateMarkerRe, tsReadMarkerRe} {
		if !re.MatchString(s) {
			t.Errorf("plugin predicate marker %q missing — the parity harness would silently skip", re)
		}
	}
	if !strings.Contains(s, "const CODE_EXTENSIONS = new Set([") || !strings.Contains(s, "// Raw read: verbatim file contents") {
		t.Error("predicate block boundary markers missing — the parity harness would silently skip")
	}
}

// unescapeQuoted unescapes the two escape forms used by the shipped assets'
// double-quoted strings: the guard's shell reasons (\" -> ") and the plugin's
// JS throw messages (\" -> "). Both sides emit the same literal text.
func unescapeQuoted(s string) string {
	s = strings.ReplaceAll(s, `\\`, `\`)
	return strings.ReplaceAll(s, `\"`, `"`)
}

// Contract Y message parity: the plugin's shadow BLOCK messages (read/grep/
// bash) must be byte-identical to the guard's `reason=` strings in
// kern-guard.sh — the opencode plugin throws exactly what the shell guard
// prints on stderr, so a redirect the agent sees is the same on every agent.
// Both assets are read from disk and unescaped the way their runtime would;
// any drift (edited redirect text on one side only) fails CI. The glob reason
// is deliberately NOT asserted: the glob shadow is unchanged by Contract Y
// (it still falls back raw and never throws), so it has no plugin message to
// pin.
func TestPluginShadowGuardMessageParity(t *testing.T) {
	pluginSrc, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(pluginSrc)

	// Guard side: every reason="..." assignment, unescaped like the shell
	// would emit it.
	reasonRe := regexp.MustCompile(`reason="((?:[^"\\]|\\.)*)"`)
	guardReasons := map[string]string{}
	for _, m := range reasonRe.FindAllStringSubmatch(kernGuardScript, -1) {
		reason := unescapeQuoted(m[1])
		switch {
		case strings.Contains(reason, "kern_compact_file"):
			guardReasons["read"] = reason
		case strings.Contains(reason, "kern_ast_search"):
			guardReasons["grep"] = reason
		case strings.Contains(reason, "kern_validate"):
			guardReasons["bash"] = reason
		case strings.Contains(reason, "kern_project_map"):
			guardReasons["glob"] = reason
		}
	}
	for _, tool := range []string{"read", "grep", "bash", "glob"} {
		if guardReasons[tool] == "" {
			t.Fatalf("guard reason for %q not found in kern-guard.sh", tool)
		}
	}

	// Plugin side: every throw new Error("...") inside the shadow built-ins
	// block (the read/grep/glob/bash handlers), unescaped like JS would.
	shadowStart := strings.Index(src, "// --- Shadow built-ins:")
	if shadowStart < 0 {
		t.Fatal("shadow built-ins marker '// --- Shadow built-ins:' not found in plugin")
	}
	shadowEnd := strings.Index(src, "config: (cfg)")
	if shadowEnd < 0 || shadowEnd < shadowStart {
		t.Fatal("shadow block end marker 'config: (cfg)' not found in plugin")
	}
	shadow := src[shadowStart:shadowEnd]
	pluginThrows := map[string][]string{}
	throwRe := regexp.MustCompile(`new Error\("((?:[^"\\]|\\.)*)"\)`)
	for _, m := range throwRe.FindAllStringSubmatch(shadow, -1) {
		msg := unescapeQuoted(m[1])
		switch {
		case strings.Contains(msg, "kern_compact_file"):
			pluginThrows["read"] = append(pluginThrows["read"], msg)
		case strings.Contains(msg, "kern_ast_search"):
			pluginThrows["grep"] = append(pluginThrows["grep"], msg)
		case strings.Contains(msg, "kern_validate"):
			pluginThrows["bash"] = append(pluginThrows["bash"], msg)
		case strings.Contains(msg, "kern_project_map"):
			pluginThrows["glob"] = append(pluginThrows["glob"], msg)
		}
	}

	// Contract Y pins read/grep/bash parity byte-for-byte.
	for _, tool := range []string{"read", "grep", "bash"} {
		msgs := pluginThrows[tool]
		if len(msgs) == 0 {
			t.Errorf("plugin has no throw message for %q — block removed?", tool)
			continue
		}
		for _, m := range msgs {
			if m != guardReasons[tool] {
				t.Errorf("plugin %s block message diverges from guard reason:\n plugin: %s\n guard:  %s", tool, m, guardReasons[tool])
			}
		}
	}
	// The glob shadow is out of Contract Y scope: it keeps its raw fallback
	// and must NOT grow a throw (a glob block message would contradict the
	// metacharacter raw-fallback path that still serves raw).
	if len(pluginThrows["glob"]) != 0 {
		t.Errorf("glob shadow unexpectedly throws %d message(s) — Contract Y leaves glob on its raw-fallback path", len(pluginThrows["glob"]))
	}
}

// Contract Y content-intent classifier: the bash shadow's first-token
// classifier is extracted verbatim from the shipped plugin and executed under
// node. This pins the exact token list (drift fails CI) and the honest
// first-token-only signal: a command that STARTS with a content-intent token
// (grep -rn ...) matches and blocks with the guard's message, while a
// `cd X && grep ...` compound has first token "cd" — not matched at the
// classifier — and keeps flowing to the governed kern build path
// (execution-class routing preserved, exactly the `cd X && npm test` case).
func TestPluginShadowContentIntentClassifier(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v — skipping content-intent classifier check", err)
	}
	pluginSrc, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(pluginSrc)
	m := regexp.MustCompile(`const contentIntents = new Set\((\[[^\]]*\])\)`)
	hit := m.FindStringSubmatch(src)
	if hit == nil {
		t.Fatal("content-intent classifier 'const contentIntents = new Set([...])' not found in plugin")
	}

	nodeScript := filepath.Join(t.TempDir(), "content-intents.js")
	nodeSrc := `const contentIntents = new Set(` + hit[1] + `);
const firstTok = (cmd) => cmd.split(/\s+/)[0];
const out = {
  tokens: [...contentIntents].sort(),
  hasGrep: contentIntents.has("grep"),
  hasFind: contentIntents.has("find"),
  hasCat: contentIntents.has("cat"),
  // Direct content-intent command: first token IS the content-intent tool.
  bareGrepFirstTok: firstTok("grep -rn foo ."),
  bareGrepMatches: contentIntents.has(firstTok("grep -rn foo .")),
  // Compound forms: first token is "cd" — the honest signal that keeps
  // execution-class compounds (cd X && npm test) flowing to kern build.
  cdCompoundFirstTok: firstTok("cd X && grep foo"),
  cdCompoundMatches: contentIntents.has(firstTok("cd X && grep foo")),
  cdNpmCompoundMatches: contentIntents.has(firstTok("cd X && npm test")),
};
process.stdout.write(JSON.stringify(out));
`
	if err := os.WriteFile(nodeScript, []byte(nodeSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", nodeScript).Output()
	if err != nil {
		t.Fatalf("node classifier harness failed: %v", err)
	}
	var res struct {
		Tokens             []string `json:"tokens"`
		HasGrep            bool     `json:"hasGrep"`
		HasFind            bool     `json:"hasFind"`
		HasCat             bool     `json:"hasCat"`
		BareGrepFirstTok   string   `json:"bareGrepFirstTok"`
		BareGrepMatches    bool     `json:"bareGrepMatches"`
		CdCompoundFirstTok string   `json:"cdCompoundFirstTok"`
		CdCompoundMatches  bool     `json:"cdCompoundMatches"`
		CdNpmCompoundMatch bool     `json:"cdNpmCompoundMatches"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal node classifier output %q: %v", out, err)
	}

	wantTokens := []string{"ag", "awk", "cat", "egrep", "find", "grep", "head", "less", "rg", "sed", "tail"}
	if strings.Join(res.Tokens, ",") != strings.Join(wantTokens, ",") {
		t.Errorf("content-intent tokens = %v, want %v", res.Tokens, wantTokens)
	}
	for name, got := range map[string]bool{"grep": res.HasGrep, "find": res.HasFind, "cat": res.HasCat} {
		if !got {
			t.Errorf("contentIntents missing %q — classifier no longer blocks direct content-intent commands", name)
		}
	}
	if res.BareGrepFirstTok != "grep" || !res.BareGrepMatches {
		t.Errorf("first-token classification of %q = %q (matches=%v), want first token 'grep' matching the set", "grep -rn foo .", res.BareGrepFirstTok, res.BareGrepMatches)
	}
	// The verbatim classifier is first-token-only: `cd X && grep foo` does NOT
	// match (first token "cd") and continues to the governed kern build path.
	if res.CdCompoundFirstTok != "cd" || res.CdCompoundMatches {
		t.Errorf("first-token classification of %q = %q (matches=%v), want first token 'cd' NOT matching (governed build path)", "cd X && grep foo", res.CdCompoundFirstTok, res.CdCompoundMatches)
	}
	if res.CdNpmCompoundMatch {
		t.Error("`cd X && npm test` first token matched the content-intent set — execution-class compounds must keep flowing to kern build")
	}
}
