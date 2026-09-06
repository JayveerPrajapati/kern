package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PipelineStep defines a single execution stage in kern_compose.
type PipelineStep struct {
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args"`
	Bind    string         `json:"bind,omitempty"`
	OnError string         `json:"on_error,omitempty"` // "stop" (default) | "skip" | "continue"
}

// handleCompose executes a deterministic pipeline of kern MCP tools in sequence.
// Outputs from prior steps can be bound to variables ($varname) and interpolated into
// downstream step arguments, collapsing 4-8 round-trips into a single MCP request.
func (s *Server) handleCompose(ctx context.Context, args map[string]any) (string, error) {
	stepsRaw, ok := args["pipeline"]
	if !ok || stepsRaw == nil {
		return "", fmt.Errorf("pipeline argument is required")
	}

	// Unmarshal pipeline steps from various accepted shapes
	var steps []PipelineStep
	switch v := stepsRaw.(type) {
	case string:
		if err := json.Unmarshal([]byte(v), &steps); err != nil {
			return "", fmt.Errorf("failed to parse pipeline JSON string: %w", err)
		}
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("failed to marshal pipeline: %w", err)
		}
		if err := json.Unmarshal(data, &steps); err != nil {
			return "", fmt.Errorf("failed to unmarshal pipeline steps: %w", err)
		}
	}

	if len(steps) == 0 {
		return "", fmt.Errorf("pipeline must contain at least one step")
	}

	// Prevent recursive bomb: kern_compose cannot compose kern_compose
	for i, step := range steps {
		if step.Tool == "kern_compose" {
			return "", fmt.Errorf("pipeline step %d: recursive kern_compose invocation is not allowed", i+1)
		}
	}

	timeoutSec := 60
	if tStr := argString(args, "timeout"); tStr != "" {
		var sec int
		if _, err := fmt.Sscanf(tStr, "%d", &sec); err == nil && sec > 0 {
			timeoutSec = sec
		}
	}

	bindings := make(map[string]string)
	var outputParts []string
	pipelineStart := time.Now()

	for i, step := range steps {
		stepNum := i + 1
		if step.Tool == "" {
			return "", fmt.Errorf("pipeline step %d: tool name is required", stepNum)
		}

		// Interpolate variables in arguments
		resolvedArgs := interpolateArgs(step.Args, bindings)

		stepCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		stepStart := time.Now()
		stepResult, err := s.runTool(stepCtx, fmt.Sprintf("compose-step-%d", stepNum), step.Tool, resolvedArgs)
		cancel()
		duration := time.Since(stepStart)

		if err != nil {
			errNotice := fmt.Sprintf("=== Step %d: %s (FAILED in %v) ===\nError: %v", stepNum, step.Tool, duration.Round(time.Millisecond), err)
			outputParts = append(outputParts, errNotice)

			switch strings.ToLower(strings.TrimSpace(step.OnError)) {
			case "continue", "skip":
				continue
			default:
				return strings.Join(outputParts, "\n\n") + fmt.Sprintf("\n\nPipeline aborted at step %d: %v", stepNum, err), fmt.Errorf("step %d (%s) failed: %w", stepNum, step.Tool, err)
			}
		}

		if step.Bind != "" {
			bindKey := strings.TrimPrefix(step.Bind, "$")
			bindings["$"+bindKey] = strings.TrimSpace(stepResult)
			bindings[bindKey] = strings.TrimSpace(stepResult)
		}

		header := fmt.Sprintf("=== Step %d: %s (OK in %v) ===", stepNum, step.Tool, duration.Round(time.Millisecond))
		if step.Bind != "" {
			header += fmt.Sprintf(" [bound to $%s]", strings.TrimPrefix(step.Bind, "$"))
		}
		outputParts = append(outputParts, header+"\n"+stepResult)
	}

	totalDuration := time.Since(pipelineStart).Round(time.Millisecond)
	summary := fmt.Sprintf("\n[kern_compose: %d/%d steps completed in %v]", len(steps), len(steps), totalDuration)
	return strings.Join(outputParts, "\n\n") + summary, nil
}

func interpolateArgs(args map[string]any, bindings map[string]string) map[string]any {
	if len(args) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = interpolateValue(v, bindings)
	}
	return out
}

func interpolateValue(val any, bindings map[string]string) any {
	switch v := val.(type) {
	case string:
		// Direct exact match
		if trimmed := strings.TrimSpace(v); strings.HasPrefix(trimmed, "$") {
			if replacement, found := bindings[trimmed]; found {
				return replacement
			}
			if replacement, found := bindings[strings.TrimPrefix(trimmed, "$")]; found {
				return replacement
			}
		}
		// Substring replacement
		res := v
		for bKey, bVal := range bindings {
			if strings.HasPrefix(bKey, "$") {
				res = strings.ReplaceAll(res, bKey, bVal)
			}
		}
		return res
	case map[string]any:
		return interpolateArgs(v, bindings)
	case []any:
		arr := make([]any, len(v))
		for i, item := range v {
			arr[i] = interpolateValue(item, bindings)
		}
		return arr
	default:
		return v
	}
}
