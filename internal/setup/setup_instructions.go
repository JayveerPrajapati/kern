package setup

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed assets/instructions/continue-kern.md
var continueKernRule string

//go:embed assets/instructions/windsurf-kern.md
var windsurfKernRule string

//go:embed assets/instructions/kiro-kern.md
var kiroKernRule string

// wireContinueInstructions writes the kern-first rule to .continue/rules/kern.md.
func wireContinueInstructions(root string) Status {
	dir := filepath.Join(root, ".continue", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Status{Agent: "continue-rules", Installed: false, Note: err.Error()}
	}
	p := filepath.Join(dir, "kern.md")
	if err := os.WriteFile(p, []byte(continueKernRule), 0o644); err != nil {
		return Status{Agent: "continue-rules", Installed: false, Note: err.Error()}
	}
	return Status{Agent: "continue-rules", Installed: true, Path: p, Note: "continue kern-first rule installed"}
}

// wireWindsurfInstructions writes the kern-first rule to .windsurf/rules/kern-first.md.
func wireWindsurfInstructions(root string) Status {
	dir := filepath.Join(root, ".windsurf", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Status{Agent: "windsurf-rules", Installed: false, Note: err.Error()}
	}
	p := filepath.Join(dir, "kern-first.md")
	if err := os.WriteFile(p, []byte(windsurfKernRule), 0o644); err != nil {
		return Status{Agent: "windsurf-rules", Installed: false, Note: err.Error()}
	}
	return Status{Agent: "windsurf-rules", Installed: true, Path: p, Note: "windsurf kern-first rule installed"}
}

// wireKiroInstructions writes the kern-first steering file to .kiro/steering/kern-first.md.
func wireKiroInstructions(root string) Status {
	dir := filepath.Join(root, ".kiro", "steering")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Status{Agent: "kiro-steering", Installed: false, Note: err.Error()}
	}
	p := filepath.Join(dir, "kern-first.md")
	if err := os.WriteFile(p, []byte(kiroKernRule), 0o644); err != nil {
		return Status{Agent: "kiro-steering", Installed: false, Note: err.Error()}
	}
	return Status{Agent: "kiro-steering", Installed: true, Path: p, Note: "kiro kern-first steering installed"}
}
