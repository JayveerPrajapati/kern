package intel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const staleBannerPrefix = "⚠️ "

// TestGraphCtxStalenessBanner: GraphCtx must prepend the one-line staleness
// banner when a file cited in the answer changed since the index was built,
// and nothing when all cited files are fresh.
func TestGraphCtxStalenessBanner(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix := buildIndex(t, dir)

	// Fresh: the answer carries no banner.
	fresh, err := GraphCtx(ix, "Public", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fresh, staleBannerPrefix) {
		t.Errorf("fresh answer has a staleness banner:\n%s", fresh)
	}

	// Stale: edit a file the answer cites (lib/lib.go defines Public and is
	// part of the neighborhood) and re-query the same index.
	p := filepath.Join(dir, "lib", "lib.go")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(data, []byte("\n// drifted\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, err := GraphCtx(ix, "Public", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stale, staleBannerPrefix) {
		t.Errorf("stale answer missing banner; got:\n%s", stale)
	}
	if !strings.Contains(stale, "1 file(s) changed since index") {
		t.Errorf("banner should name the count of drifted files; got:\n%s", stale)
	}
}

// TestGraphCtxStalenessBannerSurvivesBudget: the banner is the first line, so
// a tight token budget must keep it (head-first fitting).
func TestGraphCtxStalenessBannerSurvivesBudget(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix := buildIndex(t, dir)
	p := filepath.Join(dir, "lib", "lib.go")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(data, []byte("\n// drifted\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := GraphCtx(ix, "Public", 40)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, staleBannerPrefix) {
		t.Errorf("banner lost under budget; got:\n%s", out)
	}
}

// TestExploreStaleBanner: Explore tags its report with the staleness banner
// (definition file + blast files are the cited files), and RenderExplore
// prepends it to the rendered answer.
func TestExploreStaleBanner(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix := buildIndex(t, dir)

	rep, err := Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.StaleBanner != "" {
		t.Errorf("fresh explore reported staleness: %q", rep.StaleBanner)
	}
	if out := RenderExplore(rep); strings.Contains(out, staleBannerPrefix) {
		t.Errorf("fresh render has a staleness banner:\n%s", out)
	}

	p := filepath.Join(dir, "client", "client.go")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(data, []byte("\n// drifted\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err = Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.StaleBanner == "" {
		t.Error("stale explore missing StaleBanner")
	}
	out := RenderExplore(rep)
	if !strings.HasPrefix(out, staleBannerPrefix) {
		t.Errorf("stale render missing banner; got:\n%s", out)
	}
}