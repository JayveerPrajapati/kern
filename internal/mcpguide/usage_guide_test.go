package mcpguide

import (
	"strings"
	"testing"
)

func TestGuideDescribesCurrentSurface(t *testing.T) {
	got := Guide()
	if !strings.Contains(got, "34-tool MCP catalog") || !strings.Contains(got, "direct eleven-tool surface") {
		t.Fatalf("guide does not describe the current MCP surface:\n%s", got)
	}
}
