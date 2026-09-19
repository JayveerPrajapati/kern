// Package coord owns the agent-coordination tool bodies (kern_agent_coordination)
// as plain functions. It manages task handoffs and shared resource claims across
// multiple agents collaborating in a repository.
package coord

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

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
)

// AgentHandoff is a pending handoff of a task from one agent to another.
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

// ResourceClaim is a temporary exclusive claim on a shared resource by one
// agent, released when it expires.
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

// ResetMemory clears all in-memory claims and handoffs (used in tests to simulate fresh processes).
func ResetMemory() {
	coordMu.Lock()
	defer coordMu.Unlock()
	activeClaims = map[string]map[string]ResourceClaim{}
	activeHandoffs = map[string][]AgentHandoff{}
}

func coordinationDir(root string) string {
	return filepath.Join(root, ".kern", "coordination")
}

func claimsPath(root string) string {
	return filepath.Join(coordinationDir(root), "claims.json")
}

func loadClaims(root string) map[string]ResourceClaim {
	claims := map[string]ResourceClaim{}
	b, err := os.ReadFile(claimsPath(root))
	if err != nil {
		return claims
	}
	if err := json.Unmarshal(b, &claims); err != nil {
		return map[string]ResourceClaim{}
	}
	if claims == nil {
		claims = map[string]ResourceClaim{}
	}
	return claims
}

func saveClaims(root string) {
	dir := coordinationDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	b, err := json.MarshalIndent(activeClaims[root], "", "  ")
	if err != nil {
		return
	}
	tmp := claimsPath(root) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, claimsPath(root))
}

func loadHandoffs(root string) []AgentHandoff {
	var handoffs []AgentHandoff
	dir := coordinationDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "hf-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var h AgentHandoff
		if json.Unmarshal(b, &h) == nil && h.ID != "" {
			handoffs = append(handoffs, h)
		}
	}
	return handoffs
}

// Handle processes a kern_agent_coordination request.
func Handle(ctx context.Context, args map[string]any) (string, error) {
	action := strings.ToLower(strings.TrimSpace(mcpargs.ArgString(args, "action")))
	if action == "" {
		action = "status"
	}
	root := mcpargs.ArgString(args, "root")
	if root == "" {
		root = "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(abs)
	}

	coordMu.Lock()
	defer coordMu.Unlock()

	if _, ok := activeHandoffs[root]; !ok {
		activeHandoffs[root] = loadHandoffs(root)
	}
	if _, ok := activeClaims[root]; !ok {
		activeClaims[root] = loadClaims(root)
	}

	now := time.Now().UTC()

	cleaned := false
	for res, c := range activeClaims[root] {
		if now.After(c.ExpiresAt) {
			delete(activeClaims[root], res)
			cleaned = true
		}
	}
	if cleaned {
		saveClaims(root)
	}

	agentID := mcpargs.ArgString(args, "agent_id")
	format := strings.ToLower(mcpargs.ArgString(args, "format"))

	switch action {
	case "handoff":
		return coordHandoffLocked(root, now, agentID, format, args)
	case "claim":
		return coordClaim(root, now, agentID, format, args)
	case "release":
		return coordRelease(root, agentID, args)
	case "inbox":
		return coordInbox(root, agentID, format)
	case "status", "":
		return coordStatus(root, now, format)
	default:
		return coordStatus(root, now, format)
	}
}

// Handoff registers an agent handoff in the coordination directory.
func Handoff(root string, now time.Time, agentID, format string, args map[string]any) (string, error) {
	coordMu.Lock()
	defer coordMu.Unlock()
	if _, ok := activeHandoffs[root]; !ok {
		activeHandoffs[root] = loadHandoffs(root)
	}
	return coordHandoffLocked(root, now, agentID, format, args)
}

func coordHandoffLocked(root string, now time.Time, agentID, format string, args map[string]any) (string, error) {
	from := mcpargs.ArgString(args, "from_agent")
	if from == "" {
		from = agentID
	}
	if from == "" {
		return "", fmt.Errorf("kern_agent_coordination: 'from_agent' or 'agent_id' required for handoff")
	}
	to := mcpargs.ArgString(args, "to_agent")
	if to == "" {
		to = "*"
	}
	taskID := mcpargs.ArgString(args, "task_id")
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
		Notes:     mcpargs.ArgString(args, "notes"),
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
}

func coordClaim(root string, now time.Time, agentID, format string, args map[string]any) (string, error) {
	if agentID == "" {
		return "", fmt.Errorf("kern_agent_coordination: 'agent_id' is required to claim resource")
	}
	resource := mcpargs.ArgString(args, "resource")
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
	if ttlStr := mcpargs.ArgString(args, "ttl_seconds"); ttlStr != "" {
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
	saveClaims(root)

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
}

func coordRelease(root, agentID string, args map[string]any) (string, error) {
	resource := mcpargs.ArgString(args, "resource")
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
	saveClaims(root)
	return fmt.Sprintf("🔓 Resource %q released successfully", resource), nil
}

func coordInbox(root, agentID, format string) (string, error) {
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
}

func coordStatus(root string, now time.Time, format string) (string, error) {
	claims := loadClaims(root)
	handoffs := loadHandoffs(root)

	var claimList []ResourceClaim
	for _, c := range claims {
		if now.Before(c.ExpiresAt) {
			claimList = append(claimList, c)
		}
	}
	if format == "json" {
		data, _ := json.MarshalIndent(map[string]any{
			"claims":   claimList,
			"handoffs": handoffs,
		}, "", "  ")
		return string(data), nil
	}
	var sb strings.Builder
	sb.WriteString("## Multi-Agent Workspace Coordination\n\n")
	sb.WriteString(fmt.Sprintf("**Active Claims:** %d | **Total Handoffs:** %d\n\n", len(claimList), len(handoffs)))
	if len(claimList) > 0 {
		sb.WriteString("### 🔒 Claimed Resources\n")
		for _, c := range claimList {
			sb.WriteString(fmt.Sprintf("- `%s`: held by agent `%s` (expires in %ds)\n",
				c.Resource, c.AgentID, int(c.ExpiresAt.Sub(now).Seconds())))
		}
		sb.WriteString("\n")
	}
	if len(handoffs) > 0 {
		sb.WriteString("### 📬 Active Handoffs\n")
		for _, h := range handoffs {
			sb.WriteString(fmt.Sprintf("- **%s** (%s ➡️ %s): %s [%s]\n",
				h.ID, h.FromAgent, h.ToAgent, h.Notes, h.Status))
		}
	}
	return sb.String(), nil
}
