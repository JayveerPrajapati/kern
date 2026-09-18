// Package lspbridge implements a zero-weight Language Server Protocol (LSP)
// client bridge. It connects over stdio to local language servers (such as
// gopls, pyright, vtsls, rust-analyzer, clangd, jdtls, etc.) to query exact
// compiler-grade type information, hover documentation, cross-file definitions,
// and references without bundling external compiler runtimes into kern.
package lspbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LanguageServerDef maps a language to candidate server commands.
type LanguageServerDef struct {
	Language string
	Commands [][]string
}

// DefaultServerRegistry maps file extensions and languages to known LSP server binaries.
var DefaultServerRegistry = map[string]LanguageServerDef{
	"go": {
		Language: "go",
		Commands: [][]string{{"gopls"}},
	},
	"python": {
		Language: "python",
		Commands: [][]string{
			{"pyright-langserver", "--stdio"},
			{"pylsp"},
			{"pyright"},
			{"jedi-language-server"},
		},
	},
	"typescript": {
		Language: "typescript",
		Commands: [][]string{
			{"vtsls", "--stdio"},
			{"typescript-language-server", "--stdio"},
		},
	},
	"javascript": {
		Language: "javascript",
		Commands: [][]string{
			{"vtsls", "--stdio"},
			{"typescript-language-server", "--stdio"},
		},
	},
	"rust": {
		Language: "rust",
		Commands: [][]string{{"rust-analyzer"}},
	},
	"c": {
		Language: "c",
		Commands: [][]string{{"clangd"}},
	},
	"cpp": {
		Language: "cpp",
		Commands: [][]string{{"clangd"}},
	},
	"java": {
		Language: "java",
		Commands: [][]string{{"jdtls"}},
	},
	"csharp": {
		Language: "csharp",
		Commands: [][]string{{"csharp-ls"}, {"OmniSharp"}},
	},
	"ruby": {
		Language: "ruby",
		Commands: [][]string{{"solargraph", "stdio"}},
	},
	"php": {
		Language: "php",
		Commands: [][]string{{"intelephense", "--stdio"}},
	},
	"json": {
		Language: "json",
		Commands: [][]string{{"vscode-json-language-server", "--stdio"}},
	},
	"yaml": {
		Language: "yaml",
		Commands: [][]string{{"yaml-language-server", "--stdio"}},
	},
	"bash": {
		Language: "bash",
		Commands: [][]string{{"bash-language-server", "start"}},
	},
}

// ExtToLanguage maps file extensions to language names.
var ExtToLanguage = map[string]string{
	".go":   "go",
	".py":   "python",
	".pyw":  "python",
	".ts":   "typescript",
	".tsx":  "typescript",
	".js":   "javascript",
	".jsx":  "javascript",
	".mjs":  "javascript",
	".cjs":  "javascript",
	".rs":   "rust",
	".c":    "c",
	".h":    "c",
	".cpp":  "cpp",
	".cxx":  "cpp",
	".cc":   "cpp",
	".hpp":  "cpp",
	".java": "java",
	".cs":   "csharp",
	".rb":   "ruby",
	".php":  "php",
	".json": "json",
	".yaml": "yaml",
	".yml":  "yaml",
	".sh":   "bash",
	".bash": "bash",
}

// DetectServer returns the best available language server command for the given file or language.
func DetectServer(fileOrLang string) (cmd []string, lang string, found bool) {
	ext := strings.ToLower(filepath.Ext(fileOrLang))
	if l, ok := ExtToLanguage[ext]; ok {
		lang = l
	} else if _, ok := DefaultServerRegistry[strings.ToLower(fileOrLang)]; ok {
		lang = strings.ToLower(fileOrLang)
	} else {
		lang = strings.ToLower(fileOrLang)
	}

	def, exists := DefaultServerRegistry[lang]
	if !exists {
		return nil, lang, false
	}

	for _, c := range def.Commands {
		if len(c) == 0 {
			continue
		}
		if _, err := exec.LookPath(c[0]); err == nil {
			return c, lang, true
		}
	}
	return nil, lang, false
}

// InstalledServers scans PATH and returns a list of detected, ready-to-use language servers.
func InstalledServers() map[string][]string {
	res := make(map[string][]string)
	for lang, def := range DefaultServerRegistry {
		for _, c := range def.Commands {
			if len(c) == 0 {
				continue
			}
			if p, err := exec.LookPath(c[0]); err == nil {
				res[lang] = append(res[lang], fmt.Sprintf("%s (%s)", strings.Join(c, " "), p))
			}
		}
	}
	return res
}

// Position is an LSP 0-based position.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is an LSP 0-based range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location represents a code location in a file.
type Location struct {
	URI   string `json:"uri"`
	File  string `json:"file,omitempty"`
	Line  int    `json:"line"` // 1-based for human convenience
	Col   int    `json:"col"`  // 1-based for human convenience
	Range Range  `json:"range"`
}

// HoverInfo holds type signature and documentation extracted from LSP hover.
type HoverInfo struct {
	Signature string `json:"signature,omitempty"`
	Doc       string `json:"doc,omitempty"`
	Raw       string `json:"raw"`
}

// SymbolInfo describes a document symbol.
type SymbolInfo struct {
	Name           string       `json:"name"`
	Kind           string       `json:"kind"`
	KindCode       int          `json:"kind_code"`
	Detail         string       `json:"detail,omitempty"`
	Range          Range        `json:"range"`
	SelectionRange Range        `json:"selection_range"`
	Children       []SymbolInfo `json:"children,omitempty"`
}

// QueryRequest specifies an LSP bridge query.
type QueryRequest struct {
	Root      string   `json:"root"`
	File      string   `json:"file"`
	Line      int      `json:"line"`                 // 1-based
	Column    int      `json:"column"`               // 1-based (optional, default 1)
	Action    string   `json:"action"`               // "definition", "hover", "references", "symbols", "servers"
	ServerCmd []string `json:"server_cmd,omitempty"` // optional custom command
}

// QueryResult encapsulates the query response.
type QueryResult struct {
	Action         string       `json:"action"`
	File           string       `json:"file,omitempty"`
	Language       string       `json:"language,omitempty"`
	LanguageServer string       `json:"language_server,omitempty"`
	Locations      []Location   `json:"locations,omitempty"`
	Hover          *HoverInfo   `json:"hover,omitempty"`
	Symbols        []SymbolInfo `json:"symbols,omitempty"`
	Available      any          `json:"available,omitempty"`
	Warning        string       `json:"warning,omitempty"`
	Error          string       `json:"error,omitempty"`
}

// Client manages a long-lived connection to a local language server process over stdio.
type Client struct {
	root      string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	reader    *bufio.Reader
	writeMu   sync.Mutex
	reqID     atomic.Int64
	pendingMu sync.Mutex
	pending   map[int64]chan rpcResponse
	openedMu  sync.Mutex
	opened    map[string]int // file -> version
	closed    atomic.Bool
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// StartClient spawns the language server process and completes initialize handshake.
func StartClient(ctx context.Context, root string, serverCmd []string) (*Client, error) {
	if len(serverCmd) == 0 {
		return nil, errors.New("empty server command")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}

	cmd := exec.CommandContext(ctx, serverCmd[0], serverCmd[1:]...)
	cmd.Dir = absRoot

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start %v: %w", serverCmd, err)
	}

	c := &Client{
		root:    absRoot,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		reader:  bufio.NewReader(stdout),
		pending: make(map[int64]chan rpcResponse),
		opened:  make(map[string]int),
	}

	go c.listen()

	// Perform initialize handshake
	initParams := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   pathToURI(absRoot),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"hover": map[string]any{
					"contentFormat": []string{"markdown", "plaintext"},
				},
				"definition": map[string]any{
					"linkSupport": true,
				},
				"references":     map[string]any{},
				"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true},
			},
		},
		"workspaceFolders": []map[string]any{
			{
				"uri":  pathToURI(absRoot),
				"name": filepath.Base(absRoot),
			},
		},
	}

	var initResp json.RawMessage
	if err := c.Call(ctx, "initialize", initParams, &initResp); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("lsp initialize: %w", err)
	}

	_ = c.Notify("initialized", map[string]any{})
	return c, nil
}

// Call sends a JSON-RPC request and unmarshals the result.
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	if c.closed.Load() {
		return errors.New("lsp client closed")
	}

	id := c.reqID.Add(1)
	respCh := make(chan rpcResponse, 1)

	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	if err := c.send(req); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp, ok := <-respCh:
		if !ok {
			return errors.New("lsp server connection terminated")
		}
		if resp.Error != nil {
			return fmt.Errorf("lsp error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if result != nil && len(resp.Result) > 0 && string(resp.Result) != "null" {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

// Notify sends a JSON-RPC notification.
func (c *Client) Notify(method string, params any) error {
	if c.closed.Load() {
		return errors.New("lsp client closed")
	}
	notif := rpcNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return c.send(notif)
}

func (c *Client) send(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	if _, err := c.stdin.Write(data); err != nil {
		return err
	}
	return nil
}

func (c *Client) listen() {
	defer func() {
		c.pendingMu.Lock()
		for _, ch := range c.pending {
			close(ch)
		}
		c.pending = make(map[int64]chan rpcResponse)
		c.pendingMu.Unlock()
	}()

	for {
		msg, err := readLSPMessage(c.reader)
		if err != nil {
			return
		}

		var resp rpcResponse
		if err := json.Unmarshal(msg, &resp); err == nil && resp.ID != 0 {
			c.pendingMu.Lock()
			ch, ok := c.pending[resp.ID]
			c.pendingMu.Unlock()
			if ok {
				ch <- resp
			}
		}
	}
}

func readLSPMessage(r *bufio.Reader) ([]byte, error) {
	var length int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "content-length") {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				length = n
			}
		}
	}
	if length <= 0 {
		return nil, errors.New("missing or invalid Content-Length header")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// EnsureDidOpen ensures the file is synchronized with the language server.
func (c *Client) EnsureDidOpen(filePath string) error {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}

	c.openedMu.Lock()
	defer c.openedMu.Unlock()

	if _, ok := c.opened[abs]; ok {
		return nil
	}

	content, err := os.ReadFile(abs)
	if err != nil {
		return err
	}

	ext := strings.ToLower(filepath.Ext(abs))
	langID := ExtToLanguage[ext]
	if langID == "" {
		langID = "plaintext"
	}

	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        pathToURI(abs),
			"languageId": langID,
			"version":    1,
			"text":       string(content),
		},
	}

	if err := c.Notify("textDocument/didOpen", params); err != nil {
		return err
	}

	c.opened[abs] = 1
	return nil
}

// Definition queries textDocument/definition.
func (c *Client) Definition(ctx context.Context, file string, line, col int) ([]Location, error) {
	if err := c.EnsureDidOpen(file); err != nil {
		return nil, err
	}

	abs, _ := filepath.Abs(file)
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": pathToURI(abs),
		},
		"position": Position{
			Line:      max(0, line-1),
			Character: max(0, col-1),
		},
	}

	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/definition", params, &raw); err != nil {
		return nil, err
	}

	return parseLocations(raw, c.root)
}

// Hover queries textDocument/hover.
func (c *Client) Hover(ctx context.Context, file string, line, col int) (*HoverInfo, error) {
	if err := c.EnsureDidOpen(file); err != nil {
		return nil, err
	}

	abs, _ := filepath.Abs(file)
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": pathToURI(abs),
		},
		"position": Position{
			Line:      max(0, line-1),
			Character: max(0, col-1),
		},
	}

	var raw struct {
		Contents any `json:"contents"`
	}
	if err := c.Call(ctx, "textDocument/hover", params, &raw); err != nil {
		return nil, err
	}

	if raw.Contents == nil {
		return nil, nil
	}

	return parseHover(raw.Contents), nil
}

// References queries textDocument/references.
func (c *Client) References(ctx context.Context, file string, line, col int) ([]Location, error) {
	if err := c.EnsureDidOpen(file); err != nil {
		return nil, err
	}

	abs, _ := filepath.Abs(file)
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": pathToURI(abs),
		},
		"position": Position{
			Line:      max(0, line-1),
			Character: max(0, col-1),
		},
		"context": map[string]any{
			"includeDeclaration": true,
		},
	}

	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/references", params, &raw); err != nil {
		return nil, err
	}

	return parseLocations(raw, c.root)
}

// DocumentSymbols queries textDocument/documentSymbol.
func (c *Client) DocumentSymbols(ctx context.Context, file string) ([]SymbolInfo, error) {
	if err := c.EnsureDidOpen(file); err != nil {
		return nil, err
	}

	abs, _ := filepath.Abs(file)
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": pathToURI(abs),
		},
	}

	var raw json.RawMessage
	if err := c.Call(ctx, "textDocument/documentSymbol", params, &raw); err != nil {
		return nil, err
	}

	return parseDocumentSymbols(raw)
}

// Close gracefully shuts down the language server.
func (c *Client) Close() error {
	if c.closed.Swap(true) {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = c.Call(ctx, "shutdown", nil, nil)
	_ = c.Notify("exit", nil)

	_ = c.stdin.Close()
	_ = c.stdout.Close()

	if c.cmd != nil && c.cmd.Process != nil {
		done := make(chan error, 1)
		go func() { done <- c.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			_ = c.cmd.Process.Kill()
		}
	}
	return nil
}

// Pool maintains active LSP clients per (root, command).
type Pool struct {
	mu      sync.Mutex
	clients map[string]*Client
}

// DefaultPool is the process-wide LSP client pool.
var DefaultPool = &Pool{
	clients: make(map[string]*Client),
}

// GetOrStart retrieves an existing client or starts a new one.
func (p *Pool) GetOrStart(ctx context.Context, root string, cmd []string) (*Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := fmt.Sprintf("%s::%s", root, strings.Join(cmd, " "))
	if c, ok := p.clients[key]; ok && !c.closed.Load() {
		return c, nil
	}

	c, err := StartClient(ctx, root, cmd)
	if err != nil {
		return nil, err
	}
	p.clients[key] = c
	return c, nil
}

// Query executes a high-level LSP query using auto-detected or explicit language servers.
func Query(ctx context.Context, req QueryRequest) (*QueryResult, error) {
	if req.Root == "" {
		req.Root = "."
	}
	if req.Column <= 0 {
		req.Column = 1
	}
	if req.Line <= 0 {
		req.Line = 1
	}

	action := strings.ToLower(req.Action)
	if action == "" {
		action = "definition"
	}

	if action == "servers" {
		return &QueryResult{
			Action:    "servers",
			Available: InstalledServers(),
		}, nil
	}

	var cmd []string
	var lang string
	if len(req.ServerCmd) > 0 {
		cmd = req.ServerCmd
		lang = "custom"
	} else {
		var found bool
		cmd, lang, found = DetectServer(req.File)
		if !found {
			return &QueryResult{
				Action:   action,
				File:     req.File,
				Language: lang,
				Warning:  fmt.Sprintf("No language server installed for %s (language: %s). Install language server (e.g. gopls, pyright, vtsls, rust-analyzer) for compiler-precision queries.", req.File, lang),
			}, nil
		}
	}

	client, err := DefaultPool.GetOrStart(ctx, req.Root, cmd)
	if err != nil {
		return &QueryResult{
			Action:         action,
			File:           req.File,
			Language:       lang,
			LanguageServer: strings.Join(cmd, " "),
			Error:          fmt.Sprintf("Failed to launch language server %v: %v", cmd, err),
		}, nil
	}

	res := &QueryResult{
		Action:         action,
		File:           req.File,
		Language:       lang,
		LanguageServer: strings.Join(cmd, " "),
	}

	switch action {
	case "definition":
		locs, err := client.Definition(ctx, req.File, req.Line, req.Column)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Locations = locs
		}
	case "hover":
		h, err := client.Hover(ctx, req.File, req.Line, req.Column)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Hover = h
		}
	case "references":
		locs, err := client.References(ctx, req.File, req.Line, req.Column)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Locations = locs
		}
	case "symbols", "document_symbols":
		syms, err := client.DocumentSymbols(ctx, req.File)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Symbols = syms
		}
	default:
		res.Error = fmt.Sprintf("unsupported LSP action %q (choose from: definition, hover, references, symbols, servers)", action)
	}

	return res, nil
}

// Helpers

func pathToURI(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

func uriToPath(uriStr string) string {
	u, err := url.Parse(uriStr)
	if err != nil || u.Scheme != "file" {
		return uriStr
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
		p = filepath.FromSlash(p)
	}
	return p
}

func parseLocations(raw json.RawMessage, root string) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	// Could be Location, []Location, or LocationLink[]
	var single struct {
		URI   string `json:"uri"`
		Range Range  `json:"range"`
	}
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		p := uriToPath(single.URI)
		rel, _ := filepath.Rel(root, p)
		if rel == "" || strings.HasPrefix(rel, "..") {
			rel = p
		}
		return []Location{{
			URI:   single.URI,
			File:  rel,
			Line:  single.Range.Start.Line + 1,
			Col:   single.Range.Start.Character + 1,
			Range: single.Range,
		}}, nil
	}

	var multi []struct {
		URI            string `json:"uri"`
		Range          Range  `json:"range"`
		TargetURI      string `json:"targetUri"`
		TargetRange    Range  `json:"targetRange"`
		TargetSelRange Range  `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(raw, &multi); err == nil {
		var out []Location
		for _, m := range multi {
			uri := m.URI
			rng := m.Range
			if uri == "" && m.TargetURI != "" {
				uri = m.TargetURI
				rng = m.TargetSelRange
			}
			p := uriToPath(uri)
			rel, _ := filepath.Rel(root, p)
			if rel == "" || strings.HasPrefix(rel, "..") {
				rel = p
			}
			out = append(out, Location{
				URI:   uri,
				File:  rel,
				Line:  rng.Start.Line + 1,
				Col:   rng.Start.Character + 1,
				Range: rng,
			})
		}
		return out, nil
	}

	return nil, nil
}

func parseHover(contents any) *HoverInfo {
	info := &HoverInfo{}
	switch v := contents.(type) {
	case string:
		info.Raw = v
		info.Doc = v
	case map[string]any:
		if val, ok := v["value"].(string); ok {
			info.Raw = val
			info.Doc = val
		}
	case []any:
		var parts []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			} else if m, ok := item.(map[string]any); ok {
				if val, ok := m["value"].(string); ok {
					parts = append(parts, val)
				}
			}
		}
		info.Raw = strings.Join(parts, "\n\n")
		info.Doc = info.Raw
	}

	// Extract code snippet if present
	if strings.HasPrefix(info.Raw, "```") {
		lines := strings.Split(info.Raw, "\n")
		if len(lines) > 2 {
			end := -1
			for i := 1; i < len(lines); i++ {
				if strings.HasPrefix(lines[i], "```") {
					end = i
					break
				}
			}
			if end > 0 {
				info.Signature = strings.Join(lines[1:end], "\n")
				info.Doc = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
			}
		}
	}
	return info
}

var symbolKindNames = map[int]string{
	1:  "file",
	2:  "module",
	3:  "namespace",
	4:  "package",
	5:  "class",
	6:  "method",
	7:  "property",
	8:  "field",
	9:  "constructor",
	10: "enum",
	11: "interface",
	12: "function",
	13: "variable",
	14: "constant",
	15: "string",
	16: "number",
	17: "boolean",
	18: "array",
	19: "object",
	20: "key",
	21: "null",
	22: "enum-member",
	23: "struct",
	24: "event",
	25: "operator",
	26: "type-parameter",
}

func parseDocumentSymbols(raw json.RawMessage) ([]SymbolInfo, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	type rawDocSymbol struct {
		Name           string          `json:"name"`
		Detail         string          `json:"detail,omitempty"`
		Kind           int             `json:"kind"`
		Range          Range           `json:"range"`
		SelectionRange Range           `json:"selectionRange"`
		Children       []*rawDocSymbol `json:"children,omitempty"`
	}

	var rawList []*rawDocSymbol
	if err := json.Unmarshal(raw, &rawList); err == nil {
		var convert func(s *rawDocSymbol) SymbolInfo
		convert = func(s *rawDocSymbol) SymbolInfo {
			kindName := symbolKindNames[s.Kind]
			if kindName == "" {
				kindName = "symbol"
			}
			var children []SymbolInfo
			for _, c := range s.Children {
				if c != nil {
					children = append(children, convert(c))
				}
			}
			return SymbolInfo{
				Name:           s.Name,
				Kind:           kindName,
				KindCode:       s.Kind,
				Detail:         s.Detail,
				Range:          s.Range,
				SelectionRange: s.SelectionRange,
				Children:       children,
			}
		}
		var out []SymbolInfo
		for _, s := range rawList {
			if s != nil {
				out = append(out, convert(s))
			}
		}
		return out, nil
	}
	return nil, nil
}
