package intel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func anchorFixture() *index.Index {
	return &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "Hub", File: "hub/hub.go", Line: 10},
		},
	}
}

// TestAnchorLineFormat pins the P2 anchor contract: file:line plus the
// evidence certificate, byte-identical to the evidence_anchor formula.
func TestAnchorLineFormat(t *testing.T) {
	ix := anchorFixture()
	got := AnchorLine(ix, "Hub")
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s|%s|%d|%v|%s", "hub/hub.go", "Hub", 10, true, ix.Root)
	want := fmt.Sprintf("evidence: hub/hub.go:10 %s", "evidence-sha256:"+hex.EncodeToString(h.Sum(nil))[:16])
	if got != want {
		t.Errorf("AnchorLine = %q, want %q", got, want)
	}
	if got != AnchorLine(ix, "Hub") {
		t.Error("AnchorLine must be deterministic")
	}
}

// TestAnchorLineUnresolvable pins fail-open: unknown/empty subjects yield no
// anchor rather than a bogus certificate.
func TestAnchorLineUnresolvable(t *testing.T) {
	ix := anchorFixture()
	if got := AnchorLine(ix, "NoSuchXYZ"); got != "" {
		t.Errorf("unknown symbol = %q, want empty", got)
	}
	if got := AnchorLine(ix, ""); got != "" {
		t.Errorf("empty symbol = %q, want empty", got)
	}
	if got := AnchorLine(nil, "Hub"); got != "" {
		t.Errorf("nil index = %q, want empty", got)
	}
}

// TestExploreCarriesEvidence pins the explore wiring: the report carries the
// anchor and the render appends it.
func TestExploreCarriesEvidence(t *testing.T) {
	dir := writeTree(t, map[string]string{"lib/lib.go": srcLib})
	ix := buildIndex(t, dir)
	rep, err := Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Evidence == "" || !strings.HasPrefix(rep.Evidence, "evidence: ") {
		t.Fatalf("explore report missing anchor, got %q", rep.Evidence)
	}
	if out := RenderExplore(rep); !strings.Contains(out, rep.Evidence) {
		t.Errorf("render missing anchor line:\n%s", out)
	}
}
