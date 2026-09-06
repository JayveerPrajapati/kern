package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/memory"
	"github.com/JayveerPrajapati/kern/internal/prompt"
)

// handlePromptFill dynamically renders standardized, token-efficient agent prompts
// with auto-injected context (project map, compact file, relevant memory lessons).
// Eliminates repetitive prompt construction across agent turns while strictly respecting
// context and token hygiene.
func (s *Server) handlePromptFill(ctx context.Context, args map[string]any) (string, error) {
	templateName := argString(args, "template")
	if templateName == "" {
		templates, _ := prompt.List()
		return "", fmt.Errorf("template is required. Available templates: %s", strings.Join(templates, ", "))
	}

	root := resolveRoot(argString(args, "root"))
	task := argString(args, "task")
	file := argString(args, "file")

	// Parse custom slots if passed as JSON or map
	slots := make(map[string]string)
	if rawSlots, ok := args["slots"]; ok && rawSlots != nil {
		switch v := rawSlots.(type) {
		case string:
			_ = json.Unmarshal([]byte(v), &slots)
		case map[string]any:
			for k, val := range v {
				slots[k] = fmt.Sprintf("%v", val)
			}
		}
	}

	// Default standard slot values if not already provided
	if _, ok := slots["ROOT"]; !ok {
		slots["ROOT"] = root
	}
	if _, ok := slots["TASK"]; !ok && task != "" {
		slots["TASK"] = task
	}
	if _, ok := slots["FILE"]; !ok && file != "" {
		slots["FILE"] = file
	}

	// Auto-inject project layout summary (MAP) if empty
	if _, ok := slots["MAP"]; !ok {
		if p, err := code.BuildProject(root, 0, 50); err == nil {
			slots["MAP"] = p.Render()
		} else {
			slots["MAP"] = "(project map available via kern_project_map)"
		}
	}

	// Render the template
	rendered, err := prompt.Render(templateName, slots)
	if err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}

	// Auto-inject relevant memory lessons if requested or if task is present
	injectMemory := argBool(args, "inject_memory") || task != ""
	if injectMemory {
		query := task
		if query == "" {
			query = templateName
		}
		recalled := memory.Recall(root, query, 3)
		if len(recalled) > 0 {
			var mb strings.Builder
			mb.WriteString("\n\n---\n### Top Recalled Lessons from Project Brain:\n")
			for _, entry := range recalled {
				mb.WriteString(fmt.Sprintf("• %s\n", entry.Text))
			}
			rendered += mb.String()
		}
	}

	return strings.TrimSpace(rendered), nil
}
