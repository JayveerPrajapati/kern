package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setup_ui_path_test.go — moved from cmd/kern (workstream 3.6-A — setup
// family extraction): a machine-wide install edits files under the user's
// home; the interactive output must say which, while plain output keeps its
// exact legacy line.
func TestPrintSetupStatusNamesTheTouchedFile(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	st := Status{Agent: "claude hooks", Installed: true, Path: filepath.Join(home, ".claude", "settings.json"), Note: "hooks merged"}

	Rich := captureSetupOut(t, func() { PrintSetupStatus(TermStyle{Rich: true, Unicode: false}, "ok", st) })
	if !strings.Contains(Rich, "-> ~") || !strings.Contains(Rich, "settings.json") {
		t.Errorf("Rich output must name the file with ~ shortening:\n%s", Rich)
	}

	plain := captureSetupOut(t, func() { PrintSetupStatus(TermStyle{}, "ok", st) })
	if strings.Contains(plain, "settings.json") || strings.Count(plain, "\n") != 1 {
		t.Errorf("plain output must stay the single legacy line:\n%s", plain)
	}

	// A note that already names the path is not repeated.
	st.Note = "wrote " + st.Path
	Rich = captureSetupOut(t, func() { PrintSetupStatus(TermStyle{Rich: true}, "ok", st) })
	if strings.Count(Rich, "settings.json") != 1 {
		t.Errorf("path must not be repeated when the note has it:\n%s", Rich)
	}
}
