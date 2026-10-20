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
	block = strings.Replace(block, "function contentIntentHit(cmd: string, depth = 0): boolean {", "function contentIntentHit(cmd, depth = 0) {", 1)
	block = strings.Replace(block, "const toks: string[] = []", "const toks = []", 1)
	block = strings.Replace(block, "const segs: string[] = []", "const segs = []", 1)
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
		case strings.Contains(msg, "kern_verify"):
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
	throwTplRe := regexp.MustCompile("new Error\\(`([^`]*)`\\)")
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
	replSpliceRe := regexp.MustCompile(`\$\{repl\}|\$repl`)
	normalize := func(s string) string {
		s = cmdSpliceRe.ReplaceAllString(s, "'<cmd>'")
		s = hashSpliceRe.ReplaceAllString(s, "<hash>")
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

// TestPluginBashDenyContract pins the uniform fail-closed guard (operator
// directive, superseding the advisory posture): the bash
// shadow's content-intent branch DENIES — throws the guard's own reason
// (byte-parity pinned by TestPluginShadowGuardMessageParity) after
// consuming an operator allow-once, and the governed kern build path no
// longer prefixes any advisory banner. A source-level pin: the execute
// body depends on opencode plugin context (tool registration, the $
// shell transport) that a node harness cannot faithfully host; the
// routing predicates themselves stay behaviorally pinned by
// TestPluginShadowExemptionParity above.
func TestPluginBashDenyContract(t *testing.T) {
	pluginSrc, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(pluginSrc)
	start := strings.Index(src, "if (contentIntentHit(cmd)) {")
	if start < 0 {
		t.Fatal("bash shadow marker 'if (contentIntentHit(cmd))' not found in plugin")
	}
	if !strings.Contains(src, "function contentIntentHit(cmd") {
		t.Error("contentIntentHit helper not defined in plugin — position-independent classifier missing")
	}
	end := strings.Index(src, "// Subagent kern access")
	if end < 0 || end < start {
		t.Fatal("bash shadow end marker '// Subagent kern access' not found in plugin")
	}
	bash := src[start:end]

	// The deny contract: content-intent throws the guard's reason, after
	// consuming an operator allow-once for the exact command.
	for _, want := range []string{
		"if (await consumeAllowOnce(cmd))",
		"throw new Error(`kern guard: blocked",
		"not in the passthrough set",
		"Operator allow-once: kern guard allow ${subjHash}",
		// the governed path returns unprefixed — no advisory banner
		"if (exitCode === 0) return text",
	} {
		if !strings.Contains(bash, want) {
			t.Errorf("bash deny contract missing %q", want)
		}
	}
	for _, banned := range []string{
		"[kern advisory] direct file read",
		"let advisory",
		"advisory + ",
	} {
		if strings.Contains(bash, banned) {
			t.Errorf("bash deny contract still contains advisory remnant %q", banned)
		}
	}
}

// Content-intent classifier (position-independent hardening): the bash shadow's
// position-independent classifier (stripEnvPrefixes + splitCommandSegments +
// contentIntentHit) is extracted verbatim from the shipped plugin and
// executed under node. This pins the exact token list (drift fails CI) and
// the position-independent semantics: a command where ANY &&/||/;/|-separated
// segment (after env/sudo/env prefix stripping, one level inside an
// `sh -c "..."` wrapper) runs a content-reading tool MATCHES — direct
// (grep -rn ...), env-prefixed (ZZZ=1 grep ...), sudo/env-wrapped, compound
// (`cd X && grep ...`), piped (`git log | head -5`) and `sh -c "cat ..."`
// forms all block with the guard's message — while execution-class commands
// without a content-intent segment (`cd X && npm test`, `go build`, inline
// python) keep flowing to the governed kern build path.
func TestPluginShadowContentIntentClassifier(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v — skipping content-intent classifier check", err)
	}
	pluginSrc, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(pluginSrc)
	start := strings.Index(src, "function shellTokenize")
	if start < 0 {
		t.Fatal("content-intent classifier 'function shellTokenize' not found in plugin")
	}
	end := strings.Index(src, "// Raw read: verbatim file contents")
	if end < 0 || end < start {
		t.Fatal("content-intent classifier end marker '// Raw read:' not found in plugin")
	}
	block := src[start:end]
	// Strip the TS type annotations so plain node can eval the helpers.
	block = strings.Replace(block, "function shellTokenize(seg: string): string[] {", "function shellTokenize(seg) {", 1)
	block = strings.Replace(block, "function stripPrefixTokens(tokens: string[]): string[] {", "function stripPrefixTokens(tokens) {", 1)
	block = strings.Replace(block, "function splitCommandSegments(cmd: string): string[] {", "function splitCommandSegments(cmd) {", 1)
	block = strings.Replace(block, "function contentIntentHit(cmd: string, depth = 0): boolean {", "function contentIntentHit(cmd, depth = 0) {", 1)
	block = strings.Replace(block, "const toks: string[] = []", "const toks = []", 1)
	block = strings.Replace(block, "const segs: string[] = []", "const segs = []", 1)
	block = strings.Replace(block, "let quote: string | null = null", "let quote = null", -1)

	nodeScript := filepath.Join(t.TempDir(), "content-intents.js")
	nodeSrc := block + `
const out = {
  tokens: [...CONTENT_INTENT_CMDS].sort(),
  direct: contentIntentHit("grep -rn foo ."),
  envPrefix: contentIntentHit("ZZZ=1 grep -c x doctor_test.go"),
  bypassEnvPrefix: contentIntentHit("KERN_BYPASS=1 grep -n x f"),
  sudoPrefix: contentIntentHit("sudo cat /etc/hosts"),
  envWrapper: contentIntentHit("env cat /etc/hosts"),
  cdCompound: contentIntentHit("cd X && grep foo"),
  cdNpmCompound: contentIntentHit("cd X && npm test"),
  shWrap: contentIntentHit('sh -c "cat .gitignore"'),
  pipeSegment: contentIntentHit("git log --oneline | head -5"),
  goBuild: contentIntentHit("go build ./..."),
  pythonInline: contentIntentHit("python3 -c \"print('cat')\""),
  multiline: contentIntentHit("echo hi\ncat /etc/passwd"),
  sudoFlagValue: contentIntentHit("sudo -u nobody cat /etc/hosts"),
  envFlag: contentIntentHit("env -i cat /etc/hosts"),
  niceValueFlag: contentIntentHit("nice -n 5 cat /etc/hosts"),
  xargsWrap: contentIntentHit("xargs cat < list"),
  watchWrap: contentIntentHit("watch -n 2 cat /etc/hosts"),
  timeWrap: contentIntentHit("time cat /etc/hosts"),
  nohupWrap: contentIntentHit("nohup cat /etc/hosts"),
  straceOut: contentIntentHit("strace -o /dev/null cat /etc/hosts"),
  absPath: contentIntentHit("/bin/cat /etc/passwd"),
  relPath: contentIntentHit("./cat file"),
  shLcBundle: contentIntentHit("sh -lc 'cat /etc/passwd'"),
  bashIcBundle: contentIntentHit("bash -ic 'cat /etc/passwd'"),
  quotedEnvValue: contentIntentHit("A='b c' grep -n x f"),
  backslashObfuscation: contentIntentHit("ca\\t /etc/passwd"),
  sudoEnvAssign: contentIntentHit("sudo VAR=1 cat /etc/hosts"),
  gitCommitQuotedWords: contentIntentHit("git commit -m 'cat and grep as words only'"),
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
		Tokens               []string `json:"tokens"`
		Direct               bool     `json:"direct"`
		EnvPrefix            bool     `json:"envPrefix"`
		BypassEnvPrefix      bool     `json:"bypassEnvPrefix"`
		SudoPrefix           bool     `json:"sudoPrefix"`
		EnvWrapper           bool     `json:"envWrapper"`
		CdCompound           bool     `json:"cdCompound"`
		CdNpmCompound        bool     `json:"cdNpmCompound"`
		ShWrap               bool     `json:"shWrap"`
		PipeSegment          bool     `json:"pipeSegment"`
		GoBuild              bool     `json:"goBuild"`
		PythonInline         bool     `json:"pythonInline"`
		Multiline            bool     `json:"multiline"`
		SudoFlagValue        bool     `json:"sudoFlagValue"`
		EnvFlag              bool     `json:"envFlag"`
		NiceValueFlag        bool     `json:"niceValueFlag"`
		XargsWrap            bool     `json:"xargsWrap"`
		WatchWrap            bool     `json:"watchWrap"`
		TimeWrap             bool     `json:"timeWrap"`
		NohupWrap            bool     `json:"nohupWrap"`
		StraceOut            bool     `json:"straceOut"`
		AbsPath              bool     `json:"absPath"`
		RelPath              bool     `json:"relPath"`
		ShLcBundle           bool     `json:"shLcBundle"`
		BashIcBundle         bool     `json:"bashIcBundle"`
		QuotedEnvValue       bool     `json:"quotedEnvValue"`
		BackslashObfuscation bool     `json:"backslashObfuscation"`
		SudoEnvAssign        bool     `json:"sudoEnvAssign"`
		GitCommitQuotedWords bool     `json:"gitCommitQuotedWords"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal node classifier output %q: %v", out, err)
	}

	wantTokens := []string{"ag", "awk", "cat", "egrep", "find", "grep", "head", "less", "rg", "sed", "tail"}
	if strings.Join(res.Tokens, ",") != strings.Join(wantTokens, ",") {
		t.Errorf("content-intent tokens = %v, want %v", res.Tokens, wantTokens)
	}
	if !res.Direct {
		t.Error("direct content-intent command (grep -rn foo .) no longer classified — classifier must block direct content reads")
	}
	// Position-independent semantics: env prefixes, compound segments, sh -c wrappers and
	// pipe segments must ALL classify as content-intent now (the old
	// whole-command first-token-only match let them flow to the governed
	// path and read content).
	for name, got := range map[string]bool{
		"env-prefixed (ZZZ=1 grep ...)":          res.EnvPrefix,
		"env-prefixed (KERN_BYPASS=1 grep ...)":  res.BypassEnvPrefix,
		"sudo-prefixed (sudo cat ...)":           res.SudoPrefix,
		"env-wrapped (env cat ...)":              res.EnvWrapper,
		"compound (cd X && grep foo)":            res.CdCompound,
		`sh -c wrapper (sh -c "cat .gitignore")`: res.ShWrap,
		"pipe segment (git log | head -5)":       res.PipeSegment,
		"multiline (line 2 cat)":                 res.Multiline,
		"sudo flag+value (sudo -u nobody cat)":   res.SudoFlagValue,
		"env flag (env -i cat)":                  res.EnvFlag,
		"nice value flag (nice -n 5 cat)":        res.NiceValueFlag,
		"xargs (xargs cat < list)":               res.XargsWrap,
		"watch (watch -n 2 cat)":                 res.WatchWrap,
		"time (time cat)":                        res.TimeWrap,
		"nohup (nohup cat)":                      res.NohupWrap,
		"strace output flag (strace -o f cat)":   res.StraceOut,
		"absolute path (/bin/cat)":               res.AbsPath,
		"relative path (./cat)":                  res.RelPath,
		"sh -lc bundle":                          res.ShLcBundle,
		"bash -ic bundle":                        res.BashIcBundle,
		"quoted env value (A='b c' grep)":        res.QuotedEnvValue,
		"backslash obfuscation (ca\\t)":          res.BackslashObfuscation,
		"sudo env assignment (sudo VAR=1 cat)":   res.SudoEnvAssign,
	} {
		if !got {
			t.Errorf("classifier regression: %s no longer classified as content-intent — position-independent matching broken", name)
		}
	}
	// Execution-class commands WITHOUT a content-intent segment keep flowing
	// to the governed kern build path.
	for name, got := range map[string]bool{
		"cd X && npm test":             res.CdNpmCompound,
		"go build ./...":               res.GoBuild,
		"python3 -c inline":            res.PythonInline,
		"git commit -m 'quoted words'": res.GitCommitQuotedWords,
	} {
		if got {
			t.Errorf("%s classified as content-intent — execution-class commands must keep flowing to kern build", name)
		}
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
