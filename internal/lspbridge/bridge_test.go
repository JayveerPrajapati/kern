package lspbridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDetectServer(t *testing.T) {
	// Should detect Go language
	cmd, lang, found := DetectServer("main.go")
	if lang != "go" {
		t.Errorf("lang = %s, want go", lang)
	}
	// If gopls is in PATH, found is true, otherwise false without failing
	if found && len(cmd) == 0 {
		t.Errorf("found true but cmd empty")
	}

	// Python
	_, pyLang, _ := DetectServer("script.py")
	if pyLang != "python" {
		t.Errorf("lang = %s, want python", pyLang)
	}

	// TypeScript
	_, tsLang, _ := DetectServer("app.ts")
	if tsLang != "typescript" {
		t.Errorf("lang = %s, want typescript", tsLang)
	}

	// Rust
	_, rsLang, _ := DetectServer("lib.rs")
	if rsLang != "rust" {
		t.Errorf("lang = %s, want rust", rsLang)
	}

	// Unknown
	_, unkLang, unkFound := DetectServer("file.xyz123")
	if unkFound {
		t.Errorf("expected unknown file not to find server")
	}
	if unkLang != "file.xyz123" && unkLang != "" {
		t.Logf("unknown lang: %s", unkLang)
	}
}

func TestInstalledServers(t *testing.T) {
	servers := InstalledServers()
	if servers == nil {
		t.Fatal("expected non-nil map")
	}
	t.Logf("Installed servers: %+v", servers)
}

func TestParseHover(t *testing.T) {
	t.Run("string hover", func(t *testing.T) {
		h := parseHover("```go\nfunc Foo() int\n```\nFoo returns 42")
		if h.Signature != "func Foo() int" {
			t.Errorf("signature = %q, want %q", h.Signature, "func Foo() int")
		}
		if h.Doc != "Foo returns 42" {
			t.Errorf("doc = %q, want %q", h.Doc, "Foo returns 42")
		}
	})

	t.Run("map hover", func(t *testing.T) {
		m := map[string]any{"value": "```typescript\nfunction bar(): void\n```\nBar docs"}
		h := parseHover(m)
		if h.Signature != "function bar(): void" {
			t.Errorf("signature = %q", h.Signature)
		}
	})
}

func TestParseLocations(t *testing.T) {
	t.Run("single location", func(t *testing.T) {
		raw := json.RawMessage(`{"uri": "file:///root/main.go", "range": {"start": {"line": 10, "character": 4}, "end": {"line": 10, "character": 8}}}`)
		locs, err := parseLocations(raw, "/root")
		if err != nil {
			t.Fatalf("parseLocations: %v", err)
		}
		if len(locs) != 1 {
			t.Fatalf("len = %d, want 1", len(locs))
		}
		if locs[0].Line != 11 || locs[0].Col != 5 {
			t.Errorf("got line %d, col %d, want 11, 5", locs[0].Line, locs[0].Col)
		}
		if locs[0].File != "main.go" {
			t.Errorf("got file %s, want main.go", locs[0].File)
		}
	})

	t.Run("multi location link", func(t *testing.T) {
		raw := json.RawMessage(`[
			{"targetUri": "file:///root/pkg/util.go", "targetSelectionRange": {"start": {"line": 2, "character": 0}, "end": {"line": 2, "character": 10}}}
		]`)
		locs, err := parseLocations(raw, "/root")
		if err != nil {
			t.Fatalf("parseLocations: %v", err)
		}
		if len(locs) != 1 {
			t.Fatalf("len = %d, want 1", len(locs))
		}
		if locs[0].Line != 3 || locs[0].Col != 1 {
			t.Errorf("got line %d, col %d, want 3, 1", locs[0].Line, locs[0].Col)
		}
		if locs[0].File != "pkg/util.go" {
			t.Errorf("got file %s, want pkg/util.go", locs[0].File)
		}
	})
}

func TestParseDocumentSymbols(t *testing.T) {
	raw := json.RawMessage(`[
		{
			"name": "MyStruct",
			"kind": 23,
			"range": {"start": {"line": 5, "character": 0}, "end": {"line": 15, "character": 1}},
			"selectionRange": {"start": {"line": 5, "character": 5}, "end": {"line": 5, "character": 13}},
			"children": [
				{
					"name": "FieldA",
					"kind": 8,
					"range": {"start": {"line": 6, "character": 1}, "end": {"line": 6, "character": 10}},
					"selectionRange": {"start": {"line": 6, "character": 1}, "end": {"line": 6, "character": 7}}
				}
			]
		}
	]`)

	syms, err := parseDocumentSymbols(raw)
	if err != nil {
		t.Fatalf("parseDocumentSymbols: %v", err)
	}
	if len(syms) != 1 {
		t.Fatalf("len = %d, want 1", len(syms))
	}
	if syms[0].Name != "MyStruct" || syms[0].Kind != "struct" {
		t.Errorf("symbol = %+v", syms[0])
	}
	if len(syms[0].Children) != 1 || syms[0].Children[0].Name != "FieldA" || syms[0].Children[0].Kind != "field" {
		t.Errorf("child = %+v", syms[0].Children)
	}
}

// TestMockLSPServer executes full client protocol handshake and queries against a mock server script.
func TestMockLSPServer(t *testing.T) {
	dir := t.TempDir()

	// Write mock Python or Go responder script
	// Let's create a minimal mock LSP server script in python or shell
	mockPy := filepath.Join(dir, "mock_lsp.py")
	script := `
import sys, json

def read_msg():
    length = 0
    while True:
        line = sys.stdin.readline()
        if not line:
            return None
        line = line.strip()
        if not line:
            break
        if line.lower().startswith("content-length:"):
            length = int(line.split(":")[1].strip())
    if length <= 0:
        return None
    return json.loads(sys.stdin.read(length))

def send_msg(obj):
    body = json.dumps(obj)
    sys.stdout.write(f"Content-Length: {len(body)}\r\n\r\n{body}")
    sys.stdout.flush()

while True:
    msg = read_msg()
    if msg is None:
        break
    method = msg.get("method")
    req_id = msg.get("id")
    
    if method == "initialize":
        send_msg({"jsonrpc": "2.0", "id": req_id, "result": {"capabilities": {}}})
    elif method == "textDocument/definition":
        send_msg({
            "jsonrpc": "2.0",
            "id": req_id,
            "result": [
                {"uri": "file:///tmp/demo.py", "range": {"start": {"line": 9, "character": 4}, "end": {"line": 9, "character": 12}}}
            ]
        })
    elif method == "textDocument/hover":
        send_msg({
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "contents": (chr(96)*3) + "python\ndef calculate(): int\n" + (chr(96)*3) + "\nCalculate result."
            }
        })
    elif method == "textDocument/references":
        send_msg({
            "jsonrpc": "2.0",
            "id": req_id,
            "result": [
                {"uri": "file:///tmp/caller.py", "range": {"start": {"line": 15, "character": 0}, "end": {"line": 15, "character": 8}}}
            ]
        })
    elif method == "textDocument/documentSymbol":
        send_msg({
            "jsonrpc": "2.0",
            "id": req_id,
            "result": [
                {"name": "calculate", "kind": 12, "range": {"start": {"line": 0, "character": 0}, "end": {"line": 10, "character": 0}}, "selectionRange": {"start": {"line": 0, "character": 4}, "end": {"line": 0, "character": 13}}}
            ]
        })
    elif method == "shutdown":
        send_msg({"jsonrpc": "2.0", "id": req_id, "result": None})
    elif method == "exit":
        break
`
	if err := os.WriteFile(mockPy, []byte(script), 0755); err != nil {
		t.Fatalf("write mock: %v", err)
	}

	pyExe, err := exec.LookPath("python3")
	if err != nil {
		pyExe, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("python not installed in test environment, skipping mock subprocess test")
	}

	// Create a dummy file to open
	testFile := filepath.Join(dir, "demo.py")
	if err := os.WriteFile(testFile, []byte("def calculate():\n    return 42\n"), 0644); err != nil {
		t.Fatalf("write testFile: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := []string{pyExe, mockPy}
	client, err := StartClient(ctx, dir, cmd)
	if err != nil {
		t.Fatalf("StartClient: %v", err)
	}
	defer client.Close()

	// 1. Definition
	defs, err := client.Definition(ctx, testFile, 1, 5)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(defs) != 1 || defs[0].Line != 10 {
		t.Errorf("defs = %+v", defs)
	}

	// 2. Hover
	hover, err := client.Hover(ctx, testFile, 1, 5)
	if err != nil {
		t.Fatalf("Hover: %v", err)
	}
	if hover == nil || hover.Signature != "def calculate(): int" {
		t.Errorf("hover = %+v", hover)
	}

	// 3. References
	refs, err := client.References(ctx, testFile, 1, 5)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(refs) != 1 || refs[0].Line != 16 {
		t.Errorf("refs = %+v", refs)
	}

	// 4. DocumentSymbols
	syms, err := client.DocumentSymbols(ctx, testFile)
	if err != nil {
		t.Fatalf("DocumentSymbols: %v", err)
	}
	if len(syms) != 1 || syms[0].Name != "calculate" || syms[0].Kind != "function" {
		t.Errorf("syms = %+v", syms)
	}

	// 5. Test high-level Query() with custom server_cmd
	qRes, err := Query(ctx, QueryRequest{
		Root:      dir,
		File:      testFile,
		Line:      1,
		Column:    5,
		Action:    "hover",
		ServerCmd: cmd,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if qRes.Hover == nil || qRes.Hover.Signature != "def calculate(): int" {
		t.Errorf("Query hover = %+v", qRes.Hover)
	}
}
