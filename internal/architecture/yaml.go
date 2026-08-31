package architecture

import (
	"fmt"
	"strconv"
	"strings"
)

// --- minimal YAML parser (fixed schema subset, stdlib-only) ---
// Handles the YAML shapes this package emits: block mappings, block sequences,
// scalar values (unquoted, single/double quoted, bool/int), flow lists like
// [a, b], and nested blocks via indentation. It is intentionally NOT a general
// YAML implementation — anything outside the fixed schema fails closed.

type yamlLine struct {
	indent  int
	content string
}

// maxFlowDepth bounds YAML nesting (block and flow) to prevent stack overflow
// on pathological inputs like deeply nested "[[[…]]]" flow lists.
const maxFlowDepth = 64

func parseYAML(data []byte) (interface{}, error) {
	var lines []yamlLine
	for _, raw := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		content := strings.TrimSpace(raw)
		if strings.HasPrefix(content, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		lines = append(lines, yamlLine{indent: indent, content: content})
	}
	if len(lines) == 0 {
		return nil, nil
	}
	pos := 0
	return parseBlock(lines, &pos, lines[0].indent, 0)
}

func parseBlock(lines []yamlLine, pos *int, indent, depth int) (interface{}, error) {
	if depth > maxFlowDepth {
		return nil, fmt.Errorf("yaml nesting exceeds depth limit of %d", maxFlowDepth)
	}
	if *pos >= len(lines) {
		return nil, nil
	}
	if isSeqItem(lines[*pos].content) {
		return parseSeq(lines, pos, indent, depth+1)
	}
	return parseMap(lines, pos, indent, depth+1)
}

func parseMap(lines []yamlLine, pos *int, indent, depth int) (map[string]interface{}, error) {
	if depth > maxFlowDepth {
		return nil, fmt.Errorf("yaml nesting exceeds depth limit of %d", maxFlowDepth)
	}
	m := map[string]interface{}{}
	for *pos < len(lines) {
		ln := lines[*pos]
		if ln.indent < indent {
			break
		}
		if ln.indent > indent {
			return nil, fmt.Errorf("unexpected indentation at %q", ln.content)
		}
		if isSeqItem(ln.content) {
			break
		}
		key, val, hasVal := splitKV(ln.content)
		if key == "" {
			return nil, fmt.Errorf("invalid mapping key: %q", ln.content)
		}
		*pos++
		if !hasVal || val == "" {
			childIndent := nextIndent(lines, *pos)
			if childIndent <= indent {
				m[key] = nil
				continue
			}
			child, err := parseBlock(lines, pos, childIndent, depth+1)
			if err != nil {
				return nil, err
			}
			m[key] = child
			continue
		}
		v, err := parseScalar(val, depth+1)
		if err != nil {
			return nil, err
		}
		m[key] = v
	}
	return m, nil
}

func parseSeq(lines []yamlLine, pos *int, indent, depth int) ([]interface{}, error) {
	if depth > maxFlowDepth {
		return nil, fmt.Errorf("yaml nesting exceeds depth limit of %d", maxFlowDepth)
	}
	var items []interface{}
	for *pos < len(lines) {
		ln := lines[*pos]
		if ln.indent < indent {
			break
		}
		if ln.indent > indent {
			return nil, fmt.Errorf("bad indentation in sequence at %q", ln.content)
		}
		if !isSeqItem(ln.content) {
			break
		}
		rest := strings.TrimSpace(strings.TrimPrefix(ln.content, "-"))
		if rest == "" {
			*pos++
			childIndent := nextIndent(lines, *pos)
			if childIndent <= indent {
				items = append(items, nil)
				continue
			}
			child, err := parseBlock(lines, pos, childIndent, depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, child)
			continue
		}
		// Inline map item: "- key: value" followed by more keys at deeper indent.
		if key, val, hasVal := splitKV(rest); hasVal && key != "" {
			*pos++
			item := map[string]interface{}{}
			sv, err := parseScalar(val, depth+1)
			if err != nil {
				return nil, err
			}
			item[key] = sv
			itemIndent := ln.indent + 2
			for *pos < len(lines) {
				nxt := lines[*pos]
				if nxt.indent < itemIndent {
					break
				}
				if nxt.indent > itemIndent {
					return nil, fmt.Errorf("bad indentation in map item at %q", nxt.content)
				}
				if isSeqItem(nxt.content) {
					break
				}
				k2, v2, h2 := splitKV(nxt.content)
				if k2 == "" {
					break
				}
				*pos++
				if !h2 || v2 == "" {
					ci := nextIndent(lines, *pos)
					if ci <= itemIndent {
						item[k2] = nil
						continue
					}
					child, err := parseBlock(lines, pos, ci, depth+1)
					if err != nil {
						return nil, err
					}
					item[k2] = child
					continue
				}
				sv2, err := parseScalar(v2, depth+1)
				if err != nil {
					return nil, err
				}
				item[k2] = sv2
			}
			items = append(items, item)
			continue
		}
		// Nested sequence item "- - a" — flatten by recursing.
		if isSeqItem(rest) {
			*pos++
			childIndent := nextIndent(lines, *pos)
			child, err := parseSeq(lines, pos, childIndent, depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, child)
			continue
		}
		// Scalar item.
		*pos++
		sv, err := parseScalar(rest, depth+1)
		if err != nil {
			return nil, err
		}
		items = append(items, sv)
	}
	return items, nil
}

func parseScalar(s string, depth int) (interface{}, error) {
	if depth > maxFlowDepth {
		return nil, fmt.Errorf("yaml flow nesting exceeds depth limit of %d", maxFlowDepth)
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		if !strings.HasSuffix(s, "]") {
			return nil, fmt.Errorf("unterminated flow list: %q", s)
		}
		inner := strings.TrimSpace(s[1 : len(s)-1])
		if inner == "" {
			return []interface{}{}, nil
		}
		var out []interface{}
		for _, p := range splitFlowList(inner) {
			v, err := parseScalar(p, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			return unquoteDouble(s[1 : len(s)-1]), nil
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return s[1 : len(s)-1], nil
		}
	}
	switch s {
	case "true", "True", "TRUE":
		return true, nil
	case "false", "False", "FALSE":
		return false, nil
	case "null", "Null", "NULL", "~":
		return nil, nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, nil
	}
	return s, nil
}

func splitFlowList(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func unquoteDouble(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// splitKV splits a "key: value" line at its first colon.
func splitKV(s string) (key, val string, hasVal bool) {
	idx := strings.Index(s, ":")
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:]), true
}

func isSeqItem(s string) bool {
	return s == "-" || strings.HasPrefix(s, "- ")
}

func nextIndent(lines []yamlLine, pos int) int {
	if pos < len(lines) {
		return lines[pos].indent
	}
	return 0
}
