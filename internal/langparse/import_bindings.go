package langparse

import (
	"regexp"
	"strings"
)

var pythonFromImport = regexp.MustCompile(`^from\s+([.\w]+)\s+import\s+(.+)$`)
var scriptFromImport = regexp.MustCompile(`^import\s+(.+?)\s+from\s+['"]([^'"]+)['"]\s*;?$`)

func foreignNamedImports(source []byte, language string) []ImportEdge {
	spec := specs[language]
	if spec == nil {
		return nil
	}
	analysis := Analyze(source, spec)
	var imports []ImportEdge
	depth := 0
	pending, pendingLines := "", 0
	for position, original := range analysis.lines {
		clean := analysis.clean[position]
		line := strings.TrimSpace(original)
		atModuleScope := depth == 0
		if language == "python" {
			atModuleScope = original == strings.TrimLeft(original, " \t")
		}
		opens, closes := braceDelta(clean)
		depth += opens - closes
		if analysis.com[position] || strings.TrimSpace(clean) == "" {
			continue
		}
		if pending != "" {
			pendingLines++
			if language == "python" {
				pending += " " + strings.TrimSpace(strings.SplitN(clean, "#", 2)[0])
			} else {
				pending += " " + line
			}
			if !importStatementComplete(language, pending) {
				if pendingLines > 200 {
					pending = ""
				}
				continue
			}
			line, pending = pending, ""
		} else {
			if !atModuleScope {
				continue
			}
			if language == "python" {
				line = strings.TrimSpace(strings.SplitN(clean, "#", 2)[0])
			}
			if importStatementStarts(language, line) {
				pending, pendingLines = line, 0
				continue
			}
		}
		if language == "python" {
			if match := pythonFromImport.FindStringSubmatch(line); match != nil {
				members := strings.TrimSpace(match[2])
				if strings.HasPrefix(members, "(") && !strings.HasSuffix(members, ")") {
					continue
				}
				for _, member := range strings.Split(strings.Trim(members, "()"), ",") {
					parts := strings.Fields(member)
					if len(parts) != 1 && (len(parts) != 3 || parts[1] != "as") {
						continue
					}
					if parts[0] == "*" {
						continue
					}
					alias := parts[0]
					if len(parts) == 3 {
						alias = parts[2]
					}
					imports = append(imports, ImportEdge{Path: match[1], Alias: alias, Symbol: parts[0], Confidence: ConfidenceMedium})
				}
			} else if strings.HasPrefix(line, "import ") {
				for _, member := range strings.Split(strings.TrimPrefix(line, "import "), ",") {
					parts := strings.Fields(member)
					if len(parts) != 1 && (len(parts) != 3 || parts[1] != "as") {
						continue
					}
					alias := parts[0]
					if len(parts) == 3 {
						alias = parts[2]
					}
					imports = append(imports, ImportEdge{Path: parts[0], Alias: alias, Confidence: ConfidenceMedium})
				}
			}
			continue
		}
		match := scriptFromImport.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		clause := strings.TrimSpace(match[1])
		if defaultName, rest := splitDefaultImport(clause); defaultName != "" {
			imports = append(imports, ImportEdge{Path: match[2], Alias: defaultName, Symbol: "default", Confidence: ConfidenceMedium})
			clause = rest
		}
		if strings.HasPrefix(clause, "{") && strings.HasSuffix(clause, "}") {
			for _, member := range strings.Split(strings.Trim(clause, "{}"), ",") {
				if strings.HasPrefix(strings.TrimSpace(member), "type ") {
					continue
				}
				parts := strings.Fields(member)
				if len(parts) != 1 && (len(parts) != 3 || parts[1] != "as") {
					continue
				}
				alias := parts[0]
				if len(parts) == 3 {
					alias = parts[2]
				}
				imports = append(imports, ImportEdge{Path: match[2], Alias: alias, Symbol: parts[0], Confidence: ConfidenceMedium})
			}
		} else if parts := strings.Fields(clause); len(parts) == 3 && parts[0] == "*" && parts[1] == "as" {
			imports = append(imports, ImportEdge{Path: match[2], Alias: parts[2], Confidence: ConfidenceMedium})
		}
	}
	return imports
}

var scriptIdentifier = regexp.MustCompile(`^[A-Za-z_$][\w$]*$`)

// splitDefaultImport separates a leading default binding from the rest of an
// import clause: "Core, { a }" -> ("Core", "{ a }"), "Core" -> ("Core", "").
// Type-only clauses and clauses without a default binding return "".
func splitDefaultImport(clause string) (string, string) {
	head, rest := clause, ""
	if comma := strings.Index(clause, ","); comma >= 0 {
		head, rest = clause[:comma], strings.TrimSpace(clause[comma+1:])
	}
	head = strings.TrimSpace(head)
	if head == "type" || !scriptIdentifier.MatchString(head) {
		return "", clause
	}
	return head, rest
}

func importStatementStarts(language, line string) bool {
	if language == "python" {
		match := pythonFromImport.FindStringSubmatch(line)
		return match != nil && strings.HasPrefix(strings.TrimSpace(match[2]), "(") && !strings.Contains(match[2], ")")
	}
	return strings.HasPrefix(line, "import ") && strings.Contains(line, "{") && !strings.Contains(line, "}")
}

func importStatementComplete(language, statement string) bool {
	if language == "python" {
		return strings.Contains(statement, ")")
	}
	return scriptFromImport.MatchString(statement)
}
