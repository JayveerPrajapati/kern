package intel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ViolationRuleID maps a boundary violation to a stable SARIF rule identifier.
// Rules are keyed on the forbidden (from,to) pair so CI baseline tracking is
// stable across runs.
func ViolationRuleID(v Violation) string {
	return fmt.Sprintf("kern/boundary/forbid/%s/%s", v.RuleFrom, v.RuleTo)
}

// RenderViolationsSARIF renders boundary violations as a SARIF 2.1.0 report for
// CI consumers (GitHub code scanning, Azure DevOps, etc.). One result per
// violation; rules are deduplicated into tool.driver.rules. The run always
// reports executionSuccessful=true: a clean guard run that found crossings is a
// finding, not a tool failure.
func RenderViolationsSARIF(violations []Violation, version string) string {
	type region struct {
		StartLine int `json:"startLine,omitempty"`
	}
	type physicalLocation struct {
		ArtifactLocation map[string]any `json:"artifactLocation"`
		Region           region         `json:"region,omitempty"`
	}
	type location struct {
		Physical physicalLocation `json:"physicalLocation"`
	}
	type rule struct {
		ID               string            `json:"id"`
		ShortDescription map[string]string `json:"shortDescription"`
		FullDescription  map[string]string `json:"fullDescription"`
	}
	type result struct {
		RuleID     string         `json:"ruleId"`
		Level      string         `json:"level"`
		Message    map[string]any `json:"message"`
		Locations  []location     `json:"locations"`
		Properties map[string]any `json:"properties,omitempty"`
	}

	ruleSeen := map[string]bool{}
	var rules []rule
	results := make([]result, 0, len(violations))
	for _, v := range violations {
		id := ViolationRuleID(v)
		if !ruleSeen[id] {
			ruleSeen[id] = true
			rules = append(rules, rule{
				ID:               id,
				ShortDescription: map[string]string{"text": fmt.Sprintf("forbidden dependency %s -> %s", v.RuleFrom, v.RuleTo)},
				FullDescription:  map[string]string{"text": fmt.Sprintf("Boundary rule forbids %s importing %s.", v.RuleFrom, v.RuleTo)},
			})
		}
		msg := map[string]any{
			"text": fmt.Sprintf("Forbidden boundary crossing: %s -> %s (rule %s -> %s forbid)", v.CallerFile, v.CalleeFile, v.RuleFrom, v.RuleTo),
		}
		loc := location{Physical: physicalLocation{ArtifactLocation: map[string]any{"uri": v.CallerFile}}}
		if v.Line > 0 {
			loc.Physical.Region = region{StartLine: v.Line}
		}
		props := map[string]any{
			"kern/ruleFrom": v.RuleFrom,
			"kern/ruleTo":   v.RuleTo,
		}
		if v.Symbol != "" {
			props["kern/symbol"] = v.Symbol
		}
		results = append(results, result{
			RuleID:     id,
			Level:      "error",
			Message:    msg,
			Locations:  []location{loc},
			Properties: props,
		})
	}

	doc := map[string]any{
		"$schema": "https://schemastore.azurewebsites.net/schemas/json/sarif-2.1.0-rtm.5.json",
		"version": "2.1.0",
		"runs": []any{
			map[string]any{
				"tool": map[string]any{
					"driver": map[string]any{
						"name":    "kern",
						"version": version,
						"rules":   rules,
					},
				},
				"results": results,
				"invocations": []any{
					map[string]any{"executionSuccessful": true},
				},
			},
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return "{}"
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
