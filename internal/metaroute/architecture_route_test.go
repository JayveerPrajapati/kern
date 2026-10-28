package metaroute

import "testing"

// TestClassifyMetaRequestArchitectureOverview pins QA-2026-10-26: a broad
// architecture/overview question used to be claimed by the workflow router's
// explain+architecture arm and routed to kern_explore with the leading verb
// ("Explain") as the symbol. It must go to kern_arch.
func TestClassifyMetaRequestArchitectureOverview(t *testing.T) {
	cases := []string{
		"Explain the overall architecture of this project",
		"show me the architecture",
		"give me an overview of the system",
		"explain the architecture",
	}
	for _, request := range cases {
		tool, args := ClassifyMetaRequest(request)
		if tool != "kern_arch" {
			t.Errorf("ClassifyMetaRequest(%q) = %s %+v, want kern_arch", request, tool, args)
		}
	}
}
