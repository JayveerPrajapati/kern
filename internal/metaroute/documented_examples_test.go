package metaroute

import "testing"

// TestClassifyMetaRequest_DocumentedExamples pins the kern_meta examples the
// README, AGENTS.md and CLAUDE.md advertise verbatim, so the documentation
// cannot drift away from what the router actually does.
func TestClassifyMetaRequest_DocumentedExamples(t *testing.T) {
	cases := []struct {
		request string
		tool    string
	}{
		{"how does dispatch work?", "kern_explore"},
		{"what breaks if I change dispatch?", "kern_impact"},
		{"show me the architecture", "kern_arch"},
		{"compress this log: ERROR boom\n at main.go:10", "kern_optimize"},
		{"mask secrets in: token=sk-abc123", "kern_mask_pii"},
		{"find the NewServer function", "kern_search"},
	}
	for _, c := range cases {
		got, _ := ClassifyMetaRequest(c.request)
		if got != c.tool {
			t.Errorf("ClassifyMetaRequest(%q) = %s, documented route is %s", c.request, got, c.tool)
		}
	}
}
