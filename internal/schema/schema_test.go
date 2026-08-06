package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

const userSchema = `{
	"type": "object",
	"additionalProperties": false,
	"required": ["name", "age", "tags"],
	"properties": {
		"name":   {"type": "string", "minLength": 2},
		"age":    {"type": "integer", "min": 0, "max": 150},
		"tags":   {"type": "array", "items": {"type": "string", "enum": ["admin", "dev", "ops"]}},
		"active": {"type": "boolean"}
	}
}`

func parse(t *testing.T) *Schema {
	t.Helper()
	s, err := Parse(userSchema)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return s
}

func TestValid(t *testing.T) {
	v := parse(t)
	vs := v.Validate([]byte(`{"name":"alice","age":30,"tags":["dev"]}`))
	if len(vs) != 0 {
		t.Fatalf("expected no violations, got %v", vs)
	}
}

func TestViolations(t *testing.T) {
	v := parse(t)
	vs := v.Validate([]byte(`{"name":"a","age":200,"tags":["root"],"active":"yes","extra":1}`))
	if len(vs) == 0 {
		t.Fatal("expected violations, got none")
	}
	for _, vi := range vs {
		if vi == "output is not valid JSON" {
			t.Fatalf("unexpected json error: %v", vs)
		}
	}
	found := map[string]bool{}
	for _, vi := range vs {
		found[vi] = true
	}
	for _, want := range []string{
		`$.extra: unexpected field "extra" (additionalProperties=false)`,
		"$.active: expected type boolean, got string",
		"$.age: 200 > max 150",
		"$.name: shorter than minLength 2",
		"$.tags[0]: value root not in enum [admin dev ops]",
	} {
		if !found[want] {
			t.Fatalf("missing violation %q; got %v", want, vs)
		}
	}
}

func TestInvalidJSON(t *testing.T) {
	v := parse(t)
	vs := v.Validate([]byte(`{oops`))
	if len(vs) == 0 || vs[0] != "output is not valid JSON: invalid character 'o' looking for beginning of object key string" {
		t.Fatalf("expected JSON error, got %v", vs)
	}
}

func TestPromptBlockEmbedsSchema(t *testing.T) {
	v := parse(t)
	block := v.PromptBlock()
	start := strings.Index(block, "```json\n")
	if start < 0 {
		t.Fatalf("no json fence in block: %q", block)
	}
	end := strings.Index(block[start+8:], "\n```")
	if end < 0 {
		t.Fatalf("no closing fence in block: %q", block)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(block[start+8:start+8+end]), &decoded); err != nil {
		t.Fatalf("embedded schema not valid json: %v", err)
	}
	if _, ok := decoded["required"]; !ok {
		t.Fatalf("embedded schema missing required: %v", decoded)
	}
}

func TestEnumAndInteger(t *testing.T) {
	v := parse(t)
	vs := v.Validate([]byte(`{"name":"bob","age":30.5,"tags":["dev"]}`))
	found := false
	for _, vi := range vs {
		if vi == "$.age: expected type integer, got float64" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected integer violation, got %v", vs)
	}
}
