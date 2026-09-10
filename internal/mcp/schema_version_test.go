package mcp

import (
	"testing"
)

// TestAllToolsHaveValidSchemaVersion (P2-003) enforces that every registered
// MCP tool carries a SchemaVersion, and that the value is a well-formed
// semantic version matching the current catalog contract. This runs against
// the registration table directly, mirroring the RiskLevel invariant
// (P0-004): a new tool added without a schema version fails here.
func TestAllToolsHaveValidSchemaVersion(t *testing.T) {
	if len(tools) == 0 {
		t.Fatal("no tools registered")
	}
	for _, tool := range tools {
		if tool.SchemaVersion == "" {
			t.Errorf("tool %s has no SchemaVersion assigned", tool.Name)
			continue
		}
		if !validSchemaVersion(tool.SchemaVersion) {
			t.Errorf("tool %s has malformed SchemaVersion %q (want x.y.z)", tool.Name, tool.SchemaVersion)
		}
		if tool.SchemaVersion != SchemaVersionCurrent {
			t.Errorf("tool %s SchemaVersion = %q, want current %q", tool.Name, tool.SchemaVersion, SchemaVersionCurrent)
		}
	}
}

// TestValidSchemaVersion covers the format validator: exactly three numeric
// components (major.minor.patch).
func TestValidSchemaVersion(t *testing.T) {
	valid := []string{"1.0.0", "0.1.0", "2.3.4", "10.20.30", "0.0.1"}
	for _, v := range valid {
		if !validSchemaVersion(v) {
			t.Errorf("validSchemaVersion(%q) = false, want true", v)
		}
	}
	invalid := []string{
		"", "1", "1.0", "1.0.0.0", "1.0.x", "v1.0.0",
		"1..0", "1.0.0-alpha", " 1.0.0", "1.0.0 ", "1.0.", ".1.0",
	}
	for _, v := range invalid {
		if validSchemaVersion(v) {
			t.Errorf("validSchemaVersion(%q) = true, want false", v)
		}
	}
}

// TestNegotiateSchemaVersion pins the negotiation rules (P2-003): a supported
// request is honored verbatim; an empty or unsupported request falls back to
// the current catalog version.
func TestNegotiateSchemaVersion(t *testing.T) {
	if got := negotiateSchemaVersion(""); got != SchemaVersionCurrent {
		t.Errorf("negotiateSchemaVersion(\"\") = %q, want current %q", got, SchemaVersionCurrent)
	}
	if got := negotiateSchemaVersion(SchemaVersionV1); got != SchemaVersionV1 {
		t.Errorf("negotiateSchemaVersion(%q) = %q, want %q", SchemaVersionV1, got, SchemaVersionV1)
	}
	if got := negotiateSchemaVersion("9.9.9"); got != SchemaVersionCurrent {
		t.Errorf("negotiateSchemaVersion(\"9.9.9\") = %q, want current %q", got, SchemaVersionCurrent)
	}
	if got := negotiateSchemaVersion("0.0.1"); got != SchemaVersionCurrent {
		t.Errorf("negotiateSchemaVersion(\"0.0.1\") = %q, want current %q", got, SchemaVersionCurrent)
	}
}

// TestInitializeAdvertisesAndNegotiatesSchemaVersion drives the initialize
// handshake through dispatch: the server advertises schemaVersion in the
// response, echoes a supported client request, and falls back to the current
// catalog version for an unsupported one. The negotiated value is then
// surfaced by tools/list.
func TestInitializeAdvertisesAndNegotiatesSchemaVersion(t *testing.T) {
	t.Run("supported_request_is_echoed", func(t *testing.T) {
		req := writeReq("initialize", 1, `{"protocolVersion":"2025-06-18","schemaVersion":"`+SchemaVersionV1+`"}`)
		resps := serveMany(t, req, writeReq("tools/list", 2, ""))
		if len(resps) != 2 {
			t.Fatalf("expected 2 responses, got %d", len(resps))
		}
		initResult, ok := resps[0]["result"].(map[string]any)
		if !ok {
			t.Fatalf("initialize has no result: %v", resps[0])
		}
		if got, _ := initResult["schemaVersion"].(string); got != SchemaVersionV1 {
			t.Errorf("initialize schemaVersion = %q, want %q", got, SchemaVersionV1)
		}
		// The negotiated version must surface in tools/list too.
		listResult, ok := resps[1]["result"].(map[string]any)
		if !ok {
			t.Fatalf("tools/list has no result: %v", resps[1])
		}
		if got, _ := listResult["schemaVersion"].(string); got != SchemaVersionV1 {
			t.Errorf("tools/list schemaVersion = %q, want %q", got, SchemaVersionV1)
		}
	})

	t.Run("unsupported_request_falls_back_to_current", func(t *testing.T) {
		req := writeReq("initialize", 3, `{"protocolVersion":"2025-06-18","schemaVersion":"9.9.9"}`)
		resps := serveMany(t, req)
		if len(resps) != 1 {
			t.Fatalf("expected 1 response, got %d", len(resps))
		}
		initResult, ok := resps[0]["result"].(map[string]any)
		if !ok {
			t.Fatalf("initialize has no result: %v", resps[0])
		}
		if got, _ := initResult["schemaVersion"].(string); got != SchemaVersionCurrent {
			t.Errorf("initialize schemaVersion = %q, want current %q", got, SchemaVersionCurrent)
		}
	})

	t.Run("missing_request_defaults_to_current", func(t *testing.T) {
		req := writeReq("initialize", 4, `{"protocolVersion":"2025-06-18"}`)
		resps := serveMany(t, req)
		if len(resps) != 1 {
			t.Fatalf("expected 1 response, got %d", len(resps))
		}
		initResult, ok := resps[0]["result"].(map[string]any)
		if !ok {
			t.Fatalf("initialize has no result: %v", resps[0])
		}
		if got, _ := initResult["schemaVersion"].(string); got != SchemaVersionCurrent {
			t.Errorf("initialize schemaVersion = %q, want current %q", got, SchemaVersionCurrent)
		}
	})
}

// TestToolsListAdvertisesPerToolSchemaVersion verifies every advertised tool
// carries a schemaVersion field so clients can inspect contracts without an
// initialize round-trip (P2-003).
func TestToolsListAdvertisesPerToolSchemaVersion(t *testing.T) {
	resps := serveMany(t, writeReq("tools/list", 1, ""))
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	result, ok := resps[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list has no result: %v", resps[0])
	}
	toolsList, ok := result["tools"].([]any)
	if !ok || len(toolsList) == 0 {
		t.Fatalf("tools/list has no tools array: %v", result)
	}
	for _, raw := range toolsList {
		tool, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("tool entry is not an object: %v", raw)
		}
		name, _ := tool["name"].(string)
		v, _ := tool["schemaVersion"].(string)
		if v == "" {
			t.Errorf("advertised tool %s is missing schemaVersion", name)
			continue
		}
		if !validSchemaVersion(v) {
			t.Errorf("advertised tool %s has malformed schemaVersion %q", name, v)
		}
	}
}
