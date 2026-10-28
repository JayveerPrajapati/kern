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
		// any kern CLI invocation runs raw (guard exit 0 == TS simple)
		"kern version", "kern --version", "kern doctor", "kern health",
		"kern update", "kern version --json", "kern mcp --help",
		"kern guard allow abc123", "kern exec --readonly --lang sh x",
		// governed (guard exit 2 == TS non-simple)
		"git log -p", "git log --patch", "git log -u", "git log --raw", "git log --oneline -p",
		"git diff", "git show HEAD:main.go", "git blame file.go",
		"git statusX", "pwd123", "echo $HOME",
		// kern in pipes / wrapper-hidden stays governed
		"kern ast Foo | head -5", "sudo kern update",
		"pwd\nmake", "git status\nrm -rf /tmp/x",
		"git log --oneline | head -5", "echo $(whoami)", "ls; rm -rf /tmp/x", "grep foo file.go > out.txt",
		"sed -n '1,5p' file.go", "cat source.go", "make", "go test ./...",
		// everyday passthrough (item 4): stat-only diff/show, branch/remote
		// listing, rev-parse, wc, add/commit without the unsafe flags
		"git diff --stat", "git diff --name-only HEAD~1", "git show --stat HEAD",
		"git branch --show-current", "git branch -a", "git rev-parse HEAD", "git remote -v",
		"git remote get-url origin", "wc -l README.md", "git add main.go", "git add -A",
		"git commit -m fix", "git commit -am wip", "git commit --amend",
		"git diff --stat -p", "git diff -U5 --stat", "git show --stat HEAD:main.go",
		"git branch newname", "git branch -D old", "git remote add x url",
		"git add -f .env", "git add -Af .env", "git commit --no-verify -m x",
		"git commit -n -m x", "git commit -nm x", "git push",
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
	// Strip the TS type annotations so plain node can eval the block.
	block = strings.Replace(block, "async function isSimpleRead(filePath: string): Promise<boolean>", "async function isSimpleRead(filePath)", 1)
	block = strings.Replace(block, "function isSimpleCommand(cmd: string): boolean {", "function isSimpleCommand(cmd) {", 1)
	block = strings.Replace(block, "function isStatOnlyGit(sub: string, args: string[]): boolean {", "function isStatOnlyGit(sub, args) {", 1)
	// The classifier helpers carry their own annotations; strip those too so the
	// extraction stays node-evaluable (quote-aware tokenizer, prefix state
	// machine, newline segmenting — the classifier hardening).
	block = strings.Replace(block, "function shellTokenize(seg: string): string[] {", "function shellTokenize(seg) {", 1)
	block = strings.Replace(block, "function stripPrefixTokens(tokens: string[]): string[] {", "function stripPrefixTokens(tokens) {", 1)
	block = strings.Replace(block, "function splitCommandSegments(cmd: string): string[] {", "function splitCommandSegments(cmd) {", 1)
	block = strings.Replace(block, "function gateSegmentHit(rawSeg: string, depth: number): boolean {", "function gateSegmentHit(rawSeg, depth) {", 1)
	block = strings.Replace(block, "function gateHit(cmd: string, depth = 0): boolean {", "function gateHit(cmd, depth = 0) {", 1)
	block = strings.Replace(block, "function segName(rawSeg: string): string {", "function segName(rawSeg) {", 1)
	block = strings.Replace(block, "function isReadOnlyCompanion(seg: string): boolean {", "function isReadOnlyCompanion(seg) {", 1)
	block = strings.Replace(block, "function readPipeline(cmd: string): { tier: number; head?: string; pattern?: string; path?: string; file?: string; tails?: string[] } | null {", "function readPipeline(cmd) {", 1)
	block = strings.Replace(block, "function grepPatternAndTarget(args: string[]): { pattern: string; targets: string[] } {", "function grepPatternAndTarget(args) {", 1)
	block = strings.Replace(block, "function grepFlagsOK(args: string[]): boolean {", "function grepFlagsOK(args) {", 1)
	block = strings.Replace(block, "function cleanScope(target: string): string | null {", "function cleanScope(target) {", 1)
	block = strings.Replace(block, "function scopedGrep(args: string[]): { pattern: string; path: string } | null {", "function scopedGrep(args) {", 1)
	block = strings.Replace(block, "const toks: string[] = []", "const toks = []", 1)
	block = strings.Replace(block, "const segs: string[] = []", "const segs = []", 1)
	block = strings.Replace(block, "const tails: string[] = []", "const tails = []", 1)
	block = strings.Replace(block, "let quote: string | null = null", "let quote = null", -1)

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
	// KERN_GUARD_MODE=deny: this test pins the EXEMPTION classification
	// (what runs raw vs what is governed), which must match the TS
	// predicates. The guard's default auto-redirects
	// governed commands (exit 0 with kern output) — that third state would
	// read as "exempt" here and falsely diverge from the TS classifier.
	// The redirect path itself is pinned separately in
	// TestKernGuardAutoRedirect.
	guardExempt := func(stdin string) bool {
		code, _ := runGuard(t, script, stdin, []string{"KERN_GUARD_MODE=deny"})
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
		// One-directional since the guard-steer P3 shell-out: the plugin's
		// isSimpleCommand is a FAST PATH (run raw without consulting the
		// classifier), and every non-simple command falls through to
		// `kern guard classify` — the same Go brain the script consults.
		// The load-bearing invariant is the SAFETY direction: the plugin
		// must never run a command raw that the guard would govern or
		// deny. The reverse (guard exempts, TS fast-path misses it — e.g.
		// path-aware tmp/in-tree write allowances the classifier grants)
		// is not a divergence: the plugin reaches the same allow verdict
		// through classifyCmd, one round-trip later.
		if tsSimple && !guardEx {
			t.Errorf("DIVERGENCE on command %q: TS fast-path runs raw but guard exempts=%v — the plugin would bypass governance the guard enforces", c, guardEx)
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
	s = strings.ReplaceAll(s, `\"`, `"`)
	// The GATE reason embeds backticked command instructions: sh escapes a
	// literal backtick inside a double-quoted reason= as \` and the plugin's
	// JS template literal does the same — both must unescape to `.
	return strings.ReplaceAll(s, "\\`", "`")
}

// Message parity: the plugin's shadow BLOCK messages (read/grep/
// bash) must be byte-identical to the guard's `reason=` strings in
// kern-guard.sh — the opencode plugin throws exactly what the shell guard
// prints on stderr, so a redirect the agent sees is the same on every agent.
// Both assets are read from disk and unescaped the way their runtime would;
// any drift (edited redirect text on one side only) fails CI. The glob reason
// is deliberately NOT asserted: the glob shadow is unchanged
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
		case strings.Contains(reason, "GATE command"):
			// bash GATE reason — the risky-axis deny (reads are served, not
			// denied; only gates throw/deny). Classified before the kern_verify
			// anchor: the guard's governed reason (hook hosts only) also carries
			// the kern_verify tail.
			guardReasons["bash"] = reason
		case strings.Contains(reason, "kern_verify"):
			// governed reason — guard-only on hook hosts (the plugin governs
			// those commands via kern build instead of denying)
			guardReasons["bash_governed"] = reason
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
	// block (the read/grep/glob/bash handlers), unescaped like JS would. The
	// bash throw is a TEMPLATE literal (F1: it embeds the denied command at
	// runtime via ${clippedCmd}), so it is extracted separately below.
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
	classify := func(msg string) {
		switch {
		case strings.Contains(msg, "kern_compact_file"):
			pluginThrows["read"] = append(pluginThrows["read"], msg)
		case strings.Contains(msg, "GATE command"):
			// bash GATE throw — the only bash deny left (reads are served,
			// governed commands run through kern build without a throw).
			pluginThrows["bash"] = append(pluginThrows["bash"], msg)
		case strings.Contains(msg, "kern_search"):
			pluginThrows["grep"] = append(pluginThrows["grep"], msg)
		case strings.Contains(msg, "kern_project_map"):
			pluginThrows["glob"] = append(pluginThrows["glob"], msg)
		}
	}
	for _, m := range throwRe.FindAllStringSubmatch(shadow, -1) {
		classify(unescapeQuoted(m[1]))
	}
	// The read/grep deny messages are TEMPLATE literals now
	// — they splice the subject hash at runtime so the operator can mint an
	// allow-once for the exact denied subject (kern guard allow <hash>).
	// Extract EVERY template throw, not just the first.
	// The template-throw regex must allow ESCAPED backticks inside the
	// literal — the GATE reason embeds backticked instructions as \` and a
	// naive [^`]* stops at the first one, dropping the whole message.
	throwTplRe := regexp.MustCompile("new Error\\(`((?:[^`]|\\\\`)*)`\\)")
	for _, m := range throwTplRe.FindAllStringSubmatch(shadow, -1) {
		classify(unescapeQuoted(m[1]))
	}
	// Runtime splices are normalized to sentinels so the byte-identity check
	// pins the TEMPLATE, not the splice: the (former) bash command splice
	// (guard '$blocked_cmd', plugin '${clippedCmd}'), the subject
	// hash (guard '$subj_hash', plugin '${subjHash}'), and the
	// per-command replacement (guard '$repl', plugin '${repl}').
	cmdSpliceRe := regexp.MustCompile(`'\$(?:blocked_cmd|\{clippedCmd\})'`)
	hashSpliceRe := regexp.MustCompile(`\$\{subjHash\}|\$subj_hash`)
	rootSpliceRe := regexp.MustCompile(`\$\{root\}|\$\{CLAUDE_PROJECT_DIR:-\$PWD\}`)
	replSpliceRe := regexp.MustCompile(`\$\{repl\}|\$repl`)
	normalize := func(s string) string {
		s = cmdSpliceRe.ReplaceAllString(s, "'<cmd>'")
		s = hashSpliceRe.ReplaceAllString(s, "<hash>")
		s = rootSpliceRe.ReplaceAllString(s, "<root>")
		return replSpliceRe.ReplaceAllString(s, "<repl>")
	}

	// Parity pins read/grep/bash parity byte-for-byte. The uniform
	// guard directive (superseding the advisory
	// posture) restored the bash deny: the plugin's content-intent branch
	// throws the guard's own reason text, so byte-parity holds again. One
	// opencode nuance: only content-intent commands hit the deny there —
	// non-content commands still run through the governed kern build path
	// (hook hosts have no governed runner, which is why the guard denies
	// every non-exempt command on those hosts).
	for _, tool := range []string{"read", "grep", "bash"} {
		msgs := pluginThrows[tool]
		if len(msgs) == 0 {
			t.Errorf("plugin has no throw message for %q — block removed?", tool)
			continue
		}
		for _, m := range msgs {
			if normalize(m) != normalize(guardReasons[tool]) {
				t.Errorf("plugin %s block message diverges from guard reason:\n plugin: %s\n guard:  %s", tool, m, guardReasons[tool])
			}
		}
	}
	// bash parity is pinned by the loop above; the deny's allow-once
	// consumption and the advisory-free governed path are pinned by
	// TestPluginBashDenyContract.
	// The glob shadow is out of scope: it keeps its raw fallback
	// and must NOT grow a throw (a glob block message would contradict the
	// metacharacter raw-fallback path that still serves raw).
	if len(pluginThrows["glob"]) != 0 {
		t.Errorf("glob shadow unexpectedly throws %d message(s) — the parity contract leaves glob on its raw-fallback path", len(pluginThrows["glob"]))
	}
}

// TestPluginBashServeContract pins the guard-steer pivot (operator
// directive, superseding the uniform-fail-closed deny): the bash shadow's
// READ path SERVES (3-tier: Tier 1 symbol-shaped reads from the kern index,
// Tier 2 read-head + simple-tail computed answers, Tier 3 → kern exec
// --readonly) — reads are NEVER denied; the GATE path (write/destructive/
// network/installer) throws the guard's own GATE reason (byte-parity pinned
// by TestPluginShadowGuardMessageParity) after consuming an operator
// allow-once, and the governed kern build path never prefixes any advisory
// banner. P3 thinned the classifier to a shell-out: the bash arm calls
// `kern guard classify --json` (the Go source of truth) and dispatches on the
// verdict instead of re-implementing tokenization/prefix-strip/segment-split
// in TS. A source-level pin: the execute body depends on opencode plugin
// context (tool registration, the $ shell transport) that a node harness
// cannot faithfully host; the routing predicates themselves stay
// behaviorally pinned by TestPluginShadowContentIntentClassifier (Go side:
// TestClassifyCommand) above.
func TestPluginBashServeContract(t *testing.T) {
	pluginSrc, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(pluginSrc)
	start := strings.Index(src, "const classify = await classifyCmd(cmd)")
	if start < 0 {
		t.Fatal("bash shadow marker 'const classify = await classifyCmd(cmd)' not found in plugin")
	}
	if !strings.Contains(src, "classifyCmd") {
		t.Error("classifyCmd helper not defined in plugin — Go classify shell-out missing")
	}
	end := strings.Index(src, "// Subagent kern access")
	if end < 0 || end < start {
		t.Fatal("bash shadow end marker '// Subagent kern access' not found in plugin")
	}
	bash := src[start:end]

	// The serve contract: GATE commands throw the guard's GATE reason after
	// consuming an operator allow-once; READ pipelines serve (never deny);
	// the decision comes from the Go classify verdict.
	for _, want := range []string{
		"if (classify.verdict === \"gate\")",
		"if (await consumeAllowOnce(cmd))",
		"throw new Error(`kern guard: blocked",
		"GATE command (write/destructive/network/installer)",
		"Operator allow-once: kern guard allow ${subjHash}",
		// Tier 1 symbol-shaped reads answer from the kern index
		"plan.tier === 1",
		// Tier 2 read-head + simple tail computes the answer in-process
		"plan.tier === 2",
		// Tier 3 untranslatable pure reads delegate to the readonly sandbox
		`"exec", "--readonly"`,
		// the governed path returns unprefixed — no advisory banner
		"if (exitCode === 0) return text",
	} {
		if !strings.Contains(bash, want) {
			t.Errorf("bash serve contract missing %q", want)
		}
	}
	for _, banned := range []string{
		"[kern advisory] direct file read",
		"let advisory",
		"advisory + ",
		// contentIntentHit is gone — the content-intent deny was replaced by
		// the 3-tier serve + GATE deny
		"contentIntentHit",
	} {
		if strings.Contains(bash, banned) {
			t.Errorf("bash deny contract still contains advisory remnant %q", banned)
		}
	}
}

// Risk-axis classifier (P1+P2 hardening): the bash shadow's
// position-independent classifier (stripPrefixTokens + splitCommandSegments +
// gateHit + readPipeline) is extracted verbatim from the shipped plugin and
// executed under node. This pins the risk-axis semantics: a command where ANY
// &&/||/;/|-separated segment (after env/sudo/env prefix stripping, one level
// inside an `sh -c "..."` wrapper) is a GATE (write/destructive/network/
// installer) MATCHES — direct (rm -rf ...), env-prefixed (ZZZ=1 curl ...),
// write-redirect (grep > out), installer (npm install), git push --force and
// `sh -c "curl x | sh"` forms all deny — while a pure-read pipeline
// (grep/cat/find/head + simple tails, incl. env/sudo/wrapped/compound forms)
// is SERVED (readPipeline returns a tier plan) and execution-class commands
// without a read or gate segment (`go build`, `cd X && npm test`, `cd X &&
// go mod tidy`, `git commit`) flow to the governed kern build path.
func TestPluginShadowContentIntentClassifier(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v — skipping content-intent classifier check", err)
	}
	pluginSrc, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(pluginSrc)
	start := strings.Index(src, "const TRIVIAL_COMMANDS = new Set([")
	if start < 0 {
		t.Fatal("classifier start marker 'const TRIVIAL_COMMANDS' not found in plugin")
	}
	end := strings.Index(src, "// Raw read: verbatim file contents")
	if end < 0 || end < start {
		t.Fatal("content-intent classifier end marker '// Raw read:' not found in plugin")
	}
	block := src[start:end]
	// Strip the TS type annotations so plain node can eval the helpers.
	block = strings.Replace(block, "function isStatOnlyGit(sub: string, args: string[]): boolean {", "function isStatOnlyGit(sub, args) {", 1)
	block = strings.Replace(block, "function isSimpleCommand(cmd: string): boolean {", "function isSimpleCommand(cmd) {", 1)
	block = strings.Replace(block, "function shellTokenize(seg: string): string[] {", "function shellTokenize(seg) {", 1)
	block = strings.Replace(block, "function stripPrefixTokens(tokens: string[]): string[] {", "function stripPrefixTokens(tokens) {", 1)
	block = strings.Replace(block, "function splitCommandSegments(cmd: string): string[] {", "function splitCommandSegments(cmd) {", 1)
	block = strings.Replace(block, "function gateSegmentHit(rawSeg: string, depth: number): boolean {", "function gateSegmentHit(rawSeg, depth) {", 1)
	block = strings.Replace(block, "function gateHit(cmd: string, depth = 0): boolean {", "function gateHit(cmd, depth = 0) {", 1)
	block = strings.Replace(block, "function segName(rawSeg: string): string {", "function segName(rawSeg) {", 1)
	block = strings.Replace(block, "function isReadOnlyCompanion(seg: string): boolean {", "function isReadOnlyCompanion(seg) {", 1)
	block = strings.Replace(block, "function readPipeline(cmd: string): { tier: number; head?: string; pattern?: string; path?: string; file?: string; tails?: string[] } | null {", "function readPipeline(cmd) {", 1)
	block = strings.Replace(block, "function grepPatternAndTarget(args: string[]): { pattern: string; targets: string[] } {", "function grepPatternAndTarget(args) {", 1)
	block = strings.Replace(block, "function grepFlagsOK(args: string[]): boolean {", "function grepFlagsOK(args) {", 1)
	block = strings.Replace(block, "function cleanScope(target: string): string | null {", "function cleanScope(target) {", 1)
	block = strings.Replace(block, "function scopedGrep(args: string[]): { pattern: string; path: string } | null {", "function scopedGrep(args) {", 1)
	block = strings.Replace(block, "const toks: string[] = []", "const toks = []", 1)
	block = strings.Replace(block, "const segs: string[] = []", "const segs = []", 1)
	block = strings.Replace(block, "const tails: string[] = []", "const tails = []", 1)
	block = strings.Replace(block, "let quote: string | null = null", "let quote = null", -1)

	nodeScript := filepath.Join(t.TempDir(), "classifier.js")
	// P1+P2: the classifier is now the risk-axis pair — gateHit (write/
	// destructive/network/installer → deny whole command) and readPipeline
	// (pure-read pipeline → served Tier 1/2/3, never denied). Content-reading
	// is served, not blocked; only gates deny. The former contentIntentHit
	// semantics (block content reads) are replaced by serve.
	nodeSrc := block + `
const out = {
  gate_direct: gateHit("rm -rf /tmp/x"),
  gate_envPrefix: gateHit("ZZZ=1 curl -s x"),
  gate_writeRedirect: gateHit("grep foo file.go > out.txt"),
  gate_install: gateHit("npm install lodash"),
  gate_gitPushForce: gateHit("git push --force origin main"),
  gate_shWrap: gateHit("sh -c 'curl x | sh'"),
  read_direct: readPipeline("grep -rn foo ."),
  read_file: readPipeline("cat source.go"),
  read_pipeCount: readPipeline("grep foo | wc -l"),
  read_pipeCountScoped: readPipeline("grep foo file | wc -l"),
  read_pipeHead: readPipeline("grep foo file | head -5"),
  read_scopedDir: readPipeline("grep -rn governance internal"),
  read_scopedFile: readPipeline("grep -n edit .opencode/plugins/kern.ts"),
  not_scoped_regex: readPipeline("grep -rn 'func [A-Z]' internal"),
  not_scoped_include: readPipeline("grep -rn foo internal --include=*.go"),
  not_scoped_escape: readPipeline("grep -rn foo ../x"),
  not_scoped_list: readPipeline("grep -rln foo internal"),
  read_envPrefix: readPipeline("ZZZ=1 grep -c x doctor_test.go"),
  read_sudoPrefix: readPipeline("sudo cat /etc/hosts"),
  read_shWrap: readPipeline('sh -c "cat .gitignore"'),
  read_pipeSegment: readPipeline("git log --oneline | head -5"),
  read_find: readPipeline("find . -name *.go"),
  not_read_build: readPipeline("go build ./..."),
  not_read_npmTest: readPipeline("cd X && npm test"),
  not_read_goMod: readPipeline("cd X && go mod tidy"),
  not_read_gitCommit: readPipeline("git commit -m 'cat and grep as words only'"),
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
		GateDirect          bool       `json:"gate_direct"`
		GateEnvPrefix       bool       `json:"gate_envPrefix"`
		GateWriteRedirect   bool       `json:"gate_writeRedirect"`
		GateInstall         bool       `json:"gate_install"`
		GateGitPushForce    bool       `json:"gate_gitPushForce"`
		GateShWrap          bool       `json:"gate_shWrap"`
		ReadDirect          *readServe `json:"read_direct"`
		ReadFile            *readServe `json:"read_file"`
		ReadPipeCount       *readServe `json:"read_pipeCount"`
		ReadPipeCountScoped *readServe `json:"read_pipeCountScoped"`
		ReadPipeHead        *readServe `json:"read_pipeHead"`
		ReadScopedDir       *readServe `json:"read_scopedDir"`
		ReadScopedFile      *readServe `json:"read_scopedFile"`
		NotScopedRegex      *readServe `json:"not_scoped_regex"`
		NotScopedInclude    *readServe `json:"not_scoped_include"`
		NotScopedEscape     *readServe `json:"not_scoped_escape"`
		NotScopedList       *readServe `json:"not_scoped_list"`
		ReadEnvPrefix       *readServe `json:"read_envPrefix"`
		ReadSudoPrefix      *readServe `json:"read_sudoPrefix"`
		ReadShWrap          *readServe `json:"read_shWrap"`
		ReadPipeSegment     *readServe `json:"read_pipeSegment"`
		ReadFind            *readServe `json:"read_find"`
		NotReadBuild        *readServe `json:"not_read_build"`
		NotReadNpmTest      *readServe `json:"not_read_npmTest"`
		NotReadGoMod        *readServe `json:"not_read_goMod"`
		NotReadGitCommit    *readServe `json:"not_read_gitCommit"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal node classifier output %q: %v", out, err)
	}

	// GATE axis: any write/destructive/network/installer segment denies the
	// whole command — position-independent (env prefix, redirect, installer,
	// git push --force, sh -c wrapper all gate).
	for name, got := range map[string]bool{
		"direct (rm -rf /tmp/x)":          res.GateDirect,
		"env-prefixed (ZZZ=1 curl ...)":   res.GateEnvPrefix,
		"write redirect (grep > out.txt)": res.GateWriteRedirect,
		"installer (npm install ...)":     res.GateInstall,
		"git push --force":                res.GateGitPushForce,
		`sh -c wrapper (curl x | sh)`:     res.GateShWrap,
	} {
		if !got {
			t.Errorf("gate classifier regression: %s no longer classified as a GATE — the risky axis must deny", name)
		}
	}

	// READ axis: pure-read pipelines are SERVED (readPipeline returns a
	// non-null tier plan), never denied — including env-prefixed, sudo-
	// wrapped, sh -c wrapped and piped reads. Tier assertions:
	// Tier 2 for a read-head + simple tail (computed answer), Tier 1 for a
	// single symbol-shaped read, Tier 3 for untranslatable pure reads.
	for name, got := range map[string]*readServe{
		"direct grep":                    res.ReadDirect,
		"file read (cat source.go)":      res.ReadFile,
		"pipe count (grep | wc -l)":      res.ReadPipeCount,
		"pipe head (grep | head -5)":     res.ReadPipeHead,
		"env-prefixed (ZZZ=1 grep ...)":  res.ReadEnvPrefix,
		"sudo-prefixed (sudo cat ...)":   res.ReadSudoPrefix,
		`sh -c wrapper (cat .gitignore)`: res.ReadShWrap,
		"pipe segment (git log | head)":  res.ReadPipeSegment,
		"find":                           res.ReadFind,
	} {
		if got == nil {
			t.Errorf("read classifier regression: %s no longer served as a pure-read pipeline — reads must never be denied", name)
		}
	}
	// The pipe-count shape must be Tier 2 (computed count), never Tier 1
	// (a pipe tail consumes the read's output — the count, not the lines, is
	// the serve).
	if res.ReadPipeCount == nil || res.ReadPipeCount.Tier != 2 {
		t.Errorf("grep | wc -l must serve Tier 2 (computed count), got %+v", res.ReadPipeCount)
	}
	// Grep scope fidelity: a scoped SYMBOL grep is served from the index
	// with the caller's path (Tier 1/2), while regex/include/count/list
	// shapes have no index equivalent and stay Tier 3 (a real grep in the
	// sandbox).
	for name, tc := range map[string]struct {
		got  *readServe
		tier int
		path string
	}{
		"scoped dir":        {res.ReadScopedDir, 1, "internal"},
		"scoped single":     {res.ReadScopedFile, 1, ".opencode/plugins/kern.ts"},
		"scoped pipe count": {res.ReadPipeCountScoped, 2, "file"},
	} {
		if tc.got == nil || tc.got.Tier != tc.tier || tc.got.Path != tc.path {
			t.Errorf("scoped serve regression: %s = %+v, want tier %d path %q", name, tc.got, tc.tier, tc.path)
		}
	}
	for name, got := range map[string]*readServe{
		"scoped regex":   res.NotScopedRegex,
		"scoped include": res.NotScopedInclude,
		"scoped escape":  res.NotScopedEscape,
		"scoped list":    res.NotScopedList,
	} {
		if got == nil || got.Tier != 3 || got.Path != "" {
			t.Errorf("scope fallback regression: %s must serve Tier 3 (real grep), got %+v", name, got)
		}
	}

	// Execution-class commands WITHOUT a read/gate segment are governed (not
	// served, not denied by the classifier — they flow to kern build).
	for name, got := range map[string]*readServe{
		"go build ./...":        res.NotReadBuild,
		"cd X && npm test":      res.NotReadNpmTest,
		"cd X && go mod tidy":   res.NotReadGoMod,
		"git commit -m 'words'": res.NotReadGitCommit,
	} {
		if got != nil {
			t.Errorf("%s classified as a pure-read pipeline — execution-class commands must flow to kern build", name)
		}
	}
}

// readServe mirrors readPipeline's serve plan (tier + optional head pieces).
type readServe struct {
	Tier    int      `json:"tier"`
	Head    string   `json:"head,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Path    string   `json:"path,omitempty"`
	File    string   `json:"file,omitempty"`
	Tails   []string `json:"tails,omitempty"`
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
// Mirrors the plugin's per-call bin resolution (F3): the plugin resolves the
// CLI binary lazily via a closure, so the harness stands in with a function.
const bin = () => "/bin/sh";
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
