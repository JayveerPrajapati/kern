package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AgentHandoff struct {
	ID        string         `json:"id"`
	FromAgent string         `json:"from_agent"`
	ToAgent   string         `json:"to_agent"`
	TaskID    string         `json:"task_id"`
	CreatedAt time.Time      `json:"created_at"`
	Notes     string         `json:"notes"`
	Payload   map[string]any `json:"payload,omitempty"`
	Status    string         `json:"status"` // "pending", "accepted", "completed"
}

type ResourceClaim struct {
	Resource  string    `json:"resource"`
	AgentID   string    `json:"agent_id"`
	ClaimedAt time.Time `json:"claimed_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

var (
	coordMu        sync.Mutex
	activeHandoffs = map[string][]AgentHandoff{}           // root -> handoffs
	activeClaims   = map[string]map[string]ResourceClaim{} // root -> resource -> claim
)

func (s *Server) handleAgentCoordination(ctx context.Context, args map[string]any) (string, error) {
	action := strings.ToLower(strings.TrimSpace(argString(args, "action")))
	if action == "" {
		action = "status"
	}
	root := resolveRoot(argString(args, "root"))

	coordMu.Lock()
	defer coordMu.Unlock()

	if _, ok := activeHandoffs[root]; !ok {
		activeHandoffs[root] = []AgentHandoff{}
	}
	if _, ok := activeClaims[root]; !ok {
		activeClaims[root] = map[string]ResourceClaim{}
	}

	now := time.Now().UTC()

	// Clean up expired claims
	for res, c := range activeClaims[root] {
		if now.After(c.ExpiresAt) {
			delete(activeClaims[root], res)
		}
	}

	agentID := argString(args, "agent_id")
	format := strings.ToLower(argString(args, "format"))

	switch action {
	case "handoff":
		from := argString(args, "from_agent")
		if from == "" {
			from = agentID
		}
		if from == "" {
			return "", fmt.Errorf("kern_agent_coordination: 'from_agent' or 'agent_id' required for handoff")
		}
		to := argString(args, "to_agent")
		if to == "" {
			to = "*"
		}
		taskID := argString(args, "task_id")
		if taskID == "" {
			taskID = fmt.Sprintf("task-%d", time.Now().UnixNano()%100000)
		}

		var payload map[string]any
		if p, ok := args["payload"].(map[string]any); ok {
			payload = p
		}

		handoff := AgentHandoff{
			ID:        fmt.Sprintf("hf-%d", time.Now().UnixNano()%100000),
			FromAgent: from,
			ToAgent:   to,
			TaskID:    taskID,
			CreatedAt: now,
			Notes:     argString(args, "notes"),
			Payload:   payload,
			Status:    "pending",
		}
		activeHandoffs[root] = append(activeHandoffs[root], handoff)

		dir := filepath.Join(root, ".kern", "coordination")
		_ = os.MkdirAll(dir, 0o755)
		if b, err := json.MarshalIndent(handoff, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(dir, handoff.ID+".json"), b, 0o644)
		}

		if format == "json" {
			data, _ := json.MarshalIndent(map[string]any{
				"status":  "created",
				"handoff": handoff,
			}, "", "  ")
			return string(data), nil
		}
		return fmt.Sprintf("✅ Handoff %s registered: %s ➡️ %s (Task: %s)\nNotes: %s", handoff.ID, from, to, taskID, handoff.Notes), nil

	case "claim":
		if agentID == "" {
			return "", fmt.Errorf("kern_agent_coordination: 'agent_id' is required to claim resource")
		}
		resource := argString(args, "resource")
		if resource == "" {
			return "", fmt.Errorf("kern_agent_coordination: 'resource' is required to claim")
		}

		if existing, exists := activeClaims[root][resource]; exists {
			if existing.AgentID != agentID && now.Before(existing.ExpiresAt) {
				msg := fmt.Sprintf("❌ Resource %q is already claimed by agent %q (expires in %ds)", resource, existing.AgentID, int(existing.ExpiresAt.Sub(now).Seconds()))
				if format == "json" {
					data, _ := json.MarshalIndent(map[string]any{
						"claimed":      false,
						"conflict":     true,
						"held_by":      existing.AgentID,
						"expires_in_s": int(existing.ExpiresAt.Sub(now).Seconds()),
						"message":      msg,
					}, "", "  ")
					return string(data), nil
				}
				return msg, nil
			}
		}

		ttl := 300
		if ttlStr := argString(args, "ttl_seconds"); ttlStr != "" {
			if n, err := strconv.Atoi(ttlStr); err == nil && n > 0 {
				ttl = n
			}
		} else if n, ok := args["ttl_seconds"].(float64); ok && n > 0 {
			ttl = int(n)
		}

		expiresAt := now.Add(time.Duration(ttl) * time.Second)
		claim := ResourceClaim{
			Resource:  resource,
			AgentID:   agentID,
			ClaimedAt: now,
			ExpiresAt: expiresAt,
		}
		activeClaims[root][resource] = claim

		if format == "json" {
			data, _ := json.MarshalIndent(map[string]any{
				"claimed":    true,
				"resource":   resource,
				"agent_id":   agentID,
				"ttl_s":      ttl,
				"expires_at": expiresAt.Format(time.RFC3339),
			}, "", "  ")
			return string(data), nil
		}
		return fmt.Sprintf("🔒 Resource %q successfully claimed by %q for %ds", resource, agentID, ttl), nil

	case "release":
		resource := argString(args, "resource")
		if resource == "" {
			return "", fmt.Errorf("kern_agent_coordination: 'resource' is required to release")
		}
		existing, exists := activeClaims[root][resource]
		if !exists {
			return fmt.Sprintf("Resource %q was not claimed.", resource), nil
		}
		if agentID != "" && existing.AgentID != agentID {
			return "", fmt.Errorf("kern_agent_coordination: cannot release resource %q held by %q (caller is %q)", resource, existing.AgentID, agentID)
		}
		delete(activeClaims[root], resource)
		return fmt.Sprintf("🔓 Resource %q released successfully", resource), nil

	case "inbox":
		var myHandoffs []AgentHandoff
		for _, h := range activeHandoffs[root] {
			if agentID == "" || h.ToAgent == agentID || h.ToAgent == "*" {
				myHandoffs = append(myHandoffs, h)
			}
		}
		if format == "json" {
			data, _ := json.MarshalIndent(map[string]any{
				"agent_id": agentID,
				"count":    len(myHandoffs),
				"handoffs": myHandoffs,
			}, "", "  ")
			return string(data), nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Agent %q Inbox (%d pending)\n\n", agentID, len(myHandoffs)))
		for _, h := range myHandoffs {
			sb.WriteString(fmt.Sprintf("- **%s** (%s ➡️ %s | %s): %s\n", h.ID, h.FromAgent, h.ToAgent, h.TaskID, h.Notes))
		}
		return sb.String(), nil

	case "status":
		fallthrough
	default:
		var claimList []ResourceClaim
		for _, c := range activeClaims[root] {
			claimList = append(claimList, c)
		}
		if format == "json" {
			data, _ := json.MarshalIndent(map[string]any{
				"claims":   claimList,
				"handoffs": activeHandoffs[root],
			}, "", "  ")
			return string(data), nil
		}
		var sb strings.Builder
		sb.WriteString("## Multi-Agent Workspace Coordination\n\n")
		sb.WriteString(fmt.Sprintf("**Active Claims:** %d | **Total Handoffs:** %d\n\n", len(claimList), len(activeHandoffs[root])))
		if len(claimList) > 0 {
			sb.WriteString("### 🔒 Claimed Resources\n")
			for _, c := range claimList {
				sb.WriteString(fmt.Sprintf("- `%s`: held by agent `%s` (expires in %ds)\n",
					c.Resource, c.AgentID, int(c.ExpiresAt.Sub(now).Seconds())))
			}
			sb.WriteString("\n")
		}
		if len(activeHandoffs[root]) > 0 {
			sb.WriteString("### 📬 Active Handoffs\n")
			for _, h := range activeHandoffs[root] {
				sb.WriteString(fmt.Sprintf("- **%s** (%s ➡️ %s): %s [%s]\n",
					h.ID, h.FromAgent, h.ToAgent, h.Notes, h.Status))
			}
		}
		return sb.String(), nil
	}
}
