// Package prompt provides a small library of fine-tuned, token-efficient
// prompt templates for common agent tasks. Templates are embedded and use
// {{KEY}} placeholders filled in by the caller.
package prompt

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed assets/prompts/*.md
var fs embed.FS

// List returns the available template names, sorted.
func List() ([]string, error) {
	entries, err := fs.ReadDir("assets/prompts")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".md") {
			names = append(names, strings.TrimSuffix(name, ".md"))
		}
	}
	sort.Strings(names)
	return names, nil
}

// Render fills a template's {{KEY}} placeholders. Empty variables render as
// "(n/a)" so optional slots stay explicit.
func Render(name string, vars map[string]string) (string, error) {
	body, err := fs.ReadFile("assets/prompts/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("unknown template %q", name)
	}
	out := string(body)
	for _, k := range []string{"ROOT", "MAP", "FILE", "SYMBOLS", "LANG", "TASK"} {
		val := ""
		if v, ok := vars[k]; ok {
			val = v
		}
		if val == "" {
			val = "(n/a)"
		}
		out = strings.ReplaceAll(out, "{{"+k+"}}", val)
	}
	return strings.TrimSpace(out) + "\n", nil
}
