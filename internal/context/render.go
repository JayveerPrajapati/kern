package context

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// RenderText produces a human-readable text summary of a ContextPacket — what
// an agent or developer sees as the "Analyze this proposed change" response.
func RenderText(pkt domain.ContextPacket) string {
	var b strings.Builder

	b.WriteString("I understand the request.\n\n")
	fmt.Fprintf(&b, "Task: %s\n\n", pkt.Task)

	// System impact.
	comp := len(pkt.Symbols) + len(pkt.Files) + len(pkt.Dependencies)
	fmt.Fprintf(&b, "System impact:\n%d components.\n", comp)

	// Relevant architecture (communities / boundaries).
	b.WriteString("\nRelevant architecture:\n")
	if len(pkt.ArchitectureRules) > 0 {
		for _, r := range pkt.ArchitectureRules {
			fmt.Fprintf(&b, "- %s (%s)\n", r.Name, r.Description)
		}
	} else {
		b.WriteString("(no architectural boundary rules recorded)\n")
	}

	// Historical constraint (first recalled memory, if any).
	b.WriteString("\nHistorical constraint:\n")
	if len(pkt.Memory) > 0 {
		fmt.Fprintf(&b, "%s\n", pkt.Memory[0].Content)
	} else if len(pkt.Incidents) > 0 {
		fmt.Fprintf(&b, "%s\n", pkt.Incidents[0].Content)
	} else {
		b.WriteString("(no relevant historical memory)\n")
	}

	// Potential risk.
	level := riskLevelOf(pkt)
	b.WriteString("\nPotential risk:\n")
	fmt.Fprintf(&b, "%s\n", level)

	// Required validation: verification steps derived from the analysis.
	b.WriteString("\nRequired validation:\n")
	if len(pkt.RequiredValidation) > 0 {
		for _, step := range pkt.RequiredValidation {
			fmt.Fprintf(&b, "- %s\n", step)
		}
	} else {
		b.WriteString("- verify the change manually\n")
	}

	// Risk: level.
	b.WriteString("\nRisk:\n")
	fmt.Fprintf(&b, "%s\n", level)

	// Estimated affected files.
	fmt.Fprintf(&b, "\nEstimated affected files:\n%d.\n", len(pkt.Files))

	if hasApprovalRequired(pkt) {
		b.WriteString("\nProceed?\n")
	}

	return b.String()
}

// hasApprovalRequired reports whether any risk requires explicit approval
// before the change can proceed.
func hasApprovalRequired(pkt domain.ContextPacket) bool {
	for _, r := range pkt.Risks {
		if r.ApprovalRequired {
			return true
		}
	}
	return false
}

// riskLevelOf returns the highest risk level across the packet's risks,
// defaulting to LOW when none are recorded.
func riskLevelOf(pkt domain.ContextPacket) domain.RiskLevel {
	level := domain.RiskLow
	for _, r := range pkt.Risks {
		if rank(r.Level) > rank(level) {
			level = r.Level
		}
	}
	return level
}

// rank returns a comparable numeric rank for a risk level.
func rank(level domain.RiskLevel) int {
	switch level {
	case domain.RiskCritical:
		return 4
	case domain.RiskHigh:
		return 3
	case domain.RiskMedium:
		return 2
	default:
		return 1
	}
}

// RenderJSON produces the JSON representation of the packet.
func RenderJSON(pkt domain.ContextPacket) (string, error) {
	b, err := json.MarshalIndent(pkt, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
