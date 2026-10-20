package main

import (
	"os"

	"github.com/JayveerPrajapati/kern/internal/setup"
)

// setup_ui.go is the cmd/kern shim for the setup family's render surface.
// The render logic lives in internal/setup (workstream 3.6-A — setup family
// extraction); the cmd/kern copies of termStyle/newTermStyle/printSetupStatus
// were consolidated into internal/setup's exported surface (tranche C2), and
// this file keeps the remaining cmd/kern callers (setup_remove.go)
// compiling against the shared implementation with zero duplicated logic:
// termStyle is a thin local struct and every helper/method below is a
// one-line delegation to internal/setup.

// termStyle is cmd/kern's local view of setup.TermStyle — the setup render
// surface's terminal style: Rich = boxes, glyphs and sections (interactive
// terminals only); Color = ANSI colour; Unicode = box/glyph characters
// instead of ASCII. Pipes, CI logs and --plain keep the legacy
// "[ok] agent  note" lines so scripts that parse them keep working. The
// fields mirror setup.TermStyle's so cmd/kern's remaining render callers
// keep their member access; all rendering delegates to internal/setup.
type termStyle struct {
	rich    bool
	color   bool
	unicode bool
}

// newTermStyle builds a termStyle for stdout — delegates the style decision
// to setup.NewTermStyle.
func newTermStyle(f *os.File, plain bool) termStyle {
	st := setup.NewTermStyle(f, plain)
	return termStyle{rich: st.Rich, color: st.Color, unicode: st.Unicode}
}

// isTTY reports whether f is a terminal — delegates to setup.IsTTY.
func isTTY(f *os.File) bool {
	return setup.IsTTY(f)
}

// platformLabel renders "macOS arm64" / "Linux amd64" / "Windows amd64" —
// delegates to setup.PlatformLabel.
func platformLabel(goos, goarch string) string {
	return setup.PlatformLabel(goos, goarch)
}

// homeShort renders an absolute path with the home directory as "~" —
// delegates to setup.HomeShort.
func homeShort(p string) string {
	return setup.HomeShort(p)
}

// printSetupStatus prints one wiring result — delegates to
// setup.PrintSetupStatus.
func printSetupStatus(t termStyle, mark string, s setup.Status) {
	setup.PrintSetupStatus(setup.TermStyle{Rich: t.rich, Color: t.color, Unicode: t.unicode}, mark, s)
}

// The methods below are the termStyle surface cmd/kern's remaining render
// callers use; each reconstructs the shared setup.TermStyle from this local
// struct's fields and delegates, so no rendering logic lives here.

func (t termStyle) box(title string, lines []string) string {
	return setup.TermStyle{Rich: t.rich, Color: t.color, Unicode: t.unicode}.Box(title, lines)
}

func (t termStyle) heading(n, total int, title string) string {
	return setup.TermStyle{Rich: t.rich, Color: t.color, Unicode: t.unicode}.Heading(n, total, title)
}

func (t termStyle) bold(s string) string {
	return setup.TermStyle{Rich: t.rich, Color: t.color, Unicode: t.unicode}.Bold(s)
}

func (t termStyle) dim(s string) string {
	return setup.TermStyle{Rich: t.rich, Color: t.color, Unicode: t.unicode}.Dim(s)
}

func (t termStyle) green(s string) string {
	return setup.TermStyle{Rich: t.rich, Color: t.color, Unicode: t.unicode}.Green(s)
}

func (t termStyle) yellow(s string) string {
	return setup.TermStyle{Rich: t.rich, Color: t.color, Unicode: t.unicode}.Yellow(s)
}

func (t termStyle) pick(uni, ascii string) string {
	return setup.TermStyle{Rich: t.rich, Color: t.color, Unicode: t.unicode}.Pick(uni, ascii)
}

func (t termStyle) okGlyph() string {
	return setup.TermStyle{Rich: t.rich, Color: t.color, Unicode: t.unicode}.OKGlyph()
}

func (t termStyle) bullet() string {
	return setup.TermStyle{Rich: t.rich, Color: t.color, Unicode: t.unicode}.Bullet()
}
