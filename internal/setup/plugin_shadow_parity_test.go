package setup

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
		// read-only kern diagnostics — exact invocation only (guard exit 0 ==
		// TS simple)
		"kern version", "kern --version", "kern doctor", "kern health",
		// governed (guard exit 2 == TS non-simple)
		"git log -p", "git log --patch", "git log -u", "git log --raw", "git log --oneline -p",
		"git diff", "git show HEAD:main.go", "git blame file.go",
		"git statusX", "pwd123", "echo $HOME",
		// any kern invocation beyond the four exempt forms stays governed
		"kern update", "kern version --json",
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
		case strings.Contains(reason, "kern_verify"):
			// bash reason — must be classified before the grep anchor: its
			// "kern alternative" tail also names kern_search.
			guardReasons["bash"] = reason
		case strings.Contains(reason, "kern_search"):
			guardReasons["grep"] = reason
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
		case strings.Contains(msg, "kern_verify"):
			pluginThrows["bash"] = append(pluginThrows["bash"], msg)
		case strings.Contains(msg, "kern_search"):
			pluginThrows["grep"] = append(pluginThrows["grep"], msg)
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

// TestPluginShadowBannerEmptyReason pins the mode-1 fix: the governed-bash
// failure banner must NEVER render an empty why. Live failure: a nonzero exit
// with zero captured output showed only "Failed with exit code 2" — nothing
// to explain what happened. The banner function is extracted verbatim from
// the shipped plugin (marker-delimited) and executed under node, mirroring
// the predicate-parity harness pattern.
func TestPluginShadowBannerEmptyReason(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v — skipping banner check", err)
	}
	pluginSrc, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(pluginSrc)
	start := strings.Index(src, "// --- governed-bash banner start")
	if start < 0 {
		t.Fatal("banner block start marker '// --- governed-bash banner start' not found in plugin")
	}
	end := strings.Index(src, "// --- governed-bash banner end ---")
	if end < 0 || end < start {
		t.Fatal("banner block end marker '// --- governed-bash banner end ---' not found in plugin")
	}
	block := src[start:end]
	block = strings.Replace(block, "function governedBashBanner(exitCode: number, text: string): string {", "function governedBashBanner(exitCode, text) {", 1)

	nodeScript := filepath.Join(t.TempDir(), "banner.js")
	nodeSrc := block + `
const out = {
  empty: governedBashBanner(2, ""),
  emptyWs: governedBashBanner(1, "  \n\t "),
  denial: governedBashBanner(3, "kern: build: execution denied by governance: approval appr-x pending — resolve with: kern approve appr-x"),
  withText: governedBashBanner(1, "make: *** no rule to make target"),
};
process.stdout.write(JSON.stringify(out));
`
	if err := os.WriteFile(nodeScript, []byte(nodeSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", nodeScript).Output()
	if err != nil {
		t.Fatalf("node banner harness failed: %v", err)
	}
	var res struct {
		Empty    string `json:"empty"`
		EmptyWs  string `json:"emptyWs"`
		Denial   string `json:"denial"`
		WithText string `json:"withText"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal node banner output %q: %v", out, err)
	}

	// Mode 1: nonzero exit + empty captured text MUST render a diagnostic.
	for name, banner := range map[string]string{"empty": res.Empty, "emptyWs": res.EmptyWs} {
		if !strings.HasPrefix(banner, "[kern] command failed (exit code ") {
			t.Errorf("%s banner should be the plain-failure prefix, got: %q", name, banner)
		}
		if !strings.Contains(banner, "command produced no output") {
			t.Errorf("%s banner must append the empty-output diagnostic, got: %q", name, banner)
		}
	}
	// Denial banner keeps its denied prefix and the new sentence is not
	// required there (the pre-execution sentence lives on the Go side).
	if !strings.HasPrefix(res.Denial, "[kern] governed command denied/blocked (exit code 3):") {
		t.Errorf("denial banner prefix wrong, got: %q", res.Denial)
	}
	// Non-empty output is unchanged: no diagnostic appended.
	want := "[kern] command failed (exit code 1):\nmake: *** no rule to make target"
	if res.WithText != want {
		t.Errorf("banner with text = %q, want %q", res.WithText, want)
	}
}

// TestPluginShadowCommandForwardedVerbatim pins F2: the governed bash path
// must hand the ORIGINAL command string to `kern build` as ONE verbatim
// argument. The assembly is extracted verbatim from the shipped plugin (the
// sq quote helper + runWithExit's cmdStr line) and executed under node; the
// argv `kern build` would receive is recovered through an argv-printing
// stand-in and compared byte-for-byte. Live regressions: a compound with
// single quotes + parens + redirects hit a sh "syntax error near unexpected
// token '('" (quotes mangled, metacharacters left unquoted), and
// `grep -c '^kern_'` reached grep as `\^kern_\` ("trailing backslash",
// exit 2) — both caused by an extra manual escaping layer (a broken quote
// idiom in the sq helper) applied before the host `$` transport, which
// already quotes each interpolated value as one argument.
func TestPluginShadowCommandForwardedVerbatim(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v — skipping governed-bash quoting check", err)
	}
	pluginSrc, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(pluginSrc)

	// Extract the sq quote helper verbatim (identical in runRaw/runPayload/
	// runWithExit) and strip the TS parameter annotation for plain node.
	sqMarker := "const sq = (s: string) => `"
	sqStart := strings.Index(src, sqMarker)
	if sqStart < 0 {
		t.Fatal("sq quote helper not found in plugin — quoting parity harness would silently skip")
	}
	sqEnd := strings.Index(src[sqStart:], "\n")
	if sqEnd < 0 {
		t.Fatal("sq helper line unterminated")
	}
	sqLine := strings.TrimSpace(src[sqStart : sqStart+sqEnd])
	sqLine = strings.Replace(sqLine, "const sq = (s: string) => ", "const sq = (s) => ", 1)

	// Extract runWithExit's cmdStr assembly (the governed bash path): the
	// line whose `${args.map(sq).join(" ")}` embeds the raw command into the
	// sh -c script. Marker is unique to runWithExit (runPayload's line has
	// `${preserveExit ...}` there, runRaw has `( ${command} )`).
	asmMarker := `join(" ")} > ${sq(outFile)} 2>&1; printf '%s' "$?"`
	asmStart := strings.Index(src, asmMarker)
	if asmStart < 0 {
		t.Fatal("runWithExit cmdStr assembly not found in plugin — quoting parity harness would silently skip")
	}
	asmLineStart := strings.LastIndex(src[:asmStart], "\n") + 1
	asmEnd := strings.Index(src[asmStart:], "\n")
	if asmEnd < 0 {
		t.Fatal("cmdStr assembly line unterminated")
	}
	asmLine := strings.TrimSpace(src[asmLineStart : asmStart+asmEnd])
	if !strings.HasPrefix(asmLine, "const cmdStr = `") {
		t.Fatalf("extracted assembly line = %q, want the runWithExit const cmdStr line", asmLine)
	}

	// The exact commands observed failing live (literal \n inside the single
	// quotes, as the agent typed them).
	cases := []string{
		`printf 'package main\n' > main.go && echo "== health (unindexed) ==" && kern health 2>&1 | head -12; echo "EXIT=$?"`,
		`kern mcp tools 2>&1 | grep -c '^kern_'`,
	}
	casesJSON, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}

	// Stand-in for the kern binary: prints its argv so the test can assert on
	// the exact arguments kern build would have received.
	printer := filepath.Join(t.TempDir(), "argv.sh")
	if err := os.WriteFile(printer, []byte("#!/bin/sh\ni=0\nfor a in \"$@\"; do\n  i=$((i+1))\n  printf 'ARG%d=%s\\n' \"$i\" \"$a\"\ndone\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The harness mirrors the plugin's call shape: bin + one command arg.
	// args = [printer, cmd] plays the role of the plugin's ["build", cmd];
	// the printer script sees $@ = [cmd] — exactly the argument `kern build`
	// would receive. The command must arrive as ONE byte-verbatim argument.
	nodeScript := filepath.Join(t.TempDir(), "forward.js")
	nodeSrc := `const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
` + sqLine + "\n" + `
const printer = ` + strconv.Quote(printer) + `;
const bin = "/bin/sh";
const outFile = path.join(os.tmpdir(), "kern-fwd-" + process.pid + ".out");
const cases = ` + string(casesJSON) + `;
function forward(cmd) {
  const args = [printer, cmd];
  ` + asmLine + `
  try { execFileSync("/bin/sh", ["-c", cmdStr], { stdio: ["ignore", "pipe", "pipe"] }); } catch (e) {}
  const text = fs.readFileSync(outFile, "utf8");
  return text.split("\n").filter((l) => l.startsWith("ARG")).map((l) => l.slice(l.indexOf("=") + 1));
}
const out = {};
for (const c of cases) out[c] = forward(c);
process.stdout.write(JSON.stringify(out));
`
	if err := os.WriteFile(nodeScript, []byte(nodeSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", nodeScript).Output()
	if err != nil {
		t.Fatalf("node forwarding harness failed: %v\n%s", err, out)
	}
	var res map[string][]string
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal node output %q: %v", out, err)
	}

	for _, cmd := range cases {
		argv, ok := res[cmd]
		if !ok {
			t.Fatalf("node harness returned no argv for %q — extraction drift?", cmd)
		}
		want := []string{cmd}
		if len(argv) != len(want) {
			t.Errorf("command %q forwarded as %d argument(s) %v, want exactly 1 (%v) — the command must reach kern build as ONE verbatim argument", cmd, len(argv), argv, want)
			continue
		}
		for i := range want {
			if argv[i] != want[i] {
				t.Errorf("command %q forwarded arg %d = %q, want %q (byte-verbatim)", cmd, i, argv[i], want[i])
			}
		}
	}
}
