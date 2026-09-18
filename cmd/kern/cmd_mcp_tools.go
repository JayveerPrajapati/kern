package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/transform"
)

func runMCPTool(toolName string, args map[string]any) {
	out, err := callTool(toolName, args)
	if err != nil {
		fatal("%s: %v — see kern doctor for diagnostics", toolName, err)
	}
	fmt.Println(out)
}

// callTool invokes an MCP tool against a fresh in-process server and returns
// its raw output.
func callTool(toolName string, args map[string]any) (string, error) {
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	return srv.CallTool(context.Background(), toolName, args)
}

func readStdinIfPipe() string {
	fi, err := os.Stdin.Stat()
	if err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		data, _ := io.ReadAll(os.Stdin)
		return string(data)
	}
	return ""
}

func runHealth(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("usage: kern health [--root DIR] [--json]\nflags: %v", err)
	}
	root := f.root
	// --json is accepted for CLI compatibility (pinned by
	// TestRunHealthIndexBlockIsDiskAuthoritative) but intentionally ignored:
	// health output is always JSON regardless of the flag.
	out, err := callTool("kern_health", map[string]any{"root": root})
	if err != nil {
		fatal("health: %v — see kern doctor for diagnostics", err)
	}
	// The MCP kern_health "index" block reflects the in-process server's
	// session cache, which a fresh CLI invocation NEVER loads: on a fresh
	// process it always reports fresh=false / symbols=0 / files=0 even when
	// the on-disk index is healthy, misleading users into rebuilding. Make
	// the DISK index the authoritative "index" block and relabel the
	// in-memory view so nobody mistakes it for the persisted state.
	var snap map[string]any
	if err := json.Unmarshal([]byte(out), &snap); err == nil {
		if memIdx, ok := snap["index"].(map[string]any); ok {
			memIdx["note"] = "in-memory MCP server session index (not loaded by the CLI; see the disk 'index' block)"
			snap["mcp_memory_index"] = memIdx
		}
		disk := index.DiskIndexView(root)
		if disk == nil {
			// No persisted index yet: say so explicitly instead of zeroes.
			disk = map[string]any{
				"root":    root,
				"built":   false,
				"fresh":   false,
				"symbols": 0,
				"files":   0,
				"note":    "no persisted index — run `kern index` to build one",
			}
		}
		snap["index"] = disk
		snap["version"] = version
		data, err := json.MarshalIndent(snap, "", "  ")
		if err == nil {
			fmt.Println(string(data))
			return
		}
	}
	fmt.Println(out)
}

func runCompose(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	pipelineJSON := f.pipeline
	timeout := "60"
	if f.timeoutSet {
		timeout = strconv.Itoa(f.timeout)
	}

	raw := pipelineJSON
	if raw == "" && len(pos) > 0 {
		raw = strings.Join(pos, " ")
	}
	if raw == "" {
		raw = readStdinIfPipe()
	}
	if raw == "" {
		fatalUsage("usage: kern compose --pipeline '[{\"tool\": \"kern_search\", \"args\": {\"query\": \"Index.Search\"}}]'")
	}
	var steps any
	if err := json.Unmarshal([]byte(raw), &steps); err != nil {
		fatal("compose: invalid pipeline JSON: %v", err)
	}
	runMCPTool("kern_compose", map[string]any{
		"root":     root,
		"pipeline": steps,
		"timeout":  timeout,
	})
}

func runPreEdit(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("usage: kern pre-edit [--file PATH] [--lines N] [--symbol NAME] [--root DIR] [--json]")
	}

	file := f.file
	if file == "" && len(pos) > 0 {
		file = pos[0]
	}
	// Never bless a path that does not exist: a nonexistent -file must fail
	// loudly (rc=1) instead of producing a LOW-risk "Safe to proceed" report.
	if file != "" {
		if _, err := os.Stat(file); err != nil {
			fatal("pre-edit: file not found: %s", file)
		}
	}
	args := map[string]any{"root": f.root}
	if file != "" {
		args["file"] = file
	}
	if f.lines > 0 {
		args["lines"] = strconv.Itoa(f.lines)
	}
	if f.symbol != "" {
		args["symbol"] = f.symbol
	} else if len(pos) > 1 {
		args["symbol"] = pos[1]
	}
	out, err := callTool("kern_pre_edit", args)
	if err != nil {
		fatal("pre-edit: %v — see kern doctor for diagnostics", err)
	}
	if f.json {
		risk := "UNKNOWN"
		if i := strings.Index(out, "[Risk: "); i >= 0 {
			restStr := out[i+len("[Risk: "):]
			if j := strings.Index(restStr, "]"); j >= 0 {
				risk = restStr[:j]
			}
		}
		data, merr := json.MarshalIndent(map[string]any{
			"risk":   risk,
			"report": out,
		}, "", "  ")
		if merr == nil {
			fmt.Println(string(data))
			return
		}
	}
	fmt.Println(out)
}

func runPromptFill(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	t := f.template
	if t == "" && len(pos) > 0 {
		t = pos[0]
	}
	desc := f.task
	if desc == "" && len(pos) > 1 {
		desc = strings.Join(pos[1:], " ")
	}
	if t == "" {
		fatalUsage("usage: kern prompt-fill --template <name> [--task <desc>] [--file <path>]")
	}
	args := map[string]any{
		"template":      t,
		"task":          desc,
		"inject_memory": f.injectMemory,
		"root":          f.root,
	}
	if f.file != "" {
		args["file"] = f.file
	}
	runMCPTool("kern_prompt_fill", args)
}

func runSemanticDiff(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v\nusage: kern semantic-diff [--from REV] [--to REV] [--range A..B] [--root DIR]", err)
	}

	args := map[string]any{"root": f.root}
	if f.from != "" {
		args["from"] = f.from
	}
	if f.to != "" {
		args["to"] = f.to
	}
	if f.range_ != "" {
		args["range"] = f.range_
	} else if len(pos) > 0 {
		args["range"] = pos[0]
	}
	runMCPTool("kern_semantic_diff", args)
}

func runEvidenceAnchor(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	c := f.claim
	if c == "" && len(pos) > 0 {
		c = strings.Join(pos, " ")
	}
	args := map[string]any{"root": f.root, "claim": c}
	if f.file != "" {
		args["file"] = f.file
	}
	if f.lineRaw != "" {
		args["line"] = f.lineRaw
	}
	if f.symbol != "" {
		args["symbol"] = f.symbol
	}
	runMCPTool("kern_evidence_anchor", args)
}

func runContextWatch(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	budget := "32000"
	if f.budgetSet {
		budget = strconv.Itoa(f.budget)
	}
	format := f.format
	raw := f.text
	if raw == "" && len(pos) > 0 {
		raw = strings.Join(pos, " ")
	}
	if raw == "" {
		raw = readStdinIfPipe()
	}
	if raw == "" {
		fatalUsage("usage: kern context-watch [--budget NUM] [--format text|json] <text>")
	}
	runMCPTool("kern_context_watch", map[string]any{
		"text":   raw,
		"budget": budget,
		"format": format,
	})
}

func runAgentFingerprint(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	args := map[string]any{"format": f.format}
	if f.agent != "" {
		args["agent_id"] = f.agent
	} else if len(pos) > 0 {
		args["agent_id"] = pos[0]
	}
	runMCPTool("kern_agent_fingerprint", args)
}

func runExplain(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v\nusage: kern explain <target-symbol-or-file> [--root DIR]", err)
	}

	t := f.target
	if t == "" && len(pos) > 0 {
		t = pos[0]
	}
	// QA: Go's flag package stops parsing at the first positional, so
	// `kern explain <sym> --nonsense` previously swallowed the unknown flag
	// (exit 0). The unified parser rejects unknown flags everywhere (before
	// AND after positionals), so the trailing-flag sweep is no longer needed.
	if t == "" {
		fatalUsage("usage: kern explain <target-symbol-or-file> [--root DIR]")
	}
	runMCPTool("kern_explain", map[string]any{
		"target": t,
		"root":   f.root,
	})
}

func runCrossRepoImpact(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v\nusage: kern cross-repo-impact <symbol> [--repo <path>]... [--root DIR]", err)
	}

	t := f.target
	if t == "" {
		t = f.symbol
	}
	if t == "" && len(pos) > 0 {
		t = pos[0]
	}
	// QA: Go's flag package stops parsing at the first positional, so
	// `kern cross-repo-impact <sym> --nonsense` previously swallowed the
	// unknown flag (exit 0). The unified parser rejects unknown flags
	// everywhere, so the trailing-flag sweep is no longer needed.
	if t == "" {
		fatalUsage("usage: kern cross-repo-impact <symbol> [--repo <path>]... [--root DIR]")
	}
	for _, rp := range f.repoPaths {
		if _, err := os.Stat(rp); err != nil {
			fatal("cross-repo-impact: linked repo not found: %s", rp)
		}
	}
	runMCPTool("kern_cross_repo_impact", map[string]any{
		"target_symbol": t,
		"linked_repos":  f.repoPaths,
		"root":          f.root,
	})
}

func runMemoryRanked(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	p := f.prompt
	if p == "" && len(pos) > 0 {
		p = strings.Join(pos, " ")
	}
	if p == "" {
		fatalUsage("usage: kern memory-ranked <prompt> [-k 5] [--half-life 7.0] [--root DIR]")
	}
	runMCPTool("kern_memory_ranked", map[string]any{
		"prompt":         p,
		"k":              f.k,
		"half_life_days": f.halfLife,
		"root":           f.root,
	})
}

func runPolicyDSL(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v\nusage: kern policy_dsl [--policy TEXT|FILE] [--diff STR] [--file F]...", err)
	}

	if f.policy == "" && len(pos) > 0 {
		// Positional DSL text (e.g. `kern policy_dsl 'allow agent=...'`)
		// was previously silently dropped.
		f.policy = strings.Join(pos, " ")
	}
	args := map[string]any{"root": f.root}
	if f.policy != "" {
		args["policy"] = f.policy
	}
	if f.diff != "" {
		args["diff"] = f.diff
	}
	if len(f.files) > 0 {
		args["files"] = f.files
	}
	// Run the tool directly so the CLI can mirror the verdict in its exit
	// code. A BLOCKED verdict is a successful tool call (the policy ran) but
	// the change must not pass: rc=1 — fail closed, never a vacuous ALLOWED
	// (F7). Missing policy files already surface as tool errors (rc=1).
	out, err := callTool("kern_policy_dsl", args)
	if err != nil {
		fatal("policy-dsl: %v — see kern doctor for diagnostics", err)
	}
	fmt.Println(out)
	if strings.Contains(out, "BLOCKED") {
		fatal("policy-dsl: verdict BLOCKED — see output above")
	}
}

func runAgentCoordination(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	act := f.action
	if act == "" {
		act = "status"
	}
	if len(pos) > 0 {
		act = pos[0]
	}
	args := map[string]any{
		"action":      act,
		"agent_id":    f.agent,
		"from_agent":  f.from,
		"to_agent":    f.to,
		"task_id":     f.task,
		"resource":    f.resource,
		"ttl_seconds": f.ttl,
		"notes":       f.notes,
		"root":        f.root,
	}
	runMCPTool("kern_agent_coordination", args)
}

func runAgentRoleRBAC(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("usage: kern agent_role_rbac -tool <name> [-action evaluate] [-agent ID] [-role R]")
	}

	act := f.action
	if act == "" {
		act = "evaluate"
	}
	if len(pos) > 0 {
		act = pos[0]
	}
	args := map[string]any{
		"action":   act,
		"agent_id": f.agent,
		"role":     f.role,
		"tool":     f.tool,
		"root":     f.root,
	}
	runMCPTool("kern_agent_role_rbac", args)
}

func runStream(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	act := f.action
	if act == "" {
		act = "status"
	}
	if len(pos) > 0 {
		act = pos[0]
	}
	args := map[string]any{
		"action":         act,
		"channel":        f.channel,
		"payload":        f.payload,
		"chunk_size":     f.chunkSize,
		"progress_token": f.progressToken,
		"percent":        f.percent,
		"message":        f.message,
	}
	runMCPTool("kern_stream", args)
}

func runAstTransform(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	act := f.action
	if act == "" {
		act = "implement_interface"
	}
	if len(pos) > 0 {
		act = pos[0]
	}

	// Run the transformation in-process instead of through the loopback
	// MCP server. The loopback path spawns a fresh server whose own index
	// build races the tool call, so interface lookups against the project
	// index always miss ("unknown interface") on the first invocation.
	// Loading the index directly (the same loadOrBuild every other CLI
	// command uses) makes implement_interface resolve real interfaces.
	ix, err := loadOrBuild(f.root)
	if err != nil {
		fatal("ast-transform: %v", err)
	}
	req := transform.Request{
		Action:          act,
		File:            f.file,
		Root:            f.root,
		TargetSymbol:    f.target,
		InterfaceName:   f.iface,
		FieldName:       f.field,
		FieldType:       f.fieldType,
		FieldTag:        f.tag,
		MethodSignature: f.sig,
		MethodBody:      f.body,
		Apply:           f.apply,
		Index:           ix,
	}
	res, err := transform.Transform(req)
	if err != nil {
		fatal("ast-transform: %v", err)
	}
	fmt.Printf("# AST Transform Report: %s\n\n", res.Action)
	fmt.Printf("- **Target Symbol**: `%s`\n", res.TargetSymbol)
	if res.File != "" {
		fmt.Printf("- **File**: `%s`\n", res.File)
	}
	fmt.Printf("- **Applied to Disk**: `%v`\n", res.Applied)
	if len(res.Added) > 0 {
		fmt.Printf("- **Added Symbols**: %s\n\n", strings.Join(res.Added, ", "))
	}
	if res.Diff != "" {
		fmt.Printf("### Proposed AST Diff\n```diff\n%s\n```\n", res.Diff)
	} else {
		fmt.Println("\n*No modifications needed (declarations already satisfy contract).*")
	}
}

func runSemanticMerge(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}

	format := "text"
	if f.json {
		format = "json"
	}

	args := map[string]any{
		"file":   f.file,
		"base":   f.base,
		"local":  f.local,
		"remote": f.remote,
		"apply":  f.apply,
		"format": format,
		"root":   f.root,
	}
	runMCPTool("kern_semantic_merge", args)
}

func runSynthesizeTest(rest []string) {
	f, pos, err := parseFlags(rest)
	if err != nil {
		// stdlibFlagErr keeps the pinned "flag provided but not defined"
		// wording (TestRunSynthesizeTestBadFlagExits2); the parse itself is
		// the unified parseFlags.
		fatalUsage("flags: %v\nusage: kern synthesize-test [--target NAME] [--file PATH] [--auto-gap] [--apply] [--json] [--root DIR]", stdlibFlagErr(err))
	}

	tgt := f.target
	if tgt == "" && len(pos) > 0 {
		tgt = pos[0]
	}

	format := "text"
	if f.json {
		format = "json"
	}

	args := map[string]any{
		"target":   tgt,
		"file":     f.file,
		"auto_gap": f.autoGap,
		"apply":    f.apply,
		"format":   format,
		"root":     f.root,
	}
	runMCPTool("kern_synthesize_test", args)
}
