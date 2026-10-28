package tasklife

import (
	"regexp"
	"strings"
	"testing"

	catalog "github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

// TestRunNextActionNamesOnlyExistingTools pins the QA-2026-10-26 finding that
// kern_run recommended tools and a CLI command that no longer exist
// (kern_repair, kern_validate, `kern do`). Every kern_* token in NextAction
// must be a registered MCP tool, and the CLI entry point must be `kern run`.
func TestRunNextActionNamesOnlyExistingTools(t *testing.T) {
	p := newTestPlatform(t, t.TempDir())
	ts := NewTaskService(p, nil)
	res, err := ts.Run("Add tenant-aware caching to UserService")
	if err != nil {
		t.Fatal(err)
	}

	registered := map[string]bool{}
	for _, name := range catalog.ToolNames() {
		registered[name] = true
	}
	for _, name := range regexp.MustCompile(`kern_[a-z_]+`).FindAllString(res.NextAction, -1) {
		if !registered[name] {
			t.Errorf("NextAction recommends unregistered tool %q: %s", name, res.NextAction)
		}
	}
	if strings.Contains(res.NextAction, "kern do") {
		t.Errorf("NextAction recommends removed CLI command `kern do`: %s", res.NextAction)
	}
	if strings.Contains(res.NextAction, "kern_repair") || strings.Contains(res.NextAction, "kern_validate") {
		t.Errorf("NextAction recommends a removed tool: %s", res.NextAction)
	}
	if !strings.Contains(res.NextAction, `kern run "`) {
		t.Errorf("NextAction should name the real CLI entry point `kern run`: %s", res.NextAction)
	}
}
