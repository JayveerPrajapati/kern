package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp"
)

func runMCPTool(toolName string, args map[string]any) {
	out, err := callTool(toolName, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern %s: %v — see kern doctor for diagnostics\n", toolName, err)
		panic(exitError{code: 1})
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
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	root := fs.String("root", ".", "project root")
	jsonFlag := fs.Bool("json", false, "emit JSON (health always emits JSON; flag kept for symmetry)")
	_ = jsonFlag
	_ = fs.Parse(rest)
	out, err := callTool("kern_health", map[string]any{"root": *root})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern health: %v — see kern doctor for diagnostics\n", err)
		panic(exitError{code: 1})
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
		disk := diskIndexView(*root)
		if disk == nil {
			// No persisted index yet: say so explicitly instead of zeroes.
			disk = map[string]any{
				"root":    *root,
				"built":   false,
				"fresh":   false,
				"symbols": 0,
				"files":   0,
				"note":    "no persisted index — run `kern index` to build one",
			}
		}
		snap["index"] = disk
		data, err := json.MarshalIndent(snap, "", "  ")
		if err == nil {
			fmt.Println(string(data))
			return
		}
	}
	fmt.Println(out)
}

// diskIndexView summarizes the persisted index.json for a root, or nil when
// none exists yet (a normal first-run state). Unreadable or
// schema-mismatched indexes are reported as "rebuild required" — never as
// silent zeroes. The freshness verdict uses the same decision `kern index
// --status` makes: the cheap git tree-OID probe when decisive, and the loose
// content proof otherwise (non-git worktree, legacy index without a tree
// OID). It is the authoritative `index` block of `kern health`.
func diskIndexView(root string) map[string]any {
	if _, err := os.Stat(index.StorePath(root)); err != nil {
		return nil // nothing persisted yet
	}
	ix, err := index.Load(root)
	if err != nil {
		return map[string]any{"root": root, "version": 0, "built": false, "fresh": false, "stale": true, "rebuild_required": err.Error()}
	}
	if ix == nil {
		return nil
	}
	verdict := "unknown"
	if fresh, decided, _ := ix.TreeOIDProbe(root); decided {
		if fresh {
			verdict = "fresh"
		} else {
			verdict = "stale"
		}
	} else {
		switch ix.FreshnessProof(root).Verdict {
		case index.FreshnessFresh:
			verdict = "fresh"
		case index.FreshnessStale:
			verdict = "stale"
		}
	}
	return map[string]any{
		"root":       root,
		"built":      true,
		"fresh":      verdict == "fresh",
		"stale":      verdict != "fresh",
		"verdict":    verdict,
		"version":    ix.Version,
		"symbols":    len(ix.Symbols),
		"files":      len(ix.FileHashes),
		"packages":   len(ix.Pkgs),
		"languages":  ix.Languages(),
		"store":      index.StorePath(root),
		"updated_at": ix.UpdatedAt.Format(time.RFC3339),
	}
}

func runCompose(rest []string) {
	fs := flag.NewFlagSet("compose", flag.ContinueOnError)
	root := fs.String("root", ".", "project root")
	pipelineJSON := fs.String("pipeline", "", "pipeline JSON string")
	timeout := fs.String("timeout", "60", "per-step timeout in seconds")
	_ = fs.Parse(rest)

	raw := *pipelineJSON
	if raw == "" && len(fs.Args()) > 0 {
		raw = strings.Join(fs.Args(), " ")
	}
	if raw == "" {
		raw = readStdinIfPipe()
	}
	if raw == "" {
		fmt.Fprintln(os.Stderr, `usage: kern compose --pipeline '[{"tool": "kern_search", "args": {"query": "foo"}}]'`)
		panic(exitError{code: 2})
	}
	var steps any
	if err := json.Unmarshal([]byte(raw), &steps); err != nil {
		fmt.Fprintf(os.Stderr, "kern compose: invalid pipeline JSON: %v\n", err)
		panic(exitError{code: 1})
	}
	runMCPTool("kern_compose", map[string]any{
		"root":     *root,
		"pipeline": steps,
		"timeout":  *timeout,
	})
}

func runPreEdit(rest []string) {
	fs := flag.NewFlagSet("pre-edit", flag.ContinueOnError)
	file := fs.String("file", "", "file path")
	lines := fs.String("lines", "", "line range")
	symbol := fs.String("symbol", "", "symbol name")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	f := *file
	if f == "" && len(fs.Args()) > 0 {
		f = fs.Args()[0]
	}
	args := map[string]any{"root": *root}
	if f != "" {
		args["file"] = f
	}
	if *lines != "" {
		args["lines"] = *lines
	}
	if *symbol != "" {
		args["symbol"] = *symbol
	} else if len(fs.Args()) > 1 {
		args["symbol"] = fs.Args()[1]
	}
	runMCPTool("kern_pre_edit", args)
}

func runPromptFill(rest []string) {
	fs := flag.NewFlagSet("prompt-fill", flag.ContinueOnError)
	tmpl := fs.String("template", "", "template name")
	task := fs.String("task", "", "task description")
	file := fs.String("file", "", "target file path")
	injectMem := fs.String("inject-memory", "true", "inject memory")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	t := *tmpl
	if t == "" && len(fs.Args()) > 0 {
		t = fs.Args()[0]
	}
	desc := *task
	if desc == "" && len(fs.Args()) > 1 {
		desc = strings.Join(fs.Args()[1:], " ")
	}
	if t == "" {
		fmt.Fprintln(os.Stderr, "usage: kern prompt-fill --template <name> [--task <desc>] [--file <path>]")
		panic(exitError{code: 2})
	}
	args := map[string]any{
		"template":      t,
		"task":          desc,
		"inject_memory": *injectMem,
		"root":          *root,
	}
	if *file != "" {
		args["file"] = *file
	}
	runMCPTool("kern_prompt_fill", args)
}

func runSemanticDiff(rest []string) {
	fs := flag.NewFlagSet("semantic-diff", flag.ContinueOnError)
	from := fs.String("from", "", "from revision")
	to := fs.String("to", "", "to revision")
	gitRange := fs.String("range", "", "git revision range")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	args := map[string]any{"root": *root}
	if *from != "" {
		args["from"] = *from
	}
	if *to != "" {
		args["to"] = *to
	}
	if *gitRange != "" {
		args["range"] = *gitRange
	} else if len(fs.Args()) > 0 {
		args["range"] = fs.Args()[0]
	}
	runMCPTool("kern_semantic_diff", args)
}

func runEvidenceAnchor(rest []string) {
	fs := flag.NewFlagSet("evidence-anchor", flag.ContinueOnError)
	claim := fs.String("claim", "", "claim or citation")
	file := fs.String("file", "", "file path")
	line := fs.String("line", "", "line number")
	symbol := fs.String("symbol", "", "symbol name")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	c := *claim
	if c == "" && len(fs.Args()) > 0 {
		c = strings.Join(fs.Args(), " ")
	}
	args := map[string]any{"root": *root, "claim": c}
	if *file != "" {
		args["file"] = *file
	}
	if *line != "" {
		args["line"] = *line
	}
	if *symbol != "" {
		args["symbol"] = *symbol
	}
	runMCPTool("kern_evidence_anchor", args)
}

func runContextWatch(rest []string) {
	fs := flag.NewFlagSet("context-watch", flag.ContinueOnError)
	budget := fs.String("budget", "32000", "token budget")
	format := fs.String("format", "text", "output format")
	text := fs.String("text", "", "context text")
	_ = fs.Parse(rest)

	raw := *text
	if raw == "" && len(fs.Args()) > 0 {
		raw = strings.Join(fs.Args(), " ")
	}
	if raw == "" {
		raw = readStdinIfPipe()
	}
	if raw == "" {
		fmt.Fprintln(os.Stderr, "usage: kern context-watch [--budget NUM] [--format text|json] <text>")
		panic(exitError{code: 2})
	}
	runMCPTool("kern_context_watch", map[string]any{
		"text":   raw,
		"budget": *budget,
		"format": *format,
	})
}

func runAgentFingerprint(rest []string) {
	fs := flag.NewFlagSet("agent-fingerprint", flag.ContinueOnError)
	agentID := fs.String("agent", "", "agent id")
	format := fs.String("format", "text", "output format")
	_ = fs.Parse(rest)

	args := map[string]any{"format": *format}
	if *agentID != "" {
		args["agent_id"] = *agentID
	} else if len(fs.Args()) > 0 {
		args["agent_id"] = fs.Args()[0]
	}
	runMCPTool("kern_agent_fingerprint", args)
}

func runExplain(rest []string) {
	fs := flag.NewFlagSet("explain", flag.ContinueOnError)
	target := fs.String("target", "", "target symbol or file")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	t := *target
	if t == "" && len(fs.Args()) > 0 {
		t = fs.Args()[0]
	}
	if t == "" {
		fmt.Fprintln(os.Stderr, "usage: kern explain <target-symbol-or-file> [--root DIR]")
		panic(exitError{code: 2})
	}
	runMCPTool("kern_explain", map[string]any{
		"target": t,
		"root":   *root,
	})
}

func runCrossRepoImpact(rest []string) {
	fs := flag.NewFlagSet("cross-repo-impact", flag.ContinueOnError)
	target := fs.String("target", "", "target symbol")
	root := fs.String("root", ".", "project root")
	var repos arrayFlag
	fs.Var(&repos, "repo", "linked repo path (repeatable)")
	_ = fs.Parse(rest)

	t := *target
	if t == "" && len(fs.Args()) > 0 {
		t = fs.Args()[0]
	}
	if t == "" {
		fmt.Fprintln(os.Stderr, "usage: kern cross-repo-impact <symbol> [--repo <path>]... [--root DIR]")
		panic(exitError{code: 2})
	}
	runMCPTool("kern_cross_repo_impact", map[string]any{
		"target_symbol": t,
		"linked_repos":  []string(repos),
		"root":          *root,
	})
}

func runMemoryRanked(rest []string) {
	fs := flag.NewFlagSet("memory-ranked", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "task prompt")
	k := fs.String("k", "5", "max lessons")
	halfLife := fs.String("half-life", "7.0", "half-life in days")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	p := *prompt
	if p == "" && len(fs.Args()) > 0 {
		p = strings.Join(fs.Args(), " ")
	}
	if p == "" {
		fmt.Fprintln(os.Stderr, "usage: kern memory-ranked <prompt> [-k 5] [--half-life 7.0] [--root DIR]")
		panic(exitError{code: 2})
	}
	runMCPTool("kern_memory_ranked", map[string]any{
		"prompt":         p,
		"k":              *k,
		"half_life_days": *halfLife,
		"root":           *root,
	})
}

func runPolicyDSL(rest []string) {
	fs := flag.NewFlagSet("policy-dsl", flag.ContinueOnError)
	policy := fs.String("policy", "", "policy YAML/JSON or file path")
	diff := fs.String("diff", "", "diff string")
	root := fs.String("root", ".", "project root")
	var files arrayFlag
	fs.Var(&files, "file", "changed file (repeatable)")
	_ = fs.Parse(rest)

	args := map[string]any{"root": *root}
	if *policy != "" {
		args["policy"] = *policy
	}
	if *diff != "" {
		args["diff"] = *diff
	}
	if len(files) > 0 {
		args["files"] = []string(files)
	}
	runMCPTool("kern_policy_dsl", args)
}

func runAgentCoordination(rest []string) {
	fs := flag.NewFlagSet("agent-coordination", flag.ContinueOnError)
	action := fs.String("action", "status", "action: handoff, claim, release, inbox, status")
	agentID := fs.String("agent", "", "agent id")
	fromAgent := fs.String("from", "", "from agent")
	toAgent := fs.String("to", "", "to agent")
	taskID := fs.String("task", "", "task id")
	resource := fs.String("resource", "", "resource name")
	ttl := fs.String("ttl", "300", "TTL in seconds")
	notes := fs.String("notes", "", "notes")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	act := *action
	if len(fs.Args()) > 0 {
		act = fs.Args()[0]
	}
	args := map[string]any{
		"action":      act,
		"agent_id":    *agentID,
		"from_agent":  *fromAgent,
		"to_agent":    *toAgent,
		"task_id":     *taskID,
		"resource":    *resource,
		"ttl_seconds": *ttl,
		"notes":       *notes,
		"root":        *root,
	}
	runMCPTool("kern_agent_coordination", args)
}

func runAgentRoleRBAC(rest []string) {
	fs := flag.NewFlagSet("agent-role-rbac", flag.ContinueOnError)
	action := fs.String("action", "evaluate", "action: evaluate, roles, assign, check")
	agentID := fs.String("agent", "", "agent id")
	role := fs.String("role", "", "role name")
	tool := fs.String("tool", "", "tool name")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	act := *action
	if len(fs.Args()) > 0 {
		act = fs.Args()[0]
	}
	args := map[string]any{
		"action":   act,
		"agent_id": *agentID,
		"role":     *role,
		"tool":     *tool,
		"root":     *root,
	}
	runMCPTool("kern_agent_role_rbac", args)
}

func runStream(rest []string) {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	action := fs.String("action", "status", "action: status, chunk, channels, emit")
	channel := fs.String("channel", "", "channel name")
	payload := fs.String("payload", "", "payload string")
	chunkSize := fs.String("chunk-size", "1000", "chunk size")
	progressToken := fs.String("progress-token", "", "progress token")
	percent := fs.String("percent", "", "percent")
	message := fs.String("message", "", "message")
	_ = fs.Parse(rest)

	act := *action
	if len(fs.Args()) > 0 {
		act = fs.Args()[0]
	}
	args := map[string]any{
		"action":         act,
		"channel":        *channel,
		"payload":        *payload,
		"chunk_size":     *chunkSize,
		"progress_token": *progressToken,
		"percent":        *percent,
		"message":        *message,
	}
	runMCPTool("kern_stream", args)
}

func runAstTransform(rest []string) {
	fs := flag.NewFlagSet("ast-transform", flag.ContinueOnError)
	action := fs.String("action", "implement_interface", "action: implement_interface, add_field, add_method")
	file := fs.String("file", "", "target file path")
	target := fs.String("target", "", "target symbol/struct name")
	iface := fs.String("interface", "", "interface to implement")
	field := fs.String("field", "", "field name")
	fieldType := fs.String("field-type", "", "field type")
	tag := fs.String("tag", "", "struct field tag")
	sig := fs.String("sig", "", "method signature")
	body := fs.String("body", "", "method body")
	apply := fs.Bool("apply", false, "apply edits to file")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	act := *action
	if len(fs.Args()) > 0 {
		act = fs.Args()[0]
	}

	args := map[string]any{
		"action":           act,
		"file":             *file,
		"target_symbol":    *target,
		"interface_name":   *iface,
		"field_name":       *field,
		"field_type":       *fieldType,
		"field_tag":        *tag,
		"method_signature": *sig,
		"method_body":      *body,
		"apply":            *apply,
		"root":             *root,
	}
	runMCPTool("kern_ast_transform", args)
}

func runSemanticMerge(rest []string) {
	fs := flag.NewFlagSet("semantic-merge", flag.ContinueOnError)
	file := fs.String("file", "", "target file path")
	base := fs.String("base", "", "base version code or file path")
	local := fs.String("local", "", "local version code or file path")
	remote := fs.String("remote", "", "remote version code or file path")
	apply := fs.Bool("apply", false, "apply clean merge to file")
	jsonOut := fs.Bool("json", false, "output JSON format")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	format := "text"
	if *jsonOut {
		format = "json"
	}

	args := map[string]any{
		"file":   *file,
		"base":   *base,
		"local":  *local,
		"remote": *remote,
		"apply":  *apply,
		"format": format,
		"root":   *root,
	}
	runMCPTool("kern_semantic_merge", args)
}

func runSynthesizeTest(rest []string) {
	fs := flag.NewFlagSet("synthesize-test", flag.ContinueOnError)
	target := fs.String("target", "", "target function or method name")
	file := fs.String("file", "", "target file path")
	autoGap := fs.Bool("auto-gap", false, "auto-select top untested hotspot")
	apply := fs.Bool("apply", false, "write synthesized test to test file")
	jsonOut := fs.Bool("json", false, "output JSON format")
	root := fs.String("root", ".", "project root")
	_ = fs.Parse(rest)

	tgt := *target
	if tgt == "" && len(fs.Args()) > 0 {
		tgt = fs.Args()[0]
	}

	format := "text"
	if *jsonOut {
		format = "json"
	}

	args := map[string]any{
		"target":   tgt,
		"file":     *file,
		"auto_gap": *autoGap,
		"apply":    *apply,
		"format":   format,
		"root":     *root,
	}
	runMCPTool("kern_synthesize_test", args)
}
