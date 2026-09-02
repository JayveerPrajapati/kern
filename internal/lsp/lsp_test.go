package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- framing helpers (tiny in-memory LSP client) ----

// frame wraps a JSON body in a Content-Length header, LSP-style.
func frame(body []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Content-Length: %d\r\n\r\n", len(body))
	b.Write(body)
	return b.Bytes()
}

// chunkReader returns at most n bytes per Read call, so the server's bufio
// reader experiences real partial reads (messages split across reads).
type chunkReader struct {
	r *bytes.Reader
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(p) > c.n {
		p = p[:c.n]
	}
	return c.r.Read(p)
}

// readFrames parses every Content-Length framed message in data.
func readFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	r := bufio.NewReader(bytes.NewReader(data))
	var frames [][]byte
	for {
		msg, err := readMessage(r)
		if err == io.EOF {
			return frames
		}
		if err != nil {
			t.Fatalf("readFrames: %v", err)
		}
		frames = append(frames, msg)
	}
}

// testClient is a tiny in-memory LSP client: it frames requests into in,
// runs Serve to completion, and parses the framed responses from out.
type testClient struct {
	t    *testing.T
	root string
	in   *bytes.Buffer
	out  *bytes.Buffer
}

func newTestClient(t *testing.T, root string) *testClient {
	t.Helper()
	return &testClient{t: t, root: root, in: &bytes.Buffer{}, out: &bytes.Buffer{}}
}

// request appends a request frame with the given id and returns the id as
// sent, so the test can correlate responses.
func (c *testClient) request(id int, method string, params any) json.RawMessage {
	c.t.Helper()
	rawID := json.RawMessage(fmt.Sprintf("%d", id))
	c.sendRaw(encodeMessage(c.t, rawID, method, params))
	return rawID
}

// notify appends a notification frame (no id).
func (c *testClient) notify(method string, params any) {
	c.t.Helper()
	c.sendRaw(encodeMessage(c.t, nil, method, params))
}

// sendRaw appends an already-framed message.
func (c *testClient) sendRaw(framed []byte) {
	c.in.Write(framed)
}

// run serves the scripted input to completion (EOF or exit notification).
func (c *testClient) run() error {
	return Serve(context.Background(), c.root, c.in, c.out)
}

// responses parses all framed responses the server wrote.
func (c *testClient) responses() []wireResponse {
	c.t.Helper()
	var out []wireResponse
	for _, raw := range readFrames(c.t, c.out.Bytes()) {
		var w wireResponse
		if err := json.Unmarshal(raw, &w); err != nil {
			c.t.Fatalf("unmarshal response %s: %v", raw, err)
		}
		out = append(out, w)
	}
	return out
}

// byID returns the response with the matching id.
func (c *testClient) byID(resps []wireResponse, id json.RawMessage) *wireResponse {
	c.t.Helper()
	for i := range resps {
		if bytes.Equal(resps[i].ID, id) {
			return &resps[i]
		}
	}
	c.t.Fatalf("no response with id %s in %d response(s)", id, len(resps))
	return nil
}

// wireResponse is the response shape the test client parses.
type wireResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// encodeMessage builds one framed JSON-RPC message body.
func encodeMessage(t *testing.T, id json.RawMessage, method string, params any) []byte {
	t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if len(id) > 0 {
		msg["id"] = json.RawMessage(id)
	}
	if params != nil {
		msg["params"] = params
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("encode message: %v", err)
	}
	return frame(body)
}

// tDeref is a tiny indirection so encodeMessage keeps its signature honest.
func tDeref(t *testing.T) *testing.T { return t }

// ---- frame round-trip ----

func TestFrameRoundTrip(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	framed := frame(body)

	// Single message decodes back to the exact body.
	r := bufio.NewReader(bytes.NewReader(framed))
	got, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage(single): %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("round-trip mismatch:\n got %s\nwant %s", got, body)
	}

	// Two messages in one read decode independently.
	two := append(append([]byte{}, framed...), framed...)
	r = bufio.NewReader(bytes.NewReader(two))
	for i := 0; i < 2; i++ {
		got, err := readMessage(r)
		if err != nil {
			t.Fatalf("readMessage(two, #%d): %v", i, err)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("two-in-one #%d mismatch:\n got %s\nwant %s", i, got, body)
		}
	}
	if _, err := readMessage(r); err != io.EOF {
		t.Fatalf("expected EOF after both messages, got %v", err)
	}

	// A message split across many reads (1 byte at a time) still decodes:
	// this is what a client that writes incrementally produces.
	r = bufio.NewReader(&chunkReader{r: bytes.NewReader(framed), n: 1})
	got, err = readMessage(r)
	if err != nil {
		t.Fatalf("readMessage(split): %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("split round-trip mismatch:\n got %s\nwant %s", got, body)
	}

	// Header name case-insensitivity ("content-length" lowercase).
	lower := []byte(fmt.Sprintf("content-length: %d\r\n\r\n", len(body)))
	lower = append(lower, body...)
	got, err = readMessage(bufio.NewReader(bytes.NewReader(lower)))
	if err != nil {
		t.Fatalf("readMessage(lowercase header): %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("lowercase header mismatch:\n got %s\nwant %s", got, body)
	}
}

// ---- fixture project ----

// writeFixture creates a tiny Go project with Greet and a caller, returning
// its root and the source of main.go.
func writeFixture(t *testing.T) (root, mainSrc string) {
	t.Helper()
	root = t.TempDir()
	mainSrc = `package main

// Greet returns a greeting for name.
func Greet(name string) string {
	return "hello " + name
}

func main() {
	msg := Greet("world")
	_ = msg
}
`
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, mainSrc
}

// identPosition returns the 0-based line/character of the nth standalone
// occurrence of name in src.
func identPosition(t *testing.T, src, name string, nth int) position {
	t.Helper()
	seen := 0
	for li, line := range strings.Split(src, "\n") {
		for j := 0; j+len(name) <= len(line); j++ {
			if line[j:j+len(name)] != name {
				continue
			}
			if j > 0 && isIdentChar(line[j-1]) {
				continue
			}
			if j+len(name) < len(line) && isIdentChar(line[j+len(name)]) {
				continue
			}
			seen++
			if seen == nth {
				return position{Line: li, Character: j}
			}
		}
	}
	t.Fatalf("occurrence %d of %q not found in:\n%s", nth, name, src)
	return position{}
}

// ---- initialize ----

func TestInitializeCapabilities(t *testing.T) {
	c := newTestClient(t, ".")
	id := c.request(1, "initialize", map[string]any{
		"rootUri": fileURI(t.TempDir()),
	})
	c.notify("initialized", nil)

	if err := c.run(); err != nil {
		t.Fatalf("Serve: %v", err) // EOF after input is a clean stop
	}
	resp := c.byID(c.responses(), id)
	if resp.Error != nil {
		t.Fatalf("initialize errored: %+v", resp.Error)
	}
	var res struct {
		Capabilities struct {
			TextDocumentSync   int  `json:"textDocumentSync"`
			HoverProvider      bool `json:"hoverProvider"`
			DefinitionProvider bool `json:"definitionProvider"`
			ReferencesProvider bool `json:"referencesProvider"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("unmarshal initialize result %s: %v", resp.Result, err)
	}
	if res.Capabilities.TextDocumentSync != 0 {
		t.Errorf("textDocumentSync = %d, want 0", res.Capabilities.TextDocumentSync)
	}
	if !res.Capabilities.HoverProvider || !res.Capabilities.DefinitionProvider || !res.Capabilities.ReferencesProvider {
		t.Errorf("capabilities = %+v, want hover/definition/references all true", res.Capabilities)
	}
	if res.ServerInfo.Name != "kern-lsp" {
		t.Errorf("serverInfo.name = %q, want kern-lsp", res.ServerInfo.Name)
	}
}

// TestSessionEndToEnd runs initialize → hover → definition → references →
// shutdown → exit against the fixture and asserts every payload.
func TestSessionEndToEnd(t *testing.T) {
	root, src := writeFixture(t)
	c := newTestClient(t, "unused-root-fallback") // rootUri must win
	c.request(1, "initialize", map[string]any{"rootUri": fileURI(root)})
	c.notify("initialized", nil)

	pos := identPosition(t, src, "Greet", 2) // occurrence 2 = the func decl (1st is the doc comment)
	c.request(2, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": fileURI(filepath.Join(root, "main.go"))},
		"position":     pos,
	})
	c.request(3, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": fileURI(filepath.Join(root, "main.go"))},
		"position":     pos,
	})
	c.request(4, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": fileURI(filepath.Join(root, "main.go"))},
		"position":     pos,
	})
	c.request(5, "shutdown", nil)
	c.notify("exit", nil)

	if err := c.run(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := c.responses()

	// hover: markdown contains the name and the file.
	hover := c.byID(resps, json.RawMessage("2"))
	if hover.Error != nil {
		t.Fatalf("hover errored: %+v", hover.Error)
	}
	var hr struct {
		Contents struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(hover.Result, &hr); err != nil {
		t.Fatalf("unmarshal hover %s: %v", hover.Result, err)
	}
	if hr.Contents.Kind != "markdown" {
		t.Errorf("hover kind = %q, want markdown", hr.Contents.Kind)
	}
	for _, want := range []string{"Greet", "main.go"} {
		if !strings.Contains(hr.Contents.Value, want) {
			t.Errorf("hover markdown missing %q:\n%s", want, hr.Contents.Value)
		}
	}

	// definition: uri ends with main.go, line matches the definition line.
	def := c.byID(resps, json.RawMessage("3"))
	if def.Error != nil {
		t.Fatalf("definition errored: %+v", def.Error)
	}
	var loc struct {
		URI   string `json:"uri"`
		Range struct {
			Start position `json:"start"`
			End   position `json:"end"`
		} `json:"range"`
	}
	if err := json.Unmarshal(def.Result, &loc); err != nil {
		t.Fatalf("unmarshal definition %s: %v", def.Result, err)
	}
	if !strings.HasSuffix(loc.URI, "/main.go") {
		t.Errorf("definition uri = %q, want suffix /main.go", loc.URI)
	}
	if loc.Range.Start.Line != pos.Line {
		t.Errorf("definition line = %d, want %d (0-based)", loc.Range.Start.Line, pos.Line)
	}
	if loc.Range.Start.Character != 0 {
		t.Errorf("definition start char = %d, want 0 (index is line-precise)", loc.Range.Start.Character)
	}

	// references: definition plus the caller main, both on main.go.
	refs := c.byID(resps, json.RawMessage("4"))
	if refs.Error != nil {
		t.Fatalf("references errored: %+v", refs.Error)
	}
	var refLocs []struct {
		URI   string `json:"uri"`
		Range struct {
			Start position `json:"start"`
		} `json:"range"`
	}
	if err := json.Unmarshal(refs.Result, &refLocs); err != nil {
		t.Fatalf("unmarshal references %s: %v", refs.Result, err)
	}
	if len(refLocs) < 2 {
		t.Fatalf("references = %d location(s), want >= 2 (definition + caller)", len(refLocs))
	}
	mainLine := identPosition(t, src, "main", 2).Line // occurrence 2 = func main (1st is "package main")
	sawDef, sawMain := false, false
	for _, l := range refLocs {
		if !strings.HasSuffix(l.URI, "/main.go") {
			t.Errorf("reference uri = %q, want main.go", l.URI)
		}
		if l.Range.Start.Line == pos.Line {
			sawDef = true
		}
		if l.Range.Start.Line == mainLine {
			sawMain = true
		}
	}
	if !sawDef || !sawMain {
		t.Errorf("references missing definition (%v) or caller main at line %d: %+v", sawDef, mainLine, refLocs)
	}

	// shutdown: explicit null result.
	shut := c.byID(resps, json.RawMessage("5"))
	if shut.Error != nil {
		t.Fatalf("shutdown errored: %+v", shut.Error)
	}
	if string(shut.Result) != "null" {
		t.Errorf("shutdown result = %s, want null", shut.Result)
	}

	// exit: Serve returned nil already (c.run returned nil).
	if len(resps) != 5 {
		t.Errorf("got %d responses, want 5 (hover/definition/references/shutdown; exit is silent)", len(resps))
	}
}

// TestRootFallback uses the Serve root argument when initialize carries no
// rootUri/workspaceFolders.
func TestRootFallback(t *testing.T) {
	root, src := writeFixture(t)
	c := newTestClient(t, root)
	c.request(1, "initialize", map[string]any{})
	pos := identPosition(t, src, "Greet", 1)
	c.request(2, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": fileURI(filepath.Join(root, "main.go"))},
		"position":     pos,
	})
	c.notify("exit", nil)
	if err := c.run(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	def := c.byID(c.responses(), json.RawMessage("2"))
	if def.Error != nil {
		t.Fatalf("definition errored: %+v", def.Error)
	}
	if !strings.Contains(string(def.Result), "/main.go") {
		t.Errorf("definition = %s, want main.go location from root fallback", def.Result)
	}
}

// ---- shutdown / exit / unknown methods / parse errors ----

func TestShutdownExit(t *testing.T) {
	c := newTestClient(t, ".")
	c.request(1, "shutdown", nil)
	c.notify("exit", nil)
	if err := c.run(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := c.responses()
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1 (shutdown)", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("shutdown errored: %+v", resps[0].Error)
	}
	if string(resps[0].Result) != "null" {
		t.Errorf("shutdown result = %s, want null", resps[0].Result)
	}
}

func TestUnknownMethod(t *testing.T) {
	c := newTestClient(t, ".")
	c.request(1, "no/such/method", nil)
	c.notify("no/such/notification", nil) // must be ignored silently
	c.notify("$/cancelRequest", map[string]any{"id": 1})
	c.notify("$/setTrace", map[string]any{"value": "off"})
	c.request(2, "shutdown", nil)
	c.notify("exit", nil)
	if err := c.run(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := c.responses()
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2 (unknown request error + shutdown)", len(resps))
	}
	unk := c.byID(resps, json.RawMessage("1"))
	if unk.Error == nil || unk.Error.Code != -32601 {
		t.Fatalf("unknown request error = %+v, want code -32601", unk.Error)
	}
	shut := c.byID(resps, json.RawMessage("2"))
	if shut.Error != nil || string(shut.Result) != "null" {
		t.Errorf("shutdown = %+v, want null result", shut)
	}
}

func TestParseErrorKeepsServing(t *testing.T) {
	c := newTestClient(t, ".")
	// Garbage JSON body with a valid Content-Length: parse error, keep going.
	c.sendRaw(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":`)))
	// Valid JSON but no method: invalid request.
	c.sendRaw(frame([]byte(`{"jsonrpc":"2.0","id":7}`)))
	c.request(2, "shutdown", nil)
	c.notify("exit", nil)
	if err := c.run(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := c.responses()
	if len(resps) != 3 {
		t.Fatalf("got %d responses, want 3 (parse error + invalid request + shutdown)", len(resps))
	}
	if resps[0].Error == nil || resps[0].Error.Code != -32700 {
		t.Fatalf("parse error = %+v, want code -32700", resps[0].Error)
	}
	if resps[1].Error == nil || resps[1].Error.Code != -32600 {
		t.Fatalf("invalid request = %+v, want code -32600", resps[1].Error)
	}
	shut := c.byID(resps, json.RawMessage("2"))
	if shut.Error != nil || string(shut.Result) != "null" {
		t.Errorf("shutdown after parse error = %+v, want null result", shut)
	}
}

// ---- identifier extraction ----

func TestIdentifierAt(t *testing.T) {
	src := []byte("func Greet(name string) string {\n\treturn \"hi\" + name\n}\n")
	// Position on the G of Greet (line 0, col 5).
	start, end, ok := identifierAt(src, 0, 5)
	if !ok || string(src[start:end]) != "Greet" {
		t.Fatalf("identifierAt(0,5) = %d,%d,%v, want Greet", start, end, ok)
	}
	// Position just past Greet's last char still resolves to it.
	if _, _, ok := identifierAt(src, 0, 10); !ok {
		t.Fatal("identifierAt(0,10) = not ok, want Greet (cursor at end)")
	}
	// Position in the middle.
	if s, e, ok := identifierAt(src, 0, 7); !ok || string(src[s:e]) != "Greet" {
		t.Fatalf("identifierAt(0,7) = %d,%d,%v, want Greet", s, e, ok)
	}
	// A position between two non-identifier characters resolves to nothing.
	dbl := []byte("func  Greet")
	if _, _, ok := identifierAt(dbl, 0, 5); ok {
		t.Fatal("identifierAt(0,5 on double space) = ok, want none")
	}
	// Out-of-range line: no identifier.
	if _, _, ok := identifierAt(src, 99, 0); ok {
		t.Fatal("identifierAt(99,0) = ok, want none")
	}
}

// TestUTF16PositionConversion verifies the UTF-16 → byte offset conversion
// used for positions (astral characters count as two UTF-16 code units).
func TestUTF16PositionConversion(t *testing.T) {
	// "// é😀 Greet": é is 2 bytes (1 UTF-16 unit), 😀 is 4 bytes (2 units).
	line := []byte("// é😀 Greet")
	// "// " = 3 units, é = 1, 😀 = 2 → col 6 lands at the space after 😀,
	// whose byte offset is 3+2+4 = 9; "Greet" starts at byte 10.
	off, valid := utf16ToByteOffset(line, 6)
	if !valid || off != 9 {
		t.Fatalf("utf16ToByteOffset(line,6) = %d,%v, want 9", off, valid)
	}
	s, e, ok := identBounds(line, off+1) // on the G of Greet
	if !ok || string(line[s:e]) != "Greet" {
		t.Fatalf("identBounds at %d = %d,%d,%v, want Greet", off+1, s, e, ok)
	}
	// And the conversion is exact: col 6 (é+😀 = 3 UTF-16 units) lands right
	// after the astral rune, so the cursor there is not on an identifier.
	if _, _, ok := identBounds(line, off); ok {
		t.Fatal("identBounds at space = ok, want none")
	}
}
