package setup

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// setup_ui_test.go — the setup family's render-surface tests, moved from
// cmd/kern (workstream 3.6-A — setup family extraction). The styleFor matrix,
// box rendering, platform labels and next-step suggestions pin the pure UI
// decision functions; TestApplySetupDefaults pins the quiet-default wiring
// semantics. The cmd/kern-level flag wiring test (parseFlags knows --all-
// agents/--plain/--yes) stayed in cmd/kern/setup_ui_test.go.

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestStyleForMatrix(t *testing.T) {
	cases := []struct {
		name                  string
		tty, plain            bool
		goos                  string
		env                   map[string]string
		Rich, Color, Unicode_ bool
	}{
		{"pipe stays legacy", false, false, "linux", map[string]string{"TERM": "xterm"}, false, false, false},
		{"--plain stays legacy", true, true, "darwin", map[string]string{"TERM": "xterm"}, false, false, false},
		{"KERN_PLAIN stays legacy", true, false, "linux", map[string]string{"KERN_PLAIN": "1", "TERM": "xterm"}, false, false, false},
		{"dumb terminal stays legacy", true, false, "linux", map[string]string{"TERM": "dumb"}, false, false, false},
		{"macOS utf8", true, false, "darwin", map[string]string{"TERM": "xterm-256Color", "LANG": "en_US.UTF-8"}, true, true, true},
		{"linux utf8", true, false, "linux", map[string]string{"TERM": "xterm", "LC_ALL": "C.UTF-8"}, true, true, true},
		{"linux C locale falls back to ascii", true, false, "linux", map[string]string{"TERM": "xterm", "LANG": "C"}, true, true, false},
		{"NO_COLOR keeps glyphs, drops colour", true, false, "linux", map[string]string{"TERM": "xterm", "NO_COLOR": "1"}, true, false, true},
		{"KERN_ASCII forces ascii", true, false, "darwin", map[string]string{"TERM": "xterm", "KERN_ASCII": "1"}, true, true, false},
		{"windows terminal", true, false, "windows", map[string]string{"WT_SESSION": "abc"}, true, true, true},
		{"windows legacy conhost: no colour, no Unicode", true, false, "windows", map[string]string{}, true, false, false},
	}
	for _, c := range cases {
		got := styleFor(c.tty, c.plain, c.goos, envOf(c.env))
		if got.Rich != c.Rich || got.Color != c.Color || got.Unicode != c.Unicode_ {
			t.Errorf("%s: got Rich=%v Color=%v Unicode=%v, want Rich=%v Color=%v Unicode=%v",
				c.name, got.Rich, got.Color, got.Unicode, c.Rich, c.Color, c.Unicode_)
		}
	}
}

func TestOSNotesCoverEveryPlatform(t *testing.T) {
	for goos, want := range map[string]string{
		"darwin":  "Killed: 9",
		"windows": "PowerShell",
		"linux":   ".local/bin",
		"freebsd": ".local/bin",
	} {
		joined := strings.Join(osNotes(goos), "\n")
		if !strings.Contains(joined, want) {
			t.Errorf("osNotes(%q) missing %q:\n%s", goos, want, joined)
		}
	}
}

func TestPlatformLabel(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"darwin", "arm64"}:  "macOS arm64",
		{"linux", "amd64"}:   "Linux amd64",
		{"windows", "amd64"}: "Windows amd64",
		{"freebsd", "amd64"}: "freebsd amd64",
	} {
		if got := PlatformLabel(in[0], in[1]); got != want {
			t.Errorf("PlatformLabel(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestBoxIsRectangularInBothCharsets(t *testing.T) {
	lines := []string{"short", "a much longer line of text here", "", "naïve ünïcode · line"}
	for _, uni := range []bool{true, false} {
		box := TermStyle{Unicode: uni}.Box("Next steps", lines)
		rows := strings.Split(strings.TrimRight(box, "\n"), "\n")
		if len(rows) != len(lines)+2 {
			t.Fatalf("Unicode=%v: %d rows, want %d", uni, len(rows), len(lines)+2)
		}
		w := utf8.RuneCountInString(rows[0])
		for i, r := range rows {
			if got := utf8.RuneCountInString(r); got != w {
				t.Errorf("Unicode=%v row %d width %d, want %d: %q", uni, i, got, w, r)
			}
		}
		if !uni && strings.ContainsAny(box, "╭╮╰╯─│") {
			t.Errorf("ascii box contains box-drawing runes:\n%s", box)
		}
	}
}

func TestBoxClipsOverlongLines(t *testing.T) {
	long := strings.Repeat("x", 200)
	box := TermStyle{Unicode: true}.Box("t", []string{long})
	for _, r := range strings.Split(strings.TrimRight(box, "\n"), "\n") {
		if n := utf8.RuneCountInString(r); n > 80 {
			t.Errorf("row exceeds 80 columns (%d): %q", n, r)
		}
	}
}

func TestNextStepsSuggestions(t *testing.T) {
	has := func(steps []nextStep, cmd string) bool {
		for _, s := range steps {
			if s.cmd == cmd {
				return true
			}
		}
		return false
	}
	base := nextSteps(false)
	for _, want := range []string{"kern setup --dry-run", "kern doctor", "kern setup --check", "kern setup --global"} {
		if !has(base, want) {
			t.Errorf("nextSteps(false) missing %q", want)
		}
	}
	if has(base, "kern setup --all-agents") {
		t.Error("--all-agents hint must only show when the run defaulted to detected agents")
	}
	if !has(nextSteps(true), "kern setup --all-agents") {
		t.Error("nextSteps(true) must suggest --all-agents")
	}
}

func TestApplySetupDefaults(t *testing.T) {
	cases := []struct {
		name          string
		in            setupFlags
		wantDetect    bool
		wantDefaulted bool
		wantErr       bool
	}{
		{"bare setup detects only", setupFlags{}, true, true, false},
		{"explicit agents untouched", setupFlags{agents: "claude"}, false, false, false},
		{"explicit detect untouched", setupFlags{detect: true}, true, false, false},
		{"all-agents keeps legacy wire-everything", setupFlags{allAgents: true}, false, false, false},
		{"global keeps machine-wide meaning", setupFlags{global: true}, false, false, false},
		{"all-agents + detect conflict", setupFlags{allAgents: true, detect: true}, true, false, true},
		{"all-agents + agents conflict", setupFlags{allAgents: true, agents: "cursor"}, false, false, true},
	}
	for _, c := range cases {
		f := c.in
		defaulted, err := applySetupDefaults(&f)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", c.name, err, c.wantErr)
		}
		if f.detect != c.wantDetect || defaulted != c.wantDefaulted {
			t.Errorf("%s: detect=%v defaulted=%v, want detect=%v defaulted=%v", c.name, f.detect, defaulted, c.wantDetect, c.wantDefaulted)
		}
	}
}
