// Package mcp implements a minimal Model Context Protocol server over stdio.
// It is deliberately dependency-free so the binary stays offline and static.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

const (
	protocolVersion = "2025-06-18"
	serverName      = "kern"
	serverVersion   = "0.1.0"
)

// Tool is an MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func schema(props map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

var tools = []Tool{
	{
		Name:        "kern_optimize_prompt",
		Description: "Compress and clean a raw prompt before sending it to an LLM. Returns the optimized prompt plus token savings. Use this to reduce context cost for large or noisy prompts.",
		InputSchema: schema(map[string]any{
			"prompt":     strProp("The raw prompt text to optimize"),
			"attached_log": strProp("Optional noisy log output to compress and attach"),
			"session":    strProp("Optional session identifier for stats tracking"),
			"model":      strProp("Optional model name for cost estimation"),
		}, []string{"prompt"}),
	},
	{
		Name:        "kern_compact_file",
		Description: "Return a compact symbolic summary of a source file (functions, types, line numbers) instead of reading the whole file. Use before reading files in large codebases.",
		InputSchema: schema(map[string]any{
			"path": strProp("Absolute or relative path of the file to summarize"),
		}, []string{"path"}),
	},
	{
		Name:        "kern_project_map",
		Description: "Return a compressed map of a whole project: every source file with its symbols and line counts. Use instead of listing/reading every file in a repo.",
		InputSchema: schema(map[string]any{
			"root":      strProp("Project root directory"),
			"max_files": strProp("Maximum number of files to include (default 500)"),
		}, []string{"root"}),
	},
	{
		Name:        "kern_run_build",
		Description: "Run a build/test command locally and return only the compact result (exit status + errors), not full output. Use for builds, tests, linting to save context.",
		InputSchema: schema(map[string]any{
			"command": strProp("Shell command to run"),
			"dir":     strProp("Working directory for the command"),
		}, []string{"command"}),
	},
	{
		Name:        "kern_optimize_log",
		Description: "Strip noise from log output: keeps errors, warnings, stack traces and build failures, removes timestamps and chatter. Use before pasting logs into context.",
		InputSchema: schema(map[string]any{
			"log": strProp("The log text to compress"),
		}, []string{"log"}),
	},
	{
		Name:        "kern_context_budget",
		Description: "Fit text into a token budget: deduplicate lines, keep the head plus important lines (errors, stack frames), then trim. Use to manage a crowded context window before adding more content.",
		InputSchema: schema(map[string]any{
			"text":       strProp("The text (log output, file dump, conversation) to fit into the budget"),
			"max_tokens": strProp("Maximum tokens the result may use (default 4000)"),
		}, []string{"text"}),
	},
	{
		Name:        "kern_stats",
		Description: "Return before/after token savings and cost estimates from kern optimizations, optionally filtered to today or a session.",
		InputSchema: schema(map[string]any{
			"days":    strProp("Aggregate over the last N days (default 7)"),
			"session": strProp("Filter to a session identifier"),
		}, nil),
	},
	{
		Name:        "kern_ast_search",
		Description: "AST-level symbol search across a Go project. Supports patterns like 'func greet', 'type *User*', 'method *', '*Handler*'. Returns definitions with file:line.",
		InputSchema: schema(map[string]any{
			"pattern": strProp("Symbol pattern. Prefixes: func, method, struct, interface, type, const, var. '*' wildcards supported"),
			"root":    strProp("Project root (defaults to current directory)"),
			"limit":   strProp("Max results (default 50)"),
		}, []string{"pattern"}),
	},
	{
		Name:        "kern_code_graph",
		Description: "Return the call graph neighbourhood of a symbol: its definition, its callers, and what it calls. Use to understand dependencies without reading whole files.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name (e.g. 'greet' or 'User.Login')"),
			"root":   strProp("Project root (defaults to current directory)"),
		}, []string{"symbol"}),
	},
	{
		Name:        "kern_context",
		Description: "Return the minimal relevant source slice for a symbol: its definition source, its callers, and what it calls. Use instead of reading an entire file.",
		InputSchema: schema(map[string]any{
			"symbol": strProp("Symbol name (e.g. 'greet')"),
			"root":   strProp("Project root (defaults to current directory)"),
			"lines":  strProp("Lines of source context around the definition (default 12)"),
		}, []string{"symbol"}),
	},
}

// Server handles MCP requests over a stdio stream.
type Server struct {
	in  *bufio.Scanner
	out io.Writer
}

// NewServer returns a server wired to the given reader/writer.
func NewServer(in io.Reader, out io.Writer) *Server {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 4<<20), 4<<20)
	return &Server{in: sc, out: out}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (s *Server) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.out.Write(append(data, '\n'))
	return err
}

func (s *Server) reply(id json.RawMessage, result any) error {
	return s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) replyError(id json.RawMessage, code int, msg string) error {
	return s.write(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": msg},
	})
}

// Serve runs until the stream ends.
func (s *Server) Serve() error {
	for s.in.Scan() {
		line := s.in.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if err := s.handle(req); err != nil && req.ID != nil {
			_ = s.replyError(req.ID, -32000, err.Error())
		}
	}
	return s.in.Err()
}

func (s *Server) handle(req rpcRequest) error {
	switch req.Method {
	case "initialize":
		return s.reply(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
		})
	case "notifications/initialized":
		return nil
	case "ping":
		return s.reply(req.ID, map[string]any{})
	case "tools/list":
		return s.reply(req.ID, map[string]any{"tools": tools})
	case "tools/call":
		return s.handleToolCall(req.ID, req.Params)
	default:
		if req.Method == "" {
			return nil
		}
		return s.replyError(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Server) handleToolCall(id json.RawMessage, params json.RawMessage) error {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return s.replyError(id, -32602, "invalid params")
	}
	text, err := runTool(p.Name, p.Arguments)
	if err != nil {
		return s.replyError(id, -32000, err.Error())
	}
	result := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": false,
	}
	return s.reply(id, result)
}

func argString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func runTool(name string, args map[string]any) (string, error) {
	switch name {
	case "kern_optimize_prompt":
		prompt := argString(args, "prompt")
		if prompt == "" {
			return "", fmt.Errorf("prompt is required")
		}
		res, err := optimize.Prompt(prompt, argString(args, "attached_log"), optimize.Options{
			Session: argString(args, "session"),
			Model:   argString(args, "model"),
		})
		if err != nil {
			return "", err
		}
		return renderOptimize("optimized prompt", res), nil

	case "kern_compact_file":
		path := argString(args, "path")
		if path == "" {
			return "", fmt.Errorf("path is required")
		}
		abs := path
		if !strings.HasPrefix(abs, "/") {
			cwd, _ := os.Getwd()
			abs = cwd + "/" + abs
		}
		content, err := code.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", path, err)
		}
		sum := code.Summarize(abs, content, 200)
		return sum.Render(), nil

	case "kern_project_map":
		root := argString(args, "root")
		if root == "" {
			cwd, _ := os.Getwd()
			root = cwd
		}
		p, err := code.BuildProject(root, 500, 200)
		if err != nil {
			return "", err
		}
		return p.Render(), nil

	case "kern_run_build":
		cmd := argString(args, "command")
		if cmd == "" {
			return "", fmt.Errorf("command is required")
		}
		res, err := optimize.RunBuild(cmd, argString(args, "dir"), optimize.Options{})
		if err != nil {
			return "", err
		}
		return res.Output, nil

	case "kern_optimize_log":
		log := argString(args, "log")
		if log == "" {
			return "", fmt.Errorf("log is required")
		}
		res, err := optimize.Log(log, optimize.Options{})
		if err != nil {
			return "", err
		}
		return renderOptimize("optimized log", res), nil

	case "kern_stats":
		return renderStats(argString(args, "days"), argString(args, "session"))

	case "kern_context_budget":
		text := argString(args, "text")
		if text == "" {
			return "", fmt.Errorf("text is required")
		}
		maxTokens := 4000
		if v := argString(args, "max_tokens"); v != "" {
			fmt.Sscanf(v, "%d", &maxTokens)
		}
		out := budget.Fit(text, maxTokens)
		before := tokenize.Count(text)
		after := tokenize.Count(out)
		return fmt.Sprintf("%d -> %d tokens (saved %d, %.1f%%)\n\n%s", before, after, before-after, pct(before, after), out), nil

	case "kern_ast_search":
		pattern := argString(args, "pattern")
		if pattern == "" {
			return "", fmt.Errorf("pattern is required")
		}
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		limit := 50
		if v := argString(args, "limit"); v != "" {
			fmt.Sscanf(v, "%d", &limit)
		}
		matches := ix.Search(pattern, limit)
		if len(matches) == 0 {
			return "no symbols matched: " + pattern, nil
		}
		var b strings.Builder
		for _, m := range matches {
			b.WriteString(m.Kind)
			b.WriteString(" ")
			b.WriteString(m.FullName())
			b.WriteString(" ")
			b.WriteString(m.File)
			b.WriteString(":")
			b.WriteString(itoa(m.Line))
			b.WriteString("\n")
		}
		return strings.TrimSuffix(b.String(), "\n"), nil

	case "kern_code_graph":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		return ix.Graph(symbol), nil

	case "kern_context":
		symbol := argString(args, "symbol")
		if symbol == "" {
			return "", fmt.Errorf("symbol is required")
		}
		ix, err := loadOrBuildIndex(argString(args, "root"))
		if err != nil {
			return "", err
		}
		lines := 12
		if v := argString(args, "lines"); v != "" {
			fmt.Sscanf(v, "%d", &lines)
		}
		ctx := ix.Context(symbol, lines)
		if ctx == "" {
			return "no symbol found: " + symbol, nil
		}
		return ctx, nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func pct(before, after int) float64 {
	if before <= 0 {
		return 0
	}
	return float64(before-after) / float64(before) * 100
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// loadOrBuildIndex loads the persisted index for root, or builds + saves it.
func loadOrBuildIndex(root string) (*index.Index, error) {
	if root == "" {
		cwd, _ := os.Getwd()
		root = cwd
	}
	if ix, err := index.Load(root); err == nil && ix != nil {
		return ix, nil
	}
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	_ = ix.Save()
	return ix, nil
}

func renderOptimize(label string, res optimize.Result) string {
	return fmt.Sprintf("%s (tokens: %d -> %d, saved %d (%.1f%%)):\n%s",
		label, res.BeforeTokens, res.AfterTokens, res.SavedTokens, res.SavedPercent, res.Output)
}

func renderStats(daysStr, session string) (string, error) {
	days := 7
	if daysStr != "" {
		if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil {
			return "", fmt.Errorf("invalid days: %s", daysStr)
		}
	}
	rec, err := stats.NewRecorder()
	if err != nil {
		return "", err
	}
	sum, err := rec.Summarize(days, session)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("operations=%d before=%d after=%d saved=%d (%.1f%%) cost_saved=$%.4f",
		sum.Operations, sum.BeforeTotal, sum.AfterTotal, sum.SavedTotal, sum.SavedPct, sum.CostSaved), nil
}

// ensureRecorder wires the shared stats recorder used by optimize operations.
func ensureRecorder() error {
	rec, err := stats.NewRecorder()
	if err != nil {
		return err
	}
	optimize.Recorder = rec
	return nil
}
