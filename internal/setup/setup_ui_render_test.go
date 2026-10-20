package setup

import (
	"io"
	"os"
	"strings"
	"testing"
)

// setup_ui_render_test.go — the setup family's render-flow tests, moved from
// cmd/kern (workstream 3.6-A — setup family extraction). They pin the
// interactive plan/header/status rendering byte-for-byte on the internal
// render functions.

func captureSetupOut(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func sampleStatuses(root string) []Status {
	return []Status{
		{Agent: "claude", Path: root + "/.mcp.json", Note: "[dry-run] would merge the kern MCP entry into .mcp.json"},
		{Agent: "AGENTS.md", Path: root + "/AGENTS.md", Note: "[dry-run] would write AGENTS.md (thin rules)"},
		{Agent: "gitignore", Path: root + "/.gitignore", Note: "[dry-run] would write/refresh the kern-generated .gitignore block"},
		{Agent: "cursor", Skipped: true, Note: "not detected"},
	}
}

func TestRenderFlowUnicodeAndASCII(t *testing.T) {
	root := "/work/app"
	for _, uni := range []bool{true, false} {
		ui := TermStyle{Rich: true, Unicode: uni}
		out := captureSetupOut(t, func() {
			renderSetupHeader(ui, root)
			renderDetected(ui, []string{"claude", "opencode"}, true, true)
			renderPlan(ui, root, sampleStatuses(root), false, "this project only")
			PrintSetupStatus(ui, "ok", sampleStatuses(root)[0])
			PrintSetupStatus(ui, "--", sampleStatuses(root)[3])
			PrintSetupStatus(ui, "!!", Status{Agent: "broken", Note: "write failed"})
			renderNextSteps(ui, true)
		})
		t.Logf("Unicode=%v\n%s", uni, out)

		for _, want := range []string{
			"kern setup", "[1/3] Detect agents", "[2/3] What will change", "scope: this project only",
			".mcp.json", "kern setup --dry-run", "kern doctor", "--all-agents", "Restart your agent",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("Unicode=%v: output missing %q", uni, want)
			}
		}
		if strings.Contains(out, "cursor") && strings.Contains(out, "+ cursor") {
			t.Errorf("skipped agents must not appear in the plan")
		}
		if !uni && strings.ContainsAny(out, "╭╮╰╯─│✔✖•›") {
			t.Errorf("ascii mode leaked Unicode glyphs")
		}
	}
}

func TestRenderPlanTruncatesLongListsButNotDryRun(t *testing.T) {
	root := "/work/app"
	var many []Status
	for i := 0; i < 30; i++ {
		many = append(many, Status{Agent: "a", Path: root + "/f" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Note: "[dry-run] would write"})
	}
	ui := TermStyle{Rich: true}
	short := captureSetupOut(t, func() { renderPlan(ui, root, many, false, "this project only") })
	if !strings.Contains(short, "and 18 more") {
		t.Errorf("expected truncation notice, got:\n%s", short)
	}
	full := captureSetupOut(t, func() { renderPlan(ui, root, many, true, "this project only") })
	if strings.Contains(full, "more (preview") {
		t.Errorf("dry-run must list everything, got:\n%s", full)
	}
}

func TestRenderPlanNoAgentsDetected(t *testing.T) {
	out := captureSetupOut(t, func() { renderDetected(TermStyle{Rich: true}, nil, true, true) })
	if !strings.Contains(out, "no agents detected") || !strings.Contains(out, "--all-agents") {
		t.Errorf("empty detection must explain how to proceed, got:\n%s", out)
	}
}
