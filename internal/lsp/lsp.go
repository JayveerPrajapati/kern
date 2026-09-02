// Package lsp implements a minimal Language Server Protocol server over
// stdio for kern (G-8). It speaks Content-Length framed JSON-RPC 2.0 and is
// powered by kern's prebuilt symbol index: textDocument/hover,
// textDocument/definition and textDocument/references resolve identifiers
// against the index that `kern index` (or the CLI load-or-build path) writes
// to <root>/.kern/index.json. The index is loaded or built exactly once, at
// initialize, and reused for the whole session — never rebuilt per request.
//
// v1 limitations (deliberate, documented):
//   - Line-level precision only. The index stores 1-based declaration lines
//     but no columns, so definition/hover ranges start at character 0 and
//     references point at the caller's declaration, not the exact call site.
//     Index lines are 1-based; LSP wants 0-based, so lines are converted.
//   - Positions are UTF-16 code units (as the LSP spec requires); they are
//     converted to UTF-8 byte offsets for identifier extraction, so the
//     conversion is exact rather than an approximation.
//   - Identifiers are ASCII [A-Za-z0-9_], matching the index's symbol names.
//   - The index stores signatures (params/returns) but no doc comments, so
//     hover shows the signature, not a doc snippet.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// errExit is returned internally when the `exit` notification is received so
// the serve loop can close cleanly (Serve returns nil, mirroring the MCP
// stdio server's "clean drain → exit 0" contract).
var errExit = errors.New("lsp: exit notification received")

// Server is a minimal LSP server over a single stdio connection. It is not
// safe for concurrent use; requests are dispatched serially.
type Server struct {
	root string
	in   io.Reader
	out  io.Writer

	// mu serializes writes to out so responses never interleave.
	mu sync.Mutex
	// ix is the session's symbol index, loaded once at initialize and reused
	// for every textDocument/* request. nil means initialize failed to load
	// or build the index; textDocument requests then answer null.
	ix *index.Index
}

// Serve runs an LSP server on in/out, serving the project rooted at root
// (used as the fallback when the client's initialize params carry no
// rootUri/workspaceFolders). It blocks until the client sends `exit`, the
// underlying reader hits EOF, or ctx is canceled. On a signal, the reader is
// closed to unblock the read loop and Serve returns nil (clean exit 0),
// mirroring the graceful SIGINT/SIGTERM pattern of cmd/kern/cmd_mcp.go.
//
// Never writes anything except framed JSON-RPC responses to out; diagnostics
// go to stderr or are discarded.
func Serve(ctx context.Context, root string, in io.Reader, out io.Writer) error {
	s := &Server{root: root, in: in, out: out}
	type result struct{ err error }
	done := make(chan result, 1)
	go func() { done <- result{err: s.loop()} }()
	select {
	case r := <-done:
		return r.err
	case <-ctx.Done():
		// Unblock the read loop: ServeStdio closes os.Stdin on signal and
		// waits for the serve goroutine to finish. Do the same here when the
		// underlying reader is closable (os.Stdin is).
		if c, ok := s.in.(io.Closer); ok {
			_ = c.Close()
		}
		<-done
		return nil
	}
}

// loop reads and dispatches frames until exit/EOF/ctx. ctx is only consulted
// between messages; a blocked read is unblocked by Serve closing the reader.
func (s *Server) loop() error {
	r := bufio.NewReader(s.in)
	for {
		msg, err := readMessage(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := s.handle(msg); err != nil {
			if errors.Is(err, errExit) {
				return nil
			}
			return err
		}
	}
}

// readMessage reads one Content-Length framed message from r. Header names
// are matched case-insensitively ("Content-Length" / "content-length"); any
// other headers are ignored. A clean EOF before any byte of a new message
// returns io.EOF.
func readMessage(r *bufio.Reader) ([]byte, error) {
	var length int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && length == 0 {
				return nil, io.EOF
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "content-length") {
			continue
		}
		if n, err := parseLength(strings.TrimSpace(val)); err == nil {
			length = n
		}
	}
	if length <= 0 {
		return nil, fmt.Errorf("lsp: missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// parseLength parses a Content-Length header value, rejecting negatives,
// overflow and non-numeric junk.
func parseLength(val string) (int, error) {
	if val == "" {
		return 0, fmt.Errorf("lsp: empty Content-Length")
	}
	n := 0
	for _, c := range val {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("lsp: invalid Content-Length %q", val)
		}
		n = n*10 + int(c-'0')
		if n < 0 { // overflow
			return 0, fmt.Errorf("lsp: Content-Length %q overflows", val)
		}
	}
	return n, nil
}

// rpcMessage is the JSON-RPC 2.0 envelope received from the client. A request
// carries a non-null id; a notification carries none.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isRequest reports whether the message is a request (non-null id) rather
// than a notification. Only requests get responses.
func (m rpcMessage) isRequest() bool {
	return len(m.ID) > 0 && string(m.ID) != "null"
}

// handle parses and dispatches one message, writing the response when one is
// due. Errors returned are write/protocol errors, except errExit.
func (s *Server) handle(msg []byte) error {
	var m rpcMessage
	if err := json.Unmarshal(msg, &m); err != nil {
		// A malformed frame is a parse error (-32700); respond and keep going.
		return s.respondError(json.RawMessage("null"), -32700, "parse error: "+err.Error())
	}
	switch {
	case m.Method == "initialize":
		return s.handleInitialize(m)
	case m.Method == "initialized":
		return nil // notification: no-op
	case m.Method == "shutdown":
		if !m.isRequest() {
			return nil
		}
		return s.respond(m.ID, nil) // result null
	case m.Method == "exit":
		return errExit
	case m.Method == "":
		// A body that parses as JSON but is not a valid request/notification
		// (no method) is an invalid request when it carries an id.
		if !m.isRequest() {
			return nil
		}
		return s.respondError(m.ID, -32600, "invalid request")
	case strings.HasPrefix(m.Method, "$/"):
		return nil // $/cancelRequest, $/setTrace, ... ignored silently
	case m.Method == "textDocument/hover":
		return s.handleHover(m)
	case m.Method == "textDocument/definition":
		return s.handleDefinition(m)
	case m.Method == "textDocument/references":
		return s.handleReferences(m)
	default:
		if !m.isRequest() {
			return nil // unknown notification: ignore silently
		}
		return s.respondError(m.ID, -32601, "method not found: "+m.Method)
	}
}

// ---- initialize ----

// initializeParams is the subset of LSP InitializeParams kern cares about.
type initializeParams struct {
	RootURI          *string `json:"rootUri"`
	RootPath         *string `json:"rootPath"` // legacy pre-3.17 field
	WorkspaceFolders []struct {
		URI string `json:"uri"`
	} `json:"workspaceFolders"`
	Capabilities json.RawMessage `json:"capabilities"`
}

// initializeResult is the LSP InitializeResult: capabilities plus server info.
type initializeResult struct {
	Capabilities capabilities `json:"capabilities"`
	ServerInfo   serverInfo   `json:"serverInfo"`
}

type capabilities struct {
	TextDocumentSync   int  `json:"textDocumentSync"`   // 0 = none (read from disk)
	HoverProvider      bool `json:"hoverProvider"`      // bool form: simple provider
	DefinitionProvider bool `json:"definitionProvider"` // bool form: simple provider
	ReferencesProvider bool `json:"referencesProvider"` // bool form: simple provider
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// handleInitialize resolves the project root from the client params, loads or
// builds the index once, and answers with the server capabilities.
func (s *Server) handleInitialize(m rpcMessage) error {
	var p initializeParams
	if len(m.Params) > 0 {
		_ = json.Unmarshal(m.Params, &p)
	}
	if root := s.resolveRoot(p); root != "" {
		s.root = root
	}
	ix, err := index.LoadOrBuild(s.root)
	if err != nil {
		// Degrade gracefully: answer capabilities anyway, serve nulls for
		// textDocument/* until the index is available. Never crash the server.
		fmt.Fprintf(os.Stderr, "kern-lsp: index for %s unavailable: %v\n", s.root, err)
	} else {
		s.ix = ix
	}
	if !m.isRequest() {
		return nil
	}
	return s.respond(m.ID, initializeResult{
		Capabilities: capabilities{
			TextDocumentSync:   0,
			HoverProvider:      true,
			DefinitionProvider: true,
			ReferencesProvider: true,
		},
		ServerInfo: serverInfo{Name: "kern-lsp", Version: "0.1.0"},
	})
}

// resolveRoot picks the project root: client rootUri, then the first
// workspace folder, then the Serve root argument. Legacy rootPath is used
// only when rootUri is absent.
func (s *Server) resolveRoot(p initializeParams) string {
	if p.RootURI != nil && *p.RootURI != "" {
		if path, err := uriToPath(*p.RootURI); err == nil && path != "" {
			return path
		}
	}
	if len(p.WorkspaceFolders) > 0 && p.WorkspaceFolders[0].URI != "" {
		if path, err := uriToPath(p.WorkspaceFolders[0].URI); err == nil && path != "" {
			return path
		}
	}
	if p.RootPath != nil && *p.RootPath != "" {
		return *p.RootPath
	}
	return s.root
}

// uriToPath converts a file:// URI to a local filesystem path. Remote hosts
// are rejected (kern indexes only local projects).
func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("lsp: not a file URI: %s", uri)
	}
	if u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("lsp: remote file URI not supported: %s", uri)
	}
	p := u.Path
	if u.Host != "" && runtime.GOOS == "windows" {
		p = "//" + u.Host + p
	}
	if dec, err := url.PathUnescape(p); err == nil {
		p = dec
	}
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
		p = filepath.FromSlash(p)
	}
	return p, nil
}

// fileURI converts a local path to a file:// URI.
func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

// ---- textDocument/* ----

// position is an LSP Position: 0-based line and UTF-16 character offset.
type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type textDocumentPositionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position position `json:"position"`
}

// lookupAt extracts the identifier at pos from the file on disk and resolves
// it in the session index. ok is false when the file is unreadable, the
// position holds no identifier, or the index has no such symbol.
func (s *Server) lookupAt(uri string, pos position) (index.Symbol, bool) {
	if s.ix == nil {
		return index.Symbol{}, false
	}
	path, err := uriToPath(uri)
	if err != nil {
		return index.Symbol{}, false
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return index.Symbol{}, false
	}
	start, end, ok := identifierAt(src, pos.Line, pos.Character)
	if !ok {
		return index.Symbol{}, false
	}
	return s.ix.FindSymbol(string(src[start:end]))
}

// identifierAt returns the byte offsets of the ASCII identifier
// [A-Za-z0-9_] covering the UTF-16 position (line, character) in src.
// The UTF-16 column is converted to a byte offset exactly (each rune is 1
// UTF-16 unit below U+10000, 2 above); the position need only touch the
// identifier, and a position just past its last character still matches.
func identifierAt(src []byte, line, character int) (start, end int, ok bool) {
	lineStart := 0
	for l := 0; ; l++ {
		lineEnd := lineStart
		for lineEnd < len(src) && src[lineEnd] != '\n' {
			lineEnd++
		}
		if l == line {
			at, valid := utf16ToByteOffset(src[lineStart:lineEnd], character)
			if !valid {
				return 0, 0, false
			}
			start, end, ok := identBounds(src[lineStart:lineEnd], at)
			if !ok {
				return 0, 0, false
			}
			// identBounds returns offsets relative to the line slice; add the
			// line's start so the caller can index src directly.
			return lineStart + start, lineStart + end, true
		}
		if lineEnd >= len(src) {
			return 0, 0, false // requested line past EOF
		}
		lineStart = lineEnd + 1 // skip '\n'
	}
}

// utf16ToByteOffset converts a UTF-16 code-unit offset into a byte offset
// within line. Returns valid=false for a negative column.
func utf16ToByteOffset(line []byte, col int) (int, bool) {
	if col < 0 {
		return 0, false
	}
	units, off := 0, 0
	for off < len(line) && units < col {
		r, size := utf8.DecodeRune(line[off:])
		off += size
		if r > 0xFFFF {
			units += 2 // surrogate pair = 2 UTF-16 code units
		} else {
			units++
		}
	}
	return off, true
}

// identBounds returns the identifier touching byte offset at within line, or
// ok=false when at is not on an identifier. The cursor may sit on an
// identifier character (mid-token) or just past the identifier's last
// character (end-of-token, the char at at is non-identifier but the one
// before it is) — both resolve to the identifier.
func identBounds(line []byte, at int) (int, int, bool) {
	if at > len(line) {
		at = len(line)
	}
	onIdent := at < len(line) && isIdentChar(line[at])
	justAfter := at > 0 && at <= len(line) && isIdentChar(line[at-1])
	if !onIdent && !justAfter {
		return 0, 0, false // not on an identifier character
	}
	if !onIdent {
		at-- // move onto the identifier's last character
	}
	start := at
	for start > 0 && isIdentChar(line[start-1]) {
		start--
	}
	end := at
	for end < len(line) && isIdentChar(line[end]) {
		end++
	}
	if start == end {
		return 0, 0, false
	}
	return start, end, true
}

func isIdentChar(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// hoverResult is an LSP Hover with markdown contents.
type hoverResult struct {
	Contents markupContent `json:"contents"`
}

type markupContent struct {
	Kind  string `json:"kind"` // "markdown"
	Value string `json:"value"`
}

// handleHover answers textDocument/hover with a markdown card for the
// identifier at the position, or null when the index has no such symbol.
func (s *Server) handleHover(m rpcMessage) error {
	var p textDocumentPositionParams
	if len(m.Params) > 0 {
		_ = json.Unmarshal(m.Params, &p)
	}
	sym, ok := s.lookupAt(p.TextDocument.URI, p.Position)
	if !ok {
		return s.respond(m.ID, nil)
	}
	return s.respond(m.ID, hoverResult{Contents: markupContent{Kind: "markdown", Value: hoverMarkdown(sym)}})
}

// hoverMarkdown renders the hover card: name/kind, file:line, and the
// signature the index stores (the index keeps params/returns, not doc
// comments).
func hoverMarkdown(sym index.Symbol) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** — %s\n\n", sym.FullName(), sym.Kind)
	fmt.Fprintf(&b, "%s:%d\n", sym.File, sym.Line)
	if sig := symbolSignature(sym); sig != "" {
		fmt.Fprintf(&b, "\n```go\n%s\n```\n", sig)
	}
	return b.String()
}

// symbolSignature reconstructs a declaration signature from the index's
// Params/Returns fields. The index stores no doc comment, so this is the
// richest snippet it can serve.
func symbolSignature(sym index.Symbol) string {
	params := strings.Join(sym.Params, ", ")
	returns := strings.Join(sym.Returns, ", ")
	rets := ""
	if returns != "" {
		rets = " " + returns
	}
	switch sym.Kind {
	case "func":
		return fmt.Sprintf("func %s(%s)%s", sym.Name, params, rets)
	case "method":
		return fmt.Sprintf("func (%s) %s(%s)%s", sym.Receiver, sym.Name, params, rets)
	case "struct", "interface", "type":
		return fmt.Sprintf("type %s %s", sym.Name, sym.Kind)
	case "const":
		return fmt.Sprintf("const %s", sym.Name)
	case "var":
		return fmt.Sprintf("var %s", sym.Name)
	default:
		return fmt.Sprintf("%s %s", sym.Kind, sym.Name)
	}
}

// range_ is an LSP Range (0-based).
type range_ struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

// location is an LSP Location: a file URI plus a range.
type location struct {
	URI   string `json:"uri"`
	Range range_ `json:"range"`
}

// symbolLocation converts an index symbol to an LSP Location. The index is
// line-precise only, so the range spans [line-1, char 0] to [line-1,
// len(name)] — no column data is stored in the index (v1 limitation).
func (s *Server) symbolLocation(sym index.Symbol) location {
	uri := fileURI(filepath.Join(s.root, sym.File))
	line := sym.Line - 1 // index lines are 1-based, LSP 0-based
	return location{
		URI: uri,
		Range: range_{
			Start: position{Line: line, Character: 0},
			End:   position{Line: line, Character: len(sym.Name)},
		},
	}
}

// handleDefinition answers textDocument/definition with the declaration
// location of the identifier at the position, or null when not found.
func (s *Server) handleDefinition(m rpcMessage) error {
	var p textDocumentPositionParams
	if len(m.Params) > 0 {
		_ = json.Unmarshal(m.Params, &p)
	}
	sym, ok := s.lookupAt(p.TextDocument.URI, p.Position)
	if !ok {
		return s.respond(m.ID, nil)
	}
	return s.respond(m.ID, s.symbolLocation(sym))
}

// handleReferences answers textDocument/references with the definition plus
// the callers the index records (via the existing CallersFor edge map — no
// new graph layer is built). Empty when the identifier is unknown or nothing
// references it.
func (s *Server) handleReferences(m rpcMessage) error {
	var p textDocumentPositionParams
	if len(m.Params) > 0 {
		_ = json.Unmarshal(m.Params, &p)
	}
	sym, ok := s.lookupAt(p.TextDocument.URI, p.Position)
	if !ok {
		return s.respond(m.ID, nil)
	}
	seen := map[string]bool{}
	var locs []location
	add := func(l location) {
		key := fmt.Sprintf("%s:%d", l.URI, l.Range.Start.Line)
		if !seen[key] {
			seen[key] = true
			locs = append(locs, l)
		}
	}
	// Definition first, then every caller the index attributes to this symbol.
	add(s.symbolLocation(sym))
	for _, caller := range s.ix.CallersFor(sym) {
		if cs, ok2 := s.ix.FindSymbol(caller); ok2 {
			add(s.symbolLocation(cs))
		}
	}
	if len(locs) == 0 {
		return s.respond(m.ID, nil)
	}
	return s.respond(m.ID, locs)
}

// ---- response writing ----

// response is a JSON-RPC 2.0 response. On success Result is the raw result
// (or explicit "null"); on error Error is set and Result is omitted.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// respond writes a success response; a nil result is written as explicit
// null (the LSP contract for shutdown/hover-miss).
func (s *Server) respond(id json.RawMessage, result any) error {
	var raw json.RawMessage
	if result == nil {
		raw = json.RawMessage("null")
	} else {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		raw = b
	}
	return s.write(response{JSONRPC: "2.0", ID: id, Result: raw})
}

// respondError writes an error response (result omitted, per JSON-RPC 2.0).
func (s *Server) respondError(id json.RawMessage, code int, msg string) error {
	return s.write(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

// write frames and writes one response, flushing when the writer supports it.
func (s *Server) write(resp response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	if _, err := s.out.Write(data); err != nil {
		return err
	}
	if f, ok := s.out.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}
