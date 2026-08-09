package intel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderViolationsSARIF(t *testing.T) {
	violations := []Violation{
		{CallerFile: "client/client.go", CalleeFile: "lib/lib.go", Symbol: "Public", Line: 12, RuleFrom: "client", RuleTo: "lib"},
		{CallerFile: "client/imports.go", CalleeFile: "db/db.go", RuleFrom: "client", RuleTo: "db"},
	}
	out := RenderViolationsSARIF(violations, "v0.5.1")

	var doc struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name    string `json:"name"`
					Version string `json:"version"`
					Rules   []struct {
						ID string `json:"id"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID    string `json:"ruleId"`
				Level     string `json:"level"`
				Locations []struct {
					Physical struct {
						ArtifactLocation map[string]any `json:"artifactLocation"`
						Region           struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
			Invocations []struct {
				ExecutionSuccessful bool `json:"executionSuccessful"`
			} `json:"invocations"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if doc.Version != "2.1.0" {
		t.Errorf("expected sarif version 2.1.0, got %s", doc.Version)
	}
	run := doc.Runs[0]
	if run.Tool.Driver.Name != "kern" || run.Tool.Driver.Version != "v0.5.1" {
		t.Errorf("wrong tool driver: %+v", run.Tool.Driver)
	}
	if len(run.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(run.Results))
	}
	// Rules are deduped to distinct (from,to) pairs.
	if len(run.Tool.Driver.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d: %+v", len(run.Tool.Driver.Rules), run.Tool.Driver.Rules)
	}
	if !run.Invocations[0].ExecutionSuccessful {
		t.Error("clean run that found crossings must still report executionSuccessful=true")
	}
	r0 := run.Results[0]
	if r0.RuleID != "kern/boundary/forbid/client/lib" {
		t.Errorf("wrong ruleId: %s", r0.RuleID)
	}
	if r0.Level != "error" {
		t.Errorf("expected level error, got %s", r0.Level)
	}
	if r0.Locations[0].Physical.ArtifactLocation["uri"] != "client/client.go" {
		t.Errorf("wrong artifact uri: %v", r0.Locations[0].Physical.ArtifactLocation)
	}
	if r0.Locations[0].Physical.Region.StartLine != 12 {
		t.Errorf("expected region startLine 12, got %d", r0.Locations[0].Physical.Region.StartLine)
	}
	// Second violation has no line -> no region.
	if r1 := run.Results[1]; len(r1.Locations) == 0 || r1.Locations[0].Physical.Region.StartLine != 0 {
		t.Errorf("line-less violation must carry no region, got %+v", r1.Locations)
	}
	if !strings.Contains(out, "client -> lib") {
		t.Error("message should name the crossing")
	}
}

func TestRenderViolationsSARIFEmpty(t *testing.T) {
	out := RenderViolationsSARIF(nil, "dev")
	if !strings.Contains(out, `"results": []`) {
		t.Errorf("empty violations must yield empty results array, got: %s", out)
	}
}
