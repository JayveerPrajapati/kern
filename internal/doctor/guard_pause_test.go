package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/guardpause"
)

func TestCheckGuardPause(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	registry := filepath.Join(home, ".kern", "guard", "pauses.jsonl")
	t.Setenv("HOME", home)

	pauseFinding := func() Finding {
		for _, d := range guardpause.DoctorFindings(root) {
			if d.Check == "guard-pause" {
				return Finding{Check: d.Check, Level: d.Level, Detail: d.Detail}
			}
		}
		return Finding{}
	}

	got := pauseFinding()
	if got.Level != "ok" || got.Check != "guard-pause" || got.Detail != "no active repo guard pause" {
		t.Fatalf("inactive pause = %+v", got)
	}

	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := fmt.Sprintf(
		`{"schema_version":"kern-guard-pause","scope":"repo","root":%q,"reason":"focused debugging","expires":%q,"recorded":%q}`+"\n",
		canonical,
		now.Add(30*time.Minute).Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err := os.WriteFile(registry, []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}

	got = pauseFinding()
	if got.Level != "warn" || got.Check != "guard-pause" {
		t.Fatalf("active pause level/check = %+v", got)
	}
	for _, want := range []string{canonical, "focused debugging", "exec governance remains enforced"} {
		if !strings.Contains(got.Detail, want) {
			t.Fatalf("active pause detail %q missing %q", got.Detail, want)
		}
	}
}
