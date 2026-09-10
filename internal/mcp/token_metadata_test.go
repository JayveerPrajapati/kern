package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// tokenMetadataOf extracts the tokenMetadata object from a tools/call result
// produced by a full JSON round-trip (serveOne), failing the test if missing.
func tokenMetadataOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %+v", resp)
	}
	meta, ok := res["tokenMetadata"].(map[string]any)
	if !ok {
		t.Fatalf("result has no tokenMetadata object: %+v", res)
	}
	return meta
}

// assertTokenMetadataPresent checks a result carries valid token metadata.
// Direct dispatch (safeDispatch) holds the typed TokenMetadata struct, while a
// JSON round-trip yields a decoded map; both are accepted.
func assertTokenMetadataPresent(t *testing.T, res map[string]any) {
	t.Helper()
	switch v := res["tokenMetadata"].(type) {
	case map[string]any:
		if used, _ := v["tokensUsed"].(float64); used <= 0 {
			t.Errorf("tokensUsed = %v, want > 0", v["tokensUsed"])
		}
		if returned, _ := v["tokensReturned"].(float64); returned <= 0 {
			t.Errorf("tokensReturned = %v, want > 0", v["tokensReturned"])
		}
		if cost, _ := v["estimatedCost"].(float64); cost <= 0 {
			t.Errorf("estimatedCost = %v, want > 0", v["estimatedCost"])
		}
	case TokenMetadata:
		if v.TokensUsed <= 0 || v.TokensReturned <= 0 || v.EstimatedCost <= 0 {
			t.Errorf("invalid tokenMetadata: %+v", v)
		}
	default:
		t.Fatalf("result has no tokenMetadata object: %+v", res)
	}
}

func TestCountRequestTokens(t *testing.T) {
	empty := countRequestTokens("kern_foo", nil)
	if empty <= 0 {
		t.Errorf("countRequestTokens(name, nil) = %d, want > 0", empty)
	}
	fat := countRequestTokens("kern_foo", map[string]any{"text": strings.Repeat("word ", 2000)})
	if fat <= empty {
		t.Errorf("countRequestTokens with 2000 words = %d, want > bare-name count %d", fat, empty)
	}
}

func TestTokenMetadataFor(t *testing.T) {
	args := map[string]any{"text": strings.Repeat("word ", 500)}
	meta := tokenMetadataFor("kern_x", args, "short output")
	if meta.TokensUsed <= 0 {
		t.Errorf("TokensUsed = %d, want > 0", meta.TokensUsed)
	}
	if meta.TokensReturned <= 0 {
		t.Errorf("TokensReturned = %d, want > 0", meta.TokensReturned)
	}
	if want := meta.TokensUsed - meta.TokensReturned; meta.Savings != want {
		t.Errorf("Savings = %d, want %d (used - returned)", meta.Savings, want)
	}
	if meta.Savings <= 0 {
		t.Errorf("Savings = %d, want > 0 for compressible call", meta.Savings)
	}
	if meta.EstimatedCost <= 0 {
		t.Errorf("EstimatedCost = %v, want > 0", meta.EstimatedCost)
	}
}

func TestTokenMetadataForClampsSavings(t *testing.T) {
	// A response larger than the request (the common case: search/read tools
	// return more than they consume) must report zero savings, not negative.
	meta := tokenMetadataFor("kern_x", map[string]any{}, strings.Repeat("word ", 2000))
	if meta.Savings != 0 {
		t.Errorf("Savings = %d, want 0 when response exceeds request", meta.Savings)
	}
}

func TestTokenMetadataOmitsZeroSavings(t *testing.T) {
	meta := TokenMetadata{TokensUsed: 10, TokensReturned: 5, EstimatedCost: 0.00015, Savings: 0}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "savings") {
		t.Errorf("zero savings leaked into JSON: %s", b)
	}
	if !strings.Contains(string(b), "tokensUsed") || !strings.Contains(string(b), "tokensReturned") || !strings.Contains(string(b), "estimatedCost") {
		t.Errorf("expected tokensUsed/tokensReturned/estimatedCost fields, got %s", b)
	}
}

func TestTokenMetadataOnToolResponse(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	text := "email me at a@b.com, token ghp_1234567890abcdefghijklmnopqrstuvw"
	args, _ := json.Marshal(map[string]any{"text": text})
	resp := serveOne(t, writeReq("tools/call", 1, `{"name":"kern_mask_pii","arguments":`+string(args)+`}`))
	if _, isErr := toolResultText(t, resp); isErr {
		t.Fatalf("unexpected error: %+v", resp)
	}
	meta := tokenMetadataOf(t, resp)
	if used, _ := meta["tokensUsed"].(float64); used <= 0 {
		t.Errorf("tokensUsed = %v, want > 0", meta["tokensUsed"])
	}
	if returned, _ := meta["tokensReturned"].(float64); returned <= 0 {
		t.Errorf("tokensReturned = %v, want > 0", meta["tokensReturned"])
	}
	if cost, _ := meta["estimatedCost"].(float64); cost <= 0 {
		t.Errorf("estimatedCost = %v, want > 0", meta["estimatedCost"])
	}
}

func TestTokenMetadataSavingsOnOptimize(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// A large repetitive log compresses well, so savings must be positive.
	log := strings.Repeat("[INFO] routine heartbeat tick\n", 200) + "ERROR disk full\n"
	args, _ := json.Marshal(map[string]any{"log": log})
	resp := serveOne(t, writeReq("tools/call", 2, `{"name":"kern_optimize_log","arguments":`+string(args)+`}`))
	if out, isErr := toolResultText(t, resp); isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	meta := tokenMetadataOf(t, resp)
	savings, _ := meta["savings"].(float64)
	if savings <= 0 {
		t.Errorf("savings = %v, want > 0 for a compressible log", meta["savings"])
	}
}

func TestTokenMetadataOnErrorResult(t *testing.T) {
	// A handler error (isError=true) is still a tool response and must carry
	// the token ledger, counting the error text it returned.
	resp := serveOne(t, writeReq("tools/call", 3, `{"name":"kern_optimize_prompt","arguments":{}}`))
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %+v", resp)
	}
	if res["isError"] != true {
		t.Fatalf("expected isError result, got %+v", res)
	}
	meta := tokenMetadataOf(t, resp)
	if used, _ := meta["tokensUsed"].(float64); used <= 0 {
		t.Errorf("tokensUsed = %v, want > 0 on error result", meta["tokensUsed"])
	}
	if returned, _ := meta["tokensReturned"].(float64); returned <= 0 {
		t.Errorf("tokensReturned = %v, want > 0 on error result", meta["tokensReturned"])
	}
}

func TestTokenMetadataOnPreToolDenial(t *testing.T) {
	s := newTestServer()
	s.roots = []string{"/"}
	s.gate = nil
	s.preTool = func(name string, args map[string]any) error {
		return errors.New("policy says no")
	}
	req := rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage(`"deny"`),
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"kern_health","arguments":{}}`),
	}
	respAny := s.safeDispatch(req)
	resp, ok := respAny.(map[string]any)
	if !ok {
		t.Fatalf("dispatch returned %T, want map", respAny)
	}
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %+v", resp)
	}
	if res["isError"] != true {
		t.Fatalf("expected denial isError result, got %+v", res)
	}
	assertTokenMetadataPresent(t, res)
}

func TestTokenMetadataOnGateDenial(t *testing.T) {
	s := newTestServer()
	s.roots = []string{"/"}
	s.gate = &Gate{roots: []string{"/definitely-not-a-real-root-xyz"}, enabled: true}
	req := rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage(`1`),
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"kern_health","arguments":{"root":"/etc"}}`),
	}
	respAny := s.safeDispatch(req)
	resp, ok := respAny.(map[string]any)
	if !ok {
		t.Fatalf("dispatch returned %T, want map", respAny)
	}
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %+v", resp)
	}
	if res["isError"] != true {
		t.Fatalf("expected gate denial isError result, got %+v", res)
	}
	assertTokenMetadataPresent(t, res)
}
