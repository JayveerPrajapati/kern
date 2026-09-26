package setup

import (
	"os"
	"regexp"
	"testing"
)

// TestPluginShadowsKeepPayloads guards the run/runPayload contract in the
// opencode plugin (QA Pick #30, finding F-PL1).
//
// Several kern CLI commands print their full report to STDOUT and then exit
// non-zero as a CI signal (kern changes/review exit 3 on risk, kern verify and
// kern check-draft exit 1 on findings, kern diff-gate exits 1/2 with the
// 11-check report, kern validate-proposed exits 1/2, ...). The plugin's run()
// helper throws on non-zero exit and DROPS stdout; only runPayload() captures
// the report and returns it as the tool result. Any shadow tool that wraps one
// of these commands must therefore call runPayload (or runRaw), never run().
//
// The historical fix (2025-08-25) covered the then-existing shadows; this test
// fails the build if a NEW shadow wraps a report-exiting command with run().

// criticalCommands are CLI commands verified to exit non-zero with their
// report on stdout (QA picks #1-34 + memory #166). The pattern matches the
// quoted literal exactly as the plugin writes it into a flags array,
// e.g. ["verify"] or ["diff-gate"] — full tokens only, so "health" does not
// match "heal" and "execute" is matched explicitly.
var criticalCommandPattern = regexp.MustCompile(`\["(?:changes|review|security|delete|validate|guard|check|heal|sandbox|exec|execute|health|schema|verify|check-draft|diff-gate|validate-proposed)"\]`)

var (
	bareRunPattern  = regexp.MustCompile(`\brun\(`)
	toolBlockStart  = regexp.MustCompile(`kern_[a-z_]+: tool\(\{`)
	payloadPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\brunPayload\(`),
		regexp.MustCompile(`\brunRaw\(`),
	}
)

func TestPluginShadowsKeepPayloads(t *testing.T) {
	pluginPath := "../../.opencode/plugins/kern.ts"
	b, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Skipf("plugin source not found (running outside repo): %v", err)
	}
	src := string(b)

	// Slice the file into tool blocks: each starts at "kern_x: tool({" and
	// ends where the next block starts.
	starts := toolBlockStart.FindAllStringIndex(src, -1)
	if len(starts) == 0 {
		t.Fatalf("no tool blocks found in %s — parser drift?", pluginPath)
	}
	nameRe := regexp.MustCompile(`^kern_[a-z_]+`)
	blocks := make([][2]string, 0, len(starts))
	for i, s := range starts {
		end := len(src)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		name := nameRe.FindString(src[s[0]:end])
		if name == "" {
			t.Fatalf("could not extract tool name at offset %d", s[0])
		}
		blocks = append(blocks, [2]string{name, src[s[0]:end]})
	}

	failures := 0
	for _, blk := range blocks {
		name, body := blk[0], blk[1]
		cmd := criticalCommandPattern.FindString(body)
		if cmd == "" {
			continue // shadow does not wrap a report-exiting command
		}
		if bareRunPattern.MatchString(body) && !anyMatch(payloadPatterns, body) {
			t.Errorf("%s wraps report-exiting command %q but calls run() — the non-zero exit report would be dropped; use runPayload() (see runPayload doc comment)", name, cmd)
			failures++
		}
	}
	if failures > 0 {
		t.Fatalf("%d shadow tool(s) drop their findings report on non-zero exits", failures)
	}
}

func anyMatch(patterns []*regexp.Regexp, s string) bool {
	for _, p := range patterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}
