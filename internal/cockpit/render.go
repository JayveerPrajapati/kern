package cockpit

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/gates"
)

// ANSI color codes
const (
	reset     = "\033[0m"
	bold      = "\033[1m"
	dim       = "\033[2m"
	red       = "\033[31m"
	green     = "\033[32m"
	yellow    = "\033[33m"
	blue      = "\033[34m"
	magenta   = "\033[35m"
	cyan      = "\033[36m"
	white     = "\033[37m"
	bgRed     = "\033[41m"
	bgGreen   = "\033[42m"
	bgYellow  = "\033[43m"
	bgBlue    = "\033[44m"
	bgMagenta = "\033[45m"
)

// RenderCockpit produces the full multi-pane dashboard string.
func RenderCockpit(s *State, width int) string {
	if width < 80 {
		width = 80
	}

	var b strings.Builder

	// Header
	b.WriteString(renderHeader(s, width))
	b.WriteString("\n")

	// Pane 1: Phase Stepper
	b.WriteString(renderStepper(s, width))
	b.WriteString("\n")

	// Pane 2: 30-Gate Matrix (G0-G29)
	b.WriteString(renderGateGrid(s, width))
	b.WriteString("\n")

	// Pane 3: Active Worktree Diff Preview
	b.WriteString(renderDiffPreview(s, width, 12))
	b.WriteString("\n")

	// Pane 4: Approval / Cost / Status Bar
	b.WriteString(renderFooter(s, width))

	return b.String()
}

func renderHeader(s *State, width int) string {
	var b strings.Builder
	title := fmt.Sprintf(" %sKERNOPS COCKPIT%s │ Governed Autonomous Engineering ", bold+cyan, reset)
	autonomy := fmt.Sprintf("[%s] ", s.AutonomyLevel)
	task := fmt.Sprintf("Task: %s%s%s", bold, s.Intent, reset)

	b.WriteString(boxTop(width))
	b.WriteString("\n")
	b.WriteString(boxRow(title, width))
	b.WriteString("\n")
	b.WriteString(boxRow(autonomy+task, width))
	b.WriteString("\n")
	b.WriteString(boxMid(width))
	return b.String()
}

func renderStepper(s *State, width int) string {
	var b strings.Builder
	b.WriteString(boxRow(fmt.Sprintf("%sEXECUTION LIFECYCLE PHASES%s", bold+white, reset), width))
	b.WriteString("\n")

	var parts []string
	for _, p := range OrderedPhases {
		st := s.Phases[p]
		icon := "○"
		color := dim + white
		switch st.Status {
		case StatusRunning:
			icon = "⟳"
			color = bold + yellow
		case StatusPass:
			icon = "●"
			color = bold + green
		case StatusRepairing:
			icon = "⚡"
			color = bold + magenta
		case StatusBlock:
			icon = "✕"
			color = bold + red
		case StatusSkipped:
			icon = "─"
			color = dim
		}

		label := fmt.Sprintf("%s%s %s%s", color, icon, p, reset)
		parts = append(parts, label)
	}

	stepperLine := strings.Join(parts, fmt.Sprintf(" %s──►%s ", dim, reset))
	b.WriteString(boxRow(stepperLine, width))
	b.WriteString("\n")
	b.WriteString(boxMid(width))
	return b.String()
}

func renderGateGrid(s *State, width int) string {
	var b strings.Builder
	b.WriteString(boxRow(fmt.Sprintf("%sBLUEPRINT CHANGE FIREWALL (G0–G29 GATE MATRIX)%s", bold+white, reset), width))
	b.WriteString("\n")

	cols := 5
	var cells []string
	for _, g := range gates.Registry {
		gst, ok := s.Gates[g.ID]
		badgeColor := dim + white
		statusText := "PEND"
		if ok {
			switch gst.Status {
			case StatusPass:
				badgeColor = green
				statusText = "PASS"
			case StatusWarn:
				badgeColor = yellow
				statusText = "WARN"
			case StatusBlock:
				badgeColor = red
				statusText = "BLOCK"
			case StatusRepairing:
				badgeColor = magenta
				statusText = "REPAIR"
			case StatusRunning:
				badgeColor = cyan
				statusText = "RUN"
			case StatusSkipped:
				badgeColor = dim
				statusText = "SKIP"
			}
		}

		cell := fmt.Sprintf("%-3s:%s[%-4s]%s", g.ID, badgeColor, statusText, reset)
		cells = append(cells, cell)
	}

	// Render in rows of 5
	for i := 0; i < len(cells); i += cols {
		end := i + cols
		if end > len(cells) {
			end = len(cells)
		}
		rowStr := strings.Join(cells[i:end], "   ")
		b.WriteString(boxRow(rowStr, width))
		b.WriteString("\n")
	}

	b.WriteString(boxMid(width))
	return b.String()
}

func renderDiffPreview(s *State, width, maxLines int) string {
	var b strings.Builder
	b.WriteString(boxRow(fmt.Sprintf("%sACTIVE WORKTREE LIVE DIFF%s", bold+white, reset), width))
	b.WriteString("\n")

	if strings.TrimSpace(s.Diff) == "" {
		b.WriteString(boxRow(fmt.Sprintf("%s(clean worktree / zero modifications pending)%s", dim, reset), width))
		b.WriteString("\n")
		b.WriteString(boxMid(width))
		return b.String()
	}

	lines := strings.Split(s.Diff, "\n")
	shown := lines
	if len(shown) > maxLines {
		shown = shown[:maxLines]
	}

	for _, ln := range shown {
		styled := ln
		if strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "+++") {
			styled = green + ln + reset
		} else if strings.HasPrefix(ln, "-") && !strings.HasPrefix(ln, "---") {
			styled = red + ln + reset
		} else if strings.HasPrefix(ln, "@@") {
			styled = cyan + ln + reset
		} else if strings.HasPrefix(ln, "diff ") {
			styled = bold + white + ln + reset
		}
		b.WriteString(boxRow(styled, width))
		b.WriteString("\n")
	}

	if len(lines) > maxLines {
		b.WriteString(boxRow(fmt.Sprintf("%s... (%d more diff lines)%s", dim, len(lines)-maxLines, reset), width))
		b.WriteString("\n")
	}

	b.WriteString(boxMid(width))
	return b.String()
}

func renderFooter(s *State, width int) string {
	var b strings.Builder

	// Metrics line: Tokens saved, Cost, Repair count
	metrics := fmt.Sprintf("Tokens: %d used │ Saved: %s-%d%% tokens%s │ Repairs: %d │ Cost: $%.4f",
		s.TokensUsed, green, 72, reset, s.RepairAttempts, s.CostDollars)
	b.WriteString(boxRow(metrics, width))
	b.WriteString("\n")

	// Approval prompt or status
	if s.ApprovalNeeded && !s.Approved {
		prompt := fmt.Sprintf("%s[!] APPROVAL REQUIRED: %s%s (Press [Y] Approve, [N] Reject, [I] Inspect)",
			bold+yellow, s.ApprovalReason, reset)
		b.WriteString(boxRow(prompt, width))
		b.WriteString("\n")
	} else if s.Completed {
		if s.Success {
			status := fmt.Sprintf("%s✔ TASK COMPLETED SUCCESSFULLY — Clean Sandbox Ready%s", bold+green, reset)
			b.WriteString(boxRow(status, width))
			b.WriteString("\n")
		} else {
			status := fmt.Sprintf("%s✖ TASK FAILED / BLOCKED: %s%s", bold+red, s.Error, reset)
			b.WriteString(boxRow(status, width))
			b.WriteString("\n")
		}
	} else {
		status := fmt.Sprintf("Phase: %s%s%s in progress...", bold+cyan, s.ActivePhase, reset)
		b.WriteString(boxRow(status, width))
		b.WriteString("\n")
	}

	b.WriteString(boxBottom(width))
	b.WriteString("\n")
	return b.String()
}

// Box drawing helpers
func boxTop(width int) string {
	return "┌" + strings.Repeat("─", width-2) + "┐"
}

func boxMid(width int) string {
	return "├" + strings.Repeat("─", width-2) + "┤"
}

func boxBottom(width int) string {
	return "└" + strings.Repeat("─", width-2) + "┘"
}

func boxRow(content string, width int) string {
	// Strip ANSI escapes when calculating visible length
	visLen := visibleLen(content)
	pad := width - 4 - visLen
	if pad < 0 {
		pad = 0
	}
	return "│ " + content + strings.Repeat(" ", pad) + " │"
}

func visibleLen(s string) int {
	inEscape := false
	n := 0
	for _, r := range s {
		if r == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		n++
	}
	return n
}
