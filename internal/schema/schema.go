// Package schema injects strict JSON-schema formatting boundaries into
// prompts and deterministically validates structured output against them —
// so an agent's reply either conforms or comes back with a list of concrete
// violations. Dependency-free subset of JSON Schema (objects, arrays,
// primitives, required, enum, length/range/pattern bounds).
package schema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Schema is a parsed JSON Schema subset.
type Schema struct {
	Type                 string
	Required             []string
	Properties           map[string]*Schema
	Items                *Schema
	Enum                 []any
	MinLength, MaxLength *int
	Min, Max             *float64
	Pattern              *regexp.Regexp
	AdditionalProperties *bool

	raw map[string]any // keeps original JSON for prompt injection
}

// Parse parses a JSON Schema. It returns a Schema usable for both prompt
// injection and output validation.
func Parse(s string) (*Schema, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, fmt.Errorf("invalid schema JSON: %w", err)
	}
	return parseNode(raw), nil
}

func parseNode(m map[string]any) *Schema {
	sc := &Schema{raw: m}
	if t, ok := m["type"].(string); ok {
		sc.Type = t
	}
	if r, ok := m["required"].([]any); ok {
		for _, v := range r {
			if s, ok := v.(string); ok {
				sc.Required = append(sc.Required, s)
			}
		}
	}
	if p, ok := m["properties"].(map[string]any); ok {
		sc.Properties = map[string]*Schema{}
		for k, v := range p {
			if pm, ok := v.(map[string]any); ok {
				sc.Properties[k] = parseNode(pm)
			}
		}
	}
	if it, ok := m["items"].(map[string]any); ok {
		sc.Items = parseNode(it)
	}
	if e, ok := m["enum"].([]any); ok {
		sc.Enum = e
	}
	if v, ok := m["minLength"].(float64); ok {
		n := int(v)
		sc.MinLength = &n
	}
	if v, ok := m["maxLength"].(float64); ok {
		n := int(v)
		sc.MaxLength = &n
	}
	if v, ok := m["min"].(float64); ok {
		sc.Min = &v
	}
	if v, ok := m["max"].(float64); ok {
		sc.Max = &v
	}
	if v, ok := m["pattern"].(string); ok {
		if re, err := regexp.Compile(v); err == nil {
			sc.Pattern = re
		}
	}
	if v, ok := m["additionalProperties"].(bool); ok {
		sc.AdditionalProperties = &v
	}
	return sc
}

// PromptBlock returns the formatting-boundary text to inject into a prompt.
func (s *Schema) PromptBlock() string {
	var b strings.Builder
	b.WriteString("You MUST reply with ONLY raw JSON, no markdown fences, no prose.\n")
	b.WriteString("The JSON must conform exactly to this schema:\n")
	src, _ := json.MarshalIndent(s.raw, "", "  ")
	b.WriteString("```json\n")
	b.WriteString(string(src))
	b.WriteString("\n```\n")
	b.WriteString("Do not add, rename, or omit any field. Missing or extra keys and wrong types are validation failures.\n")
	return b.String()
}

// Validate checks data against the schema and returns a list of human-readable
// violations. An empty (nil) result means the data conforms.
func (s *Schema) Validate(data []byte) []string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return []string{"output is not valid JSON: " + err.Error()}
	}
	return s.check("$", v)
}

func (s *Schema) check(path string, v any) []string {
	var out []string
	if s == nil {
		return nil
	}
	// type
	ok := false
	switch s.Type {
	case "":
		ok = true
	case "object":
		_, ok = v.(map[string]any)
	case "array":
		_, ok = v.([]any)
	case "string":
		_, ok = v.(string)
	case "number":
		_, ok = isNumber(v)
	case "integer":
		f, isNum := isNumber(v)
		ok = isNum && f == float64(int64(f))
	case "boolean":
		_, ok = v.(bool)
	case "null":
		ok = v == nil
	default:
		ok = true
	}
	if !ok {
		out = append(out, fmt.Sprintf("%s: expected type %s, got %T", path, s.Type, v))
		return out
	}
	if len(s.Enum) > 0 {
		matched := false
		for _, e := range s.Enum {
			if eqJSON(e, v) {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, fmt.Sprintf("%s: value %v not in enum %v", path, v, s.Enum))
		}
	}
	switch s.Type {
	case "string":
		str := ""
		if sval, ok := v.(string); ok {
			str = sval
		}
		if s.MinLength != nil && len(str) < *s.MinLength {
			out = append(out, fmt.Sprintf("%s: shorter than minLength %d", path, *s.MinLength))
		}
		if s.MaxLength != nil && len(str) > *s.MaxLength {
			out = append(out, fmt.Sprintf("%s: longer than maxLength %d", path, *s.MaxLength))
		}
		if s.Pattern != nil && !s.Pattern.MatchString(str) {
			out = append(out, fmt.Sprintf("%s: does not match pattern %s", path, s.Pattern))
		}
	case "number", "integer":
		if f, isNum := isNumber(v); isNum {
			if s.Min != nil && f < *s.Min {
				out = append(out, fmt.Sprintf("%s: %v < min %v", path, f, *s.Min))
			}
			if s.Max != nil && f > *s.Max {
				out = append(out, fmt.Sprintf("%s: %v > max %v", path, f, *s.Max))
			}
		}
	case "array":
		if arr, ok := v.([]any); ok && s.Items != nil {
			for i, item := range arr {
				out = append(out, s.Items.check(fmt.Sprintf("%s[%d]", path, i), item)...)
			}
		}
	case "object":
		obj, _ := v.(map[string]any)
		prefix := path + "."
		if path == "$" {
			prefix = "$."
		}
		// required
		for _, key := range s.Required {
			if _, has := obj[key]; !has {
				out = append(out, fmt.Sprintf("%s%s: missing required field %q", prefix, key, key))
			}
		}
		// additional properties
		if s.AdditionalProperties != nil && !*s.AdditionalProperties {
			var extra []string
			for k := range obj {
				if _, known := s.Properties[k]; !known {
					extra = append(extra, k)
				}
			}
			sort.Strings(extra)
			for _, k := range extra {
				out = append(out, fmt.Sprintf("%s%s: unexpected field %q (additionalProperties=false)", prefix, k, k))
			}
		}
		// recurse
		var keys []string
		for k := range obj {
			if _, known := s.Properties[k]; known {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, s.Properties[k].check(prefix+k, obj[k])...)
		}
	}
	return out
}

func isNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func eqJSON(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}
