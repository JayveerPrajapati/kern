package setup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-isatty"
)

// TermStyle decides how setup renders. Rich = boxes, glyphs and sections
// (interactive terminals only); Color = ANSI colour; Unicode = box/glyph
// characters instead of ASCII. Pipes, CI logs and --plain keep the legacy
// "[ok] agent  note" lines so scripts that parse them keep working.
type TermStyle struct {
	Rich    bool
	Color   bool
	Unicode bool
}

func IsTTY(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

func NewTermStyle(f *os.File, plain bool) TermStyle {
	getenv := os.Getenv
	return styleFor(IsTTY(f), plain, runtime.GOOS, getenv)
}

// styleFor is the pure decision function behind NewTermStyle so every OS/env
// combination is unit-testable without a terminal.
func styleFor(tty, plain bool, goos string, getenv func(string) string) TermStyle {
	if !tty || plain || getenv("KERN_PLAIN") != "" || getenv("TERM") == "dumb" {
		return TermStyle{}
	}
	t := TermStyle{Rich: true}
	// ANSI on Windows needs a VT-capable host; Windows Terminal, ConEmu,
	// ANSICON and most IDE terminals announce themselves.
	vt := goos != "windows" || getenv("WT_SESSION") != "" || getenv("ANSICON") != "" ||
		getenv("ConEmuANSI") == "ON" || getenv("TERM_PROGRAM") != "" || getenv("TERM") != ""
	t.Color = vt && getenv("NO_COLOR") == ""
	t.Unicode = getenv("KERN_ASCII") == "" && vt && !nonUTF8Locale(getenv)
	return t
}

func nonUTF8Locale(getenv func(string) string) bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := strings.ToLower(getenv(k))
		if v == "" {
			continue
		}
		return !(strings.Contains(v, "utf-8") || strings.Contains(v, "utf8"))
	}
	return false
}

func (t TermStyle) Paint(code, s string) string {
	if !t.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (t TermStyle) Bold(s string) string   { return t.Paint("1", s) }
func (t TermStyle) Dim(s string) string    { return t.Paint("2", s) }
func (t TermStyle) Green(s string) string  { return t.Paint("32", s) }
func (t TermStyle) Yellow(s string) string { return t.Paint("33", s) }
func (t TermStyle) Red(s string) string    { return t.Paint("31", s) }
func (t TermStyle) Cyan(s string) string   { return t.Paint("36", s) }

func (t TermStyle) Pick(uni, ascii string) string {
	if t.Unicode {
		return uni
	}
	return ascii
}

func (t TermStyle) OKGlyph() string   { return t.Green(t.Pick("✔", "ok")) }
func (t TermStyle) SkipGlyph() string { return t.Dim(t.Pick("–", "--")) }
func (t TermStyle) FailGlyph() string { return t.Red(t.Pick("✖", "!!")) }
func (t TermStyle) Bullet() string    { return t.Cyan(t.Pick("•", "*")) }

// box draws a titled frame around plain-text lines (no ANSI inside: padding
// is computed from rune counts).
func (t TermStyle) Box(title string, lines []string) string {
	const maxInner = 74
	inner := utf8.RuneCountInString(title) + 4
	clipped := make([]string, len(lines))
	for i, l := range lines {
		if utf8.RuneCountInString(l) > maxInner {
			r := []rune(l)
			l = string(r[:maxInner-1]) + t.Pick("…", "~")
		}
		clipped[i] = l
		if n := utf8.RuneCountInString(l); n+2 > inner {
			inner = n + 2
		}
	}
	tl, tr, bl, br, h, v := "+", "+", "+", "+", "-", "|"
	if t.Unicode {
		tl, tr, bl, br, h, v = "╭", "╮", "╰", "╯", "─", "│"
	}
	var b strings.Builder
	b.WriteString(t.Dim(tl+h+" ") + t.Bold(title) + t.Dim(" "+strings.Repeat(h, inner-utf8.RuneCountInString(title)-3)+tr) + "\n")
	for _, l := range clipped {
		pad := inner - utf8.RuneCountInString(l) - 1
		b.WriteString(t.Dim(v) + " " + l + strings.Repeat(" ", pad) + t.Dim(v) + "\n")
	}
	b.WriteString(t.Dim(bl+strings.Repeat(h, inner)+br) + "\n")
	return b.String()
}

func (t TermStyle) Heading(n, total int, title string) string {
	return "\n" + t.Bold(t.Cyan(fmt.Sprintf("[%d/%d]", n, total))) + " " + t.Bold(title) + "\n"
}

// PlatformLabel renders "macOS arm64" / "Linux amd64" / "Windows amd64".
func PlatformLabel(goos, goarch string) string {
	name := goos
	switch goos {
	case "darwin":
		name = "macOS"
	case "linux":
		name = "Linux"
	case "windows":
		name = "Windows"
	}
	return name + " " + goarch
}

// osNotes returns the platform-specific tips shown after setup. Pure so each
// OS branch is covered by a unit test.
func osNotes(goos string) []string {
	switch goos {
	case "darwin":
		return []string{
			"macOS: if the OS kills kern-mcp (\"Killed: 9\"), re-run the installer",
			"       (it re-signs the binary) and then run: kern doctor",
		}
	case "windows":
		return []string{
			"Windows: open a NEW PowerShell window so the updated PATH applies.",
			"         If 'kern' is still not found, check: where.exe kern",
		}
	default:
		return []string{
			"Linux: if 'kern' is not found, put ~/.local/bin on PATH:",
			"       export PATH=\"$HOME/.local/bin:$PATH\"",
		}
	}
}

type nextStep struct{ cmd, why string }

// nextSteps lists the suggested follow-ups. allAgentsHint is true when the
// run defaulted to detected agents only, so the user learns how to widen it.
func nextSteps(allAgentsHint bool) []nextStep {
	steps := []nextStep{
		{"kern setup --dry-run", "preview what wiring would write (read-only)"},
		{"kern doctor", "check binary, agent configs and index health"},
		{"kern setup --check", "list which agents are wired"},
	}
	if allAgentsHint {
		steps = append(steps, nextStep{"kern setup --all-agents", "also wire agents that were not detected"})
	}
	steps = append(steps, nextStep{"kern setup --global", "wire machine-wide + git commit protection"})
	return steps
}

func shortPath(root, p string) string {
	if p == "" {
		return ""
	}
	if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// applySetupDefaults implements the quiet default: with no agent selection
// flags, wire only the agents actually found. Writing config for every
// supported agent is opt-in (--all-agents); --global keeps its documented
// machine-wide meaning. defaulted reports that detection was implied.
func applySetupDefaults(f *setupFlags) (defaulted bool, err error) {
	if f.allAgents && (f.detect || f.agents != "") {
		return false, fmt.Errorf("setup: --all-agents cannot be combined with --detect or --agents")
	}
	if f.agents == "" && !f.detect && !f.allAgents && !f.global {
		f.detect = true
		return true, nil
	}
	return false, nil
}

// statusQuietHidden reports a wiring status line that carries no action the
// user must know about: deliberate skips (agent absent) and pure no-ops
// (already present / already current). Writes, updates, removals and
// failures still print — the default output shows what changed, not the
// full wiring inventory (`kern setup --check` is the detailed surface).
func statusQuietHidden(mark, note string) bool {
	if mark == "--" {
		return true
	}
	n := strings.ToLower(note)
	return strings.Contains(n, "present") || strings.Contains(n, "already")
}

// PrintSetupStatus prints one wiring result. mark is the legacy marker
// ("ok", "--", "!!"); non-Rich output keeps the exact legacy line format.
func PrintSetupStatus(t TermStyle, mark string, s Status) {
	if !t.Rich {
		fmt.Printf("[%s] %-32s %s\n", mark, s.Agent, s.Note)
		return
	}
	g := t.OKGlyph()
	switch mark {
	case "--":
		g = t.SkipGlyph()
	case "!!":
		g = t.FailGlyph()
	}
	fmt.Printf("  %s %-30s %s\n", g, s.Agent, t.Dim(s.Note))
	// Say which file was touched: a machine-wide install edits files under the
	// user's home, and "what did it change?" must be answerable from the output.
	if s.Path != "" && !strings.Contains(s.Note, s.Path) {
		fmt.Printf("      %s %s\n", t.Dim(t.Pick("→", "->")), t.Dim(HomeShort(s.Path)))
	}
}

// HomeShort renders an absolute path with the home directory as "~".
func HomeShort(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func renderSetupHeader(t TermStyle, root string) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	fmt.Print(t.Box("kern setup", []string{
		PlatformLabel(runtime.GOOS, runtime.GOARCH) + t.Pick("  ·  ", "  |  ") + "project: " + HomeShort(abs),
		"Nothing is written until you confirm (use --dry-run to only preview).",
	}))
}

func renderDetected(t TermStyle, detected []string, usedDetect, defaulted bool) {
	fmt.Print(t.Heading(1, 3, "Detect agents"))
	switch {
	case !usedDetect:
		fmt.Printf("  %s all supported agents (explicit --all-agents or --global)\n", t.Bullet())
	case len(detected) == 0:
		fmt.Printf("  %s %s\n", t.Yellow(t.Pick("!", "!")), "no agents detected in this project or on this machine")
		fmt.Printf("    %s\n", t.Dim("target some with --agents claude,cursor, or wire everything with --all-agents"))
	default:
		for _, a := range detected {
			fmt.Printf("  %s %s\n", t.OKGlyph(), a)
		}
		if defaulted {
			fmt.Printf("    %s\n", t.Dim("only detected agents are wired by default; add others with --agents NAME or --all-agents"))
		}
	}
}

func renderPlan(t TermStyle, root string, sts []Status, dryRun bool, scope string) {
	title := "What will change"
	if dryRun {
		title = "What would change (dry-run)"
	}
	fmt.Print(t.Heading(2, 3, title))
	fmt.Printf("  %s scope: %s\n", t.Dim(t.Pick("›", ">")), scope)
	const maxRows = 12
	n := 0
	rows := 0
	for _, s := range sts {
		if s.Skipped {
			continue
		}
		n++
		if !dryRun && rows >= maxRows {
			continue
		}
		rows++
		what := strings.TrimPrefix(s.Note, "[dry-run] would ")
		where := shortPath(root, s.Path)
		if where == "" {
			where = s.Agent
		}
		fmt.Printf("  %s %-34s %s\n", t.Cyan("+"), where, t.Dim(what))
	}
	if n > rows {
		fmt.Printf("  %s\n", t.Dim(fmt.Sprintf("… and %d more (preview everything with: kern setup --dry-run)", n-rows)))
	}
	if n == 0 {
		fmt.Printf("  %s nothing to write\n", t.Dim(t.Pick("–", "--")))
	}
}

func renderNextSteps(t TermStyle, allAgentsHint bool) {
	lines := []string{"Restart your agent so it loads kern", "(Claude Code: restart, opencode: reload)", ""}
	for _, s := range nextSteps(allAgentsHint) {
		lines = append(lines, fmt.Sprintf("%-26s %s", s.cmd, s.why))
	}
	lines = append(lines, "")
	lines = append(lines, osNotes(runtime.GOOS)...)
	fmt.Println()
	fmt.Print(t.Box("Next steps", lines))
}

// confirmPrompt asks a yes/no question on stdin; the default is yes.
func confirmPrompt(t TermStyle, in io.Reader, q string) bool {
	fmt.Printf("\n%s %s ", t.Bold(q), t.Dim("[Y/n]"))
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	}
	return false
}
