// Package security owns the security-family MCP tool bodies (kern_mask_pii)
// as plain functions.
package security

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/pii"
)

// SecurityService is the minimal security surface the security tool bodies
// need. It is declared locally (rather than importing the dissolved
// internal/service layer) so this package depends only on the internal
// engines.
type SecurityService interface {
	Mask(ctx context.Context, text string, names []string) (pii.Result, error)
}

// MaskPII sanitizes sensitive PII and secrets from text.
func MaskPII(ctx context.Context, secSvc SecurityService, args map[string]any) (string, error) {
	text := mcpargs.ArgString(args, "text")
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	var names []string
	for _, n := range strings.Split(mcpargs.ArgString(args, "mask_names"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	res, err := secSvc.Mask(ctx, text, names)
	if err != nil {
		return "", err
	}
	var parts []string
	for k, v := range res.ByLabel {
		parts = append(parts, fmt.Sprintf("%s %d", k, v))
	}
	summary := "masked " + strconv.Itoa(res.Replaced) + " secrets"
	if len(parts) > 0 {
		summary += ": " + strings.Join(parts, ", ")
	}
	return res.Text + "\n[kern] " + summary + "\n", nil
}
