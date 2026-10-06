package setup

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestPluginExplorationCapture pins the context-reuse WRITE end: the plugin's
// tool.execute.after hook must record successful read-only kern tool calls
// (kern_explore/kern_search/kern_context/kern_buddy) as
// "Explored <target> → <top result>" memory-store calls, deduped per session
// target, capped at 20 per session, byte-bounded under 200, and skipped for
// failures and no-match calls. The capture block is extracted verbatim from
// the shipped plugin asset and executed under node with a stubbed remember()
// (the same harness style as TestPluginCompressedFooterGuarded); the block is
// deliberately plain JS (no TS annotations), so no stripping is needed.
func TestPluginExplorationCapture(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v — skipping TS exploration capture check", err)
	}
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(src), "// --- Exploration capture")
	end := strings.Index(string(src), "// --- End exploration capture")
	if start < 0 || end < 0 || end < start {
		t.Fatal("exploration capture block markers not found in plugin — capture removed?")
	}
	block := string(src[start:end])

	nodeScript := filepath.Join(t.TempDir(), "explore-capture.js")
	harness := block + `
const remembers = [];
const remember = async (lesson, maxLen) => { remembers.push(lesson); };
const exploreOut = "symbol: AddAuto (func internal/memory/memory.go:134)\n== callers ==\nevidence: internal/memory/memory.go:134 evidence-sha256:71f85f252f415d87";
(async () => {
  // 1. A successful kern_explore result is recorded.
  await rememberExploration("kern_explore", { symbol: "AddAuto" }, exploreOut, remember);
  // 2. The same exploration again (same symbol, even via another tool):
  //    deduped — no second record.
  await rememberExploration("kern_explore", { symbol: "AddAuto" }, exploreOut, remember);
  await rememberExploration("kern_context", { symbol: "AddAuto" }, exploreOut, remember);
  // 3. A failure is skipped.
  await rememberExploration("kern_explore", { symbol: "boom" }, "error: index not found", remember);
  // 4. A no-match call is skipped (no top-result reference in the output).
  await rememberExploration("kern_search", { query: "missingthing" }, "no symbols matched", remember);
  // 5. A search hit is recorded.
  await rememberExploration("kern_search", { query: "remember" }, "func runRemember cmd/kern/cmd_memory.go:55", remember);
  // 6. The buddy digest records its project root.
  await rememberExploration("kern_buddy", {}, "# kern buddy briefing\n## Project map\nProject: /tmp/demo (12 files)\n", remember);
  // 7. Dual-registered MCP name (kern_kern_explore) is normalized and
  //    recorded.
  await rememberExploration("kern_kern_explore", { symbol: "dual" }, "dual (func pkg/dual.go:1)", remember);
  // 8. An over-long record is byte-clipped below 200 with an ellipsis.
  await rememberExploration("kern_explore", { symbol: "verylongsymbol".repeat(30) }, "x (func pkg/verylong.go:42)", remember);
  // 9. The session cap: 20 exploration records TOTAL per session — the 30
  //    distinct explorations stop once the cap is reached.
  for (let i = 0; i < 30; i++) {
    await rememberExploration("kern_explore", { symbol: "sym" + i }, "sym" + i + " (func pkg/a" + i + ".go:10)", remember);
  }
  process.stdout.write(JSON.stringify({ records: remembers }));
})();
`
	if err := os.WriteFile(nodeScript, []byte(harness), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", nodeScript).Output()
	if err != nil {
		t.Fatalf("node exploration harness failed: %v", err)
	}
	var res struct {
		Records []string `json:"records"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode node output: %v (%s)", err, out)
	}

	// Order: explore, search, buddy digest, dual, clipped, then the 20
	// capped sym0..sym19 (dedupe/failure/no-match contributed nothing).
	wantHead := []string{
		"Explored AddAuto → internal/memory/memory.go:134",
		"Explored remember → cmd/kern/cmd_memory.go:55",
		"Explored session digest → /tmp/demo",
		"Explored dual → pkg/dual.go:1",
	}
	if len(res.Records) < len(wantHead) {
		t.Fatalf("got %d records, want at least %d: %v", len(res.Records), len(wantHead), res.Records)
	}
	for i, w := range wantHead {
		if res.Records[i] != w {
			t.Errorf("record %d = %q, want %q", i, res.Records[i], w)
		}
	}
	// The cap is 20 records TOTAL per session: the 5 records above leave
	// room for 15 more (sym0..sym14); sym15..sym29 are dropped.
	wantTotal := 20
	if len(res.Records) != wantTotal {
		t.Errorf("record count = %d, want %d (dedupe + failure/no-match skips + 20-record session cap): %v",
			len(res.Records), wantTotal, res.Records)
	}
	if last := res.Records[len(res.Records)-1]; last != "Explored sym14 → pkg/a14.go:10" {
		t.Errorf("last record = %q, want the 20th session record sym14", last)
	}
	for _, dropped := range []string{"sym15", "sym29"} {
		for _, r := range res.Records {
			if strings.Contains(r, dropped+" ") {
				t.Errorf("record past the session cap leaked: %q", r)
			}
		}
	}
	// Every record under 200 bytes (Go len is UTF-8 bytes).
	for i, r := range res.Records {
		if len(r) >= 200 {
			t.Errorf("record %d is %d bytes, want < 200: %q", i, len(r), r)
		}
	}
	// The clipped record ends with the ellipsis.
	if clipped := res.Records[4]; !strings.HasSuffix(clipped, "…") {
		t.Errorf("clipped record does not end with ellipsis: %q", clipped)
	}

	// The record format must flow into the already-committed read end:
	// `kern remember "Explored …"` is classified Source "auto" so it lands
	// in the buddy digest's "Recent session activity" section, never in the
	// lessons section.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := memory.Add(root, res.Records[0]); err != nil {
		t.Fatal(err)
	}
	entries := memory.List(root)
	if len(entries) != 1 || entries[0].Source != "auto" {
		t.Fatalf("exploration record not classified auto (read end would drop it): %+v", entries)
	}
}
