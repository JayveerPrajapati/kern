package langparse

import (
	"regexp"
	"strings"
)

var (
	exportDefaultDeclaration = regexp.MustCompile(`^export\s+default\s+(?:abstract\s+)?(?:async\s+)?(?:function\s*\*?|class)\s+([A-Za-z_$][\w$]*)`)
	exportDefaultIdentifier  = regexp.MustCompile(`^export\s+default\s+([A-Za-z_$][\w$]*)\s*;?$`)
	exportNamedDeclaration   = regexp.MustCompile(`^export\s+(?:abstract\s+)?(?:async\s+)?(?:function\s*\*?|class|const|let|var|enum)\s+([A-Za-z_$][\w$]*)`)
	exportStarFrom           = regexp.MustCompile(`^export\s+\*\s+from\s+['"]([^'"]+)['"]\s*;?$`)
	exportBraces             = regexp.MustCompile(`^export\s+(type\s+)?\{(.*)\}\s*(?:from\s+['"]([^'"]+)['"])?\s*;?$`)
	exportBracesStart        = regexp.MustCompile(`^export\s+(?:type\s+)?\{`)
)

var scriptKeywordsNotDefaults = map[string]bool{"function": true, "class": true, "async": true, "abstract": true, "new": true, "await": true, "null": true, "true": true, "false": true, "undefined": true}

func IsScriptLanguage(language string) bool {
	return language == "javascript" || language == "typescript"
}

// foreignExports extracts module-scope default exports and reexports from
// JavaScript/TypeScript source. Multi-line `export { … } from` is joined.
func foreignExports(source []byte, language string) []ExportEdge {
	if !IsScriptLanguage(language) {
		return nil
	}
	spec := specs[language]
	if spec == nil {
		return nil
	}
	analysis := Analyze(source, spec)
	var exports []ExportEdge
	depth, pendingLines := 0, 0
	pending := ""
	for position, original := range analysis.lines {
		clean := analysis.clean[position]
		line := strings.TrimSpace(original)
		atModuleScope := depth == 0
		opens, closes := braceDelta(clean)
		depth += opens - closes
		if analysis.com[position] || strings.TrimSpace(clean) == "" {
			continue
		}
		if pending != "" {
			pending += " " + line
			pendingLines++
			if !strings.Contains(line, "}") {
				if pendingLines > 200 {
					pending = ""
				}
				continue
			}
			line, pending = pending, ""
		} else {
			if !atModuleScope || !strings.HasPrefix(line, "export") {
				continue
			}
			if exportBracesStart.MatchString(line) && !strings.Contains(line, "}") {
				pending, pendingLines = line, 0
				continue
			}
		}
		exports = append(exports, parseExportStatement(line)...)
	}
	return exports
}

func parseExportStatement(line string) []ExportEdge {
	if match := exportDefaultDeclaration.FindStringSubmatch(line); match != nil {
		return []ExportEdge{{Kind: "default", Symbol: match[1]}}
	}
	if match := exportDefaultIdentifier.FindStringSubmatch(line); match != nil && !scriptKeywordsNotDefaults[match[1]] {
		return []ExportEdge{{Kind: "default", Symbol: match[1]}}
	}
	if match := exportNamedDeclaration.FindStringSubmatch(line); match != nil {
		return []ExportEdge{{Kind: "named", Name: match[1], Symbol: match[1]}}
	}
	if match := exportStarFrom.FindStringSubmatch(line); match != nil {
		return []ExportEdge{{Kind: "star", Path: match[1]}}
	}
	match := exportBraces.FindStringSubmatch(line)
	if match == nil || match[1] != "" {
		return nil
	}
	var exports []ExportEdge
	for _, member := range strings.Split(match[2], ",") {
		if strings.HasPrefix(strings.TrimSpace(member), "type ") {
			continue
		}
		parts := strings.Fields(member)
		if len(parts) != 1 && (len(parts) != 3 || parts[1] != "as") {
			continue
		}
		name := parts[0]
		if len(parts) == 3 {
			name = parts[2]
		}
		switch {
		case match[3] != "":
			exports = append(exports, ExportEdge{Kind: "named", Name: name, Symbol: parts[0], Path: match[3]})
		case name == "default" && parts[0] != "default":
			exports = append(exports, ExportEdge{Kind: "default", Symbol: parts[0]})
		default:
			exports = append(exports, ExportEdge{Kind: "named", Name: name, Symbol: parts[0]})
		}
	}
	return exports
}
