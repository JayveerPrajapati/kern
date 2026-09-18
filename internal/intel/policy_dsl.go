package intel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PolicyRule defines an individual policy constraint.
type PolicyRule struct {
	ID              string   `json:"id"`
	Category        string   `json:"category"`
	Severity        string   `json:"severity"` // "block", "warn", "info"
	Description     string   `json:"description"`
	BannedImports   []string `json:"banned_imports,omitempty"`
	ProtectedPaths  []string `json:"protected_paths,omitempty"`
	BannedPatterns  []string `json:"banned_patterns,omitempty"`
	MaxFilesChanged int      `json:"max_files_changed,omitempty"`
}

// PolicySpec is a collection of evaluated rules.
type PolicySpec struct {
	Name  string       `json:"name"`
	Rules []PolicyRule `json:"rules"`
}

// PolicyViolation details a violated policy constraint.
type PolicyViolation struct {
	RuleID      string `json:"rule_id"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Target      string `json:"target,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

// PolicyEvaluation is the verdict returned after evaluating an action against policy.
type PolicyEvaluation struct {
	Allowed     bool              `json:"allowed"`
	Violations  []PolicyViolation `json:"violations"`
	Warnings    []PolicyViolation `json:"warnings"`
	RulesPassed int               `json:"rules_passed"`
	TotalRules  int               `json:"total_rules"`
}

// EvaluatePolicy checks target files, diff text, and imports against the given policy specification.
func EvaluatePolicy(spec PolicySpec, files []string, diffContent string, imports []string) PolicyEvaluation {
	eval := PolicyEvaluation{
		Allowed:    true,
		Violations: []PolicyViolation{},
		Warnings:   []PolicyViolation{},
		TotalRules: len(spec.Rules),
	}

	for _, rule := range spec.Rules {
		rulePassed := true
		sev := strings.ToLower(rule.Severity)
		if sev == "" {
			sev = "block"
		}

		// 1. Check MaxFilesChanged
		if rule.MaxFilesChanged > 0 && len(files) > rule.MaxFilesChanged {
			v := PolicyViolation{
				RuleID:      rule.ID,
				Category:    rule.Category,
				Severity:    sev,
				Message:     fmt.Sprintf("Changed %d files, exceeding policy maximum of %d", len(files), rule.MaxFilesChanged),
				Target:      fmt.Sprintf("%d files", len(files)),
				Remediation: "Split your changes into smaller, focused pull requests or commits.",
			}
			recordViolation(&eval, v, sev)
			rulePassed = false
		}

		// 2. Check ProtectedPaths
		for _, prot := range rule.ProtectedPaths {
			for _, f := range files {
				matched, _ := filepath.Match(prot, f)
				if matched || strings.HasPrefix(f, strings.TrimPrefix(prot, "/")) || strings.Contains(f, strings.Trim(prot, "*")) {
					v := PolicyViolation{
						RuleID:      rule.ID,
						Category:    rule.Category,
						Severity:    sev,
						Message:     fmt.Sprintf("File %q matches protected path pattern %q", f, prot),
						Target:      f,
						Remediation: "Modifications to protected paths require architectural approval.",
					}
					recordViolation(&eval, v, sev)
					rulePassed = false
				}
			}
		}

		// 3. Check BannedImports
		for _, banned := range rule.BannedImports {
			for _, imp := range imports {
				if imp == banned || strings.HasPrefix(imp, banned+"/") {
					v := PolicyViolation{
						RuleID:      rule.ID,
						Category:    rule.Category,
						Severity:    sev,
						Message:     fmt.Sprintf("Import %q is disallowed by policy", imp),
						Target:      imp,
						Remediation: fmt.Sprintf("Use approved alternatives instead of %q.", banned),
					}
					recordViolation(&eval, v, sev)
					rulePassed = false
				}
			}
			// Also search in diffContent for new imports e.g. `+import "unsafe"`
			if diffContent != "" {
				impRegex := regexp.MustCompile(`(?m)^\+\s*.*"` + regexp.QuoteMeta(banned) + `.*"`)
				if impRegex.MatchString(diffContent) {
					v := PolicyViolation{
						RuleID:      rule.ID,
						Category:    rule.Category,
						Severity:    sev,
						Message:     fmt.Sprintf("Diff introduces banned import %q", banned),
						Target:      banned,
						Remediation: fmt.Sprintf("Remove dependency on %q.", banned),
					}
					recordViolation(&eval, v, sev)
					rulePassed = false
				}
			}
		}

		// 4. Check BannedPatterns
		for _, pat := range rule.BannedPatterns {
			if pat == "" {
				continue
			}
			re, err := regexp.Compile(`(?m)^\+.*` + pat)
			if err == nil && diffContent != "" {
				if re.MatchString(diffContent) {
					v := PolicyViolation{
						RuleID:      rule.ID,
						Category:    rule.Category,
						Severity:    sev,
						Message:     fmt.Sprintf("Diff introduces banned pattern %q", pat),
						Target:      pat,
						Remediation: fmt.Sprintf("Refactor code to avoid banned pattern %q.", pat),
					}
					recordViolation(&eval, v, sev)
					rulePassed = false
				}
			}
		}

		if rulePassed {
			eval.RulesPassed++
		}
	}

	eval.Allowed = len(eval.Violations) == 0
	return eval
}

func recordViolation(eval *PolicyEvaluation, v PolicyViolation, severity string) {
	if severity == "block" || severity == "error" {
		eval.Violations = append(eval.Violations, v)
	} else {
		eval.Warnings = append(eval.Warnings, v)
	}
}

// ParsePolicySpec parses JSON or lightweight line-based YAML into a PolicySpec.
//
// The value may also name a policy FILE (the `--policy FILE` CLI form): an
// existing file is read and parsed; a value that looks like a path but does
// not exist is a hard error. This closes the fail-open hole where a policy
// file was never read and silently evaluated as a vacuous 0-rule ALLOWED —
// a requested policy must load, or fail loud (F7).
func ParsePolicySpec(content string) (PolicySpec, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return PolicySpec{}, fmt.Errorf("empty policy")
	}

	resolved, fromFile, err := resolvePolicyInput(content)
	if err != nil {
		return PolicySpec{}, err
	}
	content = resolved

	spec := PolicySpec{}
	if strings.HasPrefix(content, "{") {
		err = json.Unmarshal([]byte(content), &spec)
	} else {
		spec = parsePolicyYAML(content)
	}
	if err != nil {
		return spec, err
	}
	// Never allow a policy FILE request to fail open: a file that parses to
	// zero rules would make EvaluatePolicy vacuously ALLOW everything.
	if fromFile && len(spec.Rules) == 0 {
		return spec, fmt.Errorf("policy file contains no rules; refusing to evaluate a vacuous ALLOWED")
	}
	return spec, nil
}

// resolvePolicyInput decides whether the value is an existing policy file, a
// path-like value that must fail loud, or inline policy text. It returns the
// text to parse and whether it came from a file.
func resolvePolicyInput(content string) (string, bool, error) {
	// 1. A value naming an existing regular file is read as the policy.
	if fi, err := os.Stat(content); err == nil && fi.Mode().IsRegular() {
		data, rerr := os.ReadFile(content)
		if rerr != nil {
			return "", true, fmt.Errorf("policy file not found: %s (%v)", content, rerr)
		}
		return string(data), true, nil
	}
	// 2. A value that looks like a file path but does not exist is a hard
	// error — never silently parse it as inline text and allow everything.
	if looksLikePolicyPath(content) {
		return "", false, fmt.Errorf("policy file not found: %s", content)
	}
	// 3. Pure inline text keeps the historical behavior.
	return content, false, nil
}

// looksLikePolicyPath reports whether s is a plausible policy file path
// rather than inline policy text: a single line that is not a JSON document
// and not a YAML rule line, containing a path separator or a policy file
// extension (.json/.yaml/.yml). Multi-line text is always inline.
func looksLikePolicyPath(s string) bool {
	if strings.ContainsAny(s, "\n\r") {
		return false // multi-line: inline policy text
	}
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		return false // inline JSON
	}
	if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "name:") || strings.HasPrefix(s, "id:") {
		return false // single-line YAML rule
	}
	lower := strings.ToLower(s)
	if strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") {
		return true
	}
	return strings.Contains(s, "/")
}

// parsePolicyYAML parses lightweight line-based YAML into a PolicySpec.
func parsePolicyYAML(content string) PolicySpec {
	spec := PolicySpec{Name: "custom_policy"}
	lines := strings.Split(content, "\n")
	var curRule *PolicyRule

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- id:") {
			if curRule != nil {
				spec.Rules = append(spec.Rules, *curRule)
			}
			curRule = &PolicyRule{ID: strings.TrimSpace(strings.TrimPrefix(trimmed, "- id:"))}
			continue
		}
		if curRule == nil {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			switch k {
			case "category":
				curRule.Category = v
			case "severity":
				curRule.Severity = v
			case "description":
				curRule.Description = v
			case "banned_import":
				curRule.BannedImports = append(curRule.BannedImports, v)
			case "protected_path":
				curRule.ProtectedPaths = append(curRule.ProtectedPaths, v)
			case "banned_pattern":
				curRule.BannedPatterns = append(curRule.BannedPatterns, v)
			}
		} else if strings.HasPrefix(trimmed, "- ") {
			item := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")), `"'`)
			if len(curRule.BannedImports) > 0 {
				curRule.BannedImports = append(curRule.BannedImports, item)
			} else if len(curRule.ProtectedPaths) > 0 {
				curRule.ProtectedPaths = append(curRule.ProtectedPaths, item)
			}
		}
	}
	if curRule != nil {
		spec.Rules = append(spec.Rules, *curRule)
	}

	return spec
}
