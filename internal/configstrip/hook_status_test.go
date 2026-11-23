package configstrip

import "testing"

func TestHasKernHookStandardAndAntigravityShapes(t *testing.T) {
	standard := `{"hooks":{"PreToolUse":[{"hooks":[{"command":"kern guard hook"}]}]}}`
	if ok, err := HasKernHook(standard); err != nil || !ok {
		t.Fatalf("standard hook = %v, err = %v; want true, nil", ok, err)
	}

	antigravity := `{"kern-guard":{"PreToolUse":[{"hooks":[{"command":"kern guard hook"}]}]}}`
	if ok, err := HasKernHook(antigravity); err != nil || !ok {
		t.Fatalf("antigravity hook = %v, err = %v; want true, nil", ok, err)
	}

	mcpOnly := `{"mcpServers":{"kern":{"command":"kern-mcp"}}}`
	if ok, err := HasKernHook(mcpOnly); err != nil || ok {
		t.Fatalf("mcp-only = %v, err = %v; want false, nil", ok, err)
	}
}
