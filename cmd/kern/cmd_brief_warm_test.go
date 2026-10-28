package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunBriefWarmsColdIndex pins the onboarding contract: `kern buddy` must
// not dead-end on a cold store with "not built yet" — it warms (builds and
// persists) the index before rendering the digest.
func TestRunBriefWarmsColdIndex(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module probe\n\ngo 1.22\n",
		"a.go":   "package probe\n\nfunc WarmTarget() int { return 1 }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := captureStdout(t, func() { runBrief([]string{dir}) })
	if strings.Contains(out, "not built yet") {
		t.Fatalf("buddy dead-ended on a cold index:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".kern")); err != nil {
		t.Fatalf("expected a persisted index store: %v", err)
	}
}
