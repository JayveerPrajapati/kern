package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// AgentFingerprintReport summarizes an agent's behavioral pattern based on its tool call sequence.
type AgentFingerprintReport struct {
	AgentID          string         `json:"agent_id"`
	TotalCalls       int            `json:"total_calls"`
	FingerprintHash  string         `json:"fingerprint_hash"`
	LoopDetected     bool           `json:"loop_detected"`
	RepetitiveCount  int            `json:"repetitive_calls_count"`
	DominantTool     string         `json:"dominant_tool"`
	ToolDistribution map[string]int `json:"tool_distribution"`
	CallSequence     []string       `json:"call_sequence_recent"`
	HealthStatus     string         `json:"health_status"` // "NORMAL" | "LOOPING" | "DEGRADED"
	Recommendation   string         `json:"recommendation"`
}

// handleAgentFingerprint hashes and analyzes an agent's tool-call sequence from the
// tamper-evident audit log to detect infinite loops, hallucinations, or behavioral drift.
func (s *Server) handleAgentFingerprint(ctx context.Context, args map[string]any) (string, error) {
	agentID := argString(args, "agent_id")
	if agentID == "" {
		agentID = governance.DefaultAgentID
	}

	s.auditMu.Lock()
	var entries []governance.AuditEntry
	if s.audit != nil {
		entries = s.audit.Filter(agentID)
	}
	s.auditMu.Unlock()

	// If no entries for specific agent, check all recent audit entries
	if len(entries) == 0 {
		s.auditMu.Lock()
		if s.audit != nil {
			entries = s.audit.All()
		}
		s.auditMu.Unlock()
	}

	toolCounts := make(map[string]int)
	var sequence []string

	for _, e := range entries {
		action := e.Action
		if action == "" {
			continue
		}
		toolCounts[action]++
		sequence = append(sequence, action)
	}

	// Keep last 30 calls
	recentSeq := sequence
	if len(recentSeq) > 30 {
		recentSeq = recentSeq[len(recentSeq)-30:]
	}

	// Detect loops: 3 or more consecutive identical calls, or repeating 2-step cycles
	loopDetected := false
	repetitiveCount := 0
	if len(recentSeq) >= 3 {
		for i := len(recentSeq) - 1; i >= 2; i-- {
			if recentSeq[i] == recentSeq[i-1] && recentSeq[i] == recentSeq[i-2] {
				loopDetected = true
				repetitiveCount++
			}
		}
		// Check AB-AB-AB pattern
		if len(recentSeq) >= 6 {
			last6 := recentSeq[len(recentSeq)-6:]
			if last6[0] == last6[2] && last6[2] == last6[4] && last6[1] == last6[3] && last6[3] == last6[5] {
				loopDetected = true
				repetitiveCount += 2
			}
		}
	}

	// Compute behavioral fingerprint hash
	h := sha256.New()
	for _, tool := range sequence {
		h.Write([]byte(tool + "|"))
	}
	fpHash := "fp-" + hex.EncodeToString(h.Sum(nil))[:16]

	dominantTool := ""
	maxCount := 0
	for tool, cnt := range toolCounts {
		if cnt > maxCount {
			maxCount = cnt
			dominantTool = tool
		}
	}

	health := "NORMAL"
	recommendation := "Agent behavior pattern is healthy and diversified."
	if loopDetected {
		health = "LOOPING"
		recommendation = fmt.Sprintf("Repetitive loop detected on '%s'. Interrupt turn and instruct agent to switch strategy.", dominantTool)
	} else if len(sequence) > 10 && float64(maxCount)/float64(len(sequence)) > 0.70 {
		health = "DEGRADED"
		recommendation = fmt.Sprintf("High tool polarization: %s accounts for >70%% of calls. Check if agent is struggling.", dominantTool)
	}

	report := AgentFingerprintReport{
		AgentID:          agentID,
		TotalCalls:       len(sequence),
		FingerprintHash:  fpHash,
		LoopDetected:     loopDetected,
		RepetitiveCount:  repetitiveCount,
		DominantTool:     dominantTool,
		ToolDistribution: toolCounts,
		CallSequence:     recentSeq,
		HealthStatus:     health,
		Recommendation:   recommendation,
	}

	if argString(args, "format") == "json" {
		data, _ := json.MarshalIndent(report, "", "  ")
		return string(data), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "AGENT BEHAVIOR FINGERPRINT [%s — Status: %s]\n", agentID, health)
	fmt.Fprintf(&b, "========================================================\n")
	fmt.Fprintf(&b, "Total Recorded Calls: %d | Fingerprint: %s\n", len(sequence), fpHash)
	fmt.Fprintf(&b, "Dominant Tool:        %s (%d invocations)\n", dominantTool, maxCount)
	fmt.Fprintf(&b, "Looping Detected:     %v\n\n", loopDetected)

	if len(recentSeq) > 0 {
		fmt.Fprintf(&b, "Recent Sequence (tail %d):\n", len(recentSeq))
		fmt.Fprintf(&b, "  %s\n\n", strings.Join(recentSeq, " → "))
	}

	fmt.Fprintf(&b, "Recommendation: %s\n", recommendation)
	return strings.TrimSpace(b.String()), nil
}
