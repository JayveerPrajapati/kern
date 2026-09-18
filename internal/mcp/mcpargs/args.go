// Package args owns the tool-argument parsing helpers shared by every MCP
// handler family. Handlers receive JSON-decoded arguments as
// map[string]any; these helpers convert them to typed values leniently
// (native types pass through, stringly-typed clients are tolerated) but
// fail loud on malformed input (a typo'd number must surface as an error,
// not a silent default).
//
// internal/mcp keeps unexported wrappers (argString et al.) so existing
// handler call sites compile unchanged; extracted handler families import
// this package directly.
package mcpargs

import (
	"fmt"
	"strconv"
	"strings"
)

// ArgString reads an optional string tool argument, trimmed. Missing or nil
// returns "".
func ArgString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// ArgStrings reads an optional array-of-strings tool argument. Accepts a
// []any / []string (MCP JSON arrays) and, for lenient clients that pass
// everything as a single string, a comma- or whitespace-separated list.
// Values are trimmed and empty entries dropped; missing/nil returns nil.
func ArgStrings(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	var out []string
	switch t := v.(type) {
	case []string:
		for _, s := range t {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case []any:
		for _, e := range t {
			if s := strings.TrimSpace(fmt.Sprintf("%v", e)); s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, s := range strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// ArgBool reads an optional boolean tool argument. Accepts native bools and
// the strings "true"/"1" (MCP clients often pass everything as strings).
func ArgBool(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.TrimSpace(t)
		return s == "true" || s == "1"
	case float64:
		return t != 0
	}
	return false
}

// AtoiArg parses an integer tool argument, falling back to def for empty
// input. A malformed value is an error, not a silent default, so a typo'd
// number can't quietly zero out a limit or mis-size a buffer.
func AtoiArg(v string, def int) (int, error) {
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", v)
	}
	return n, nil
}

// ArgStringWithAliases reads a string tool argument, checking key first and
// falling back to common LLM-generated aliases (e.g. symbol_name vs name).
func ArgStringWithAliases(args map[string]any, key string, aliases ...string) string {
	if val := ArgString(args, key); val != "" {
		return val
	}
	for _, alias := range aliases {
		if val := ArgString(args, alias); val != "" {
			return val
		}
	}
	return ""
}

// ArgStringsWithAliases reads an array-of-strings argument, checking key first
// and falling back to aliases.
func ArgStringsWithAliases(args map[string]any, key string, aliases ...string) []string {
	if val := ArgStrings(args, key); len(val) > 0 {
		return val
	}
	for _, alias := range aliases {
		if val := ArgStrings(args, alias); len(val) > 0 {
			return val
		}
	}
	return nil
}

// ArgInt reads an integer tool argument from native int, float64, or string,
// checking key and aliases, and falling back to def.
func ArgInt(args map[string]any, key string, def int, aliases ...string) (int, error) {
	keys := append([]string{key}, aliases...)
	for _, k := range keys {
		v, ok := args[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case int:
			return t, nil
		case int64:
			return int(t), nil
		case float64:
			return int(t), nil
		case string:
			if strings.TrimSpace(t) == "" {
				return def, nil
			}
			return AtoiArg(strings.TrimSpace(t), def)
		}
	}
	return def, nil
}
