package mcp

import (
	"encoding/json"
	"math"

	"github.com/JayveerPrajapati/kern/internal/config"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// TokenMetadata (P1-006) is structured token usage/cost info attached to every
// tool response. Clients can read it to understand the token cost of a call
// without parsing the content text. Fields are omitted when they are zero
// (not applicable), so a plain result stays lean.
type TokenMetadata struct {
	// TokensUsed is the token count of the request (tool name + serialized
	// arguments) before processing.
	TokensUsed int `json:"tokensUsed,omitempty"`
	// TokensReturned is the token count of the final response text.
	TokensReturned int `json:"tokensReturned,omitempty"`
	// EstimatedCost is the estimated dollar cost of the call, computed from
	// request + response tokens at the KERN_COST_PER_TOKEN / cost_per_token
	// rate (default $0.00001/token). Omitted when zero.
	EstimatedCost float64 `json:"estimatedCost,omitempty"`
	// Savings is the token saving when the response is smaller than the
	// request (e.g. optimize-family tools). Omitted when zero (not
	// applicable).
	Savings int `json:"savings,omitempty"`
}

// defaultCostPerToken is the $/token rate used to estimate spend when
// KERN_COST_PER_TOKEN is unset. It mirrors internal/context's default so the
// MCP response metadata agrees with the context engine's spend estimates.
const defaultCostPerToken = 0.00001

// countRequestTokens counts the tokens consumed by a tool request: the tool
// name plus its serialized arguments. A serialization failure falls back to
// the bare tool name so counting never fails a call.
func countRequestTokens(tool string, args map[string]any) int {
	b, err := json.Marshal(args)
	if err != nil {
		return tokenize.Count(tool)
	}
	return tokenize.Count(tool + " " + string(b))
}

// tokenMetadataFor computes the token metadata for one tool call: request
// tokens before processing, response tokens after, the estimated cost, and the
// savings when the response shrank the payload (input > output).
func tokenMetadataFor(tool string, args map[string]any, out string) TokenMetadata {
	used := countRequestTokens(tool, args)
	returned := tokenize.Count(out)
	savings := used - returned
	if savings < 0 {
		savings = 0
	}
	rate := config.Float64("", "KERN_COST_PER_TOKEN", "cost_per_token", defaultCostPerToken)
	cost := float64(used+returned) * rate
	// Round to 6 decimals so float arithmetic noise never leaks into the
	// response (e.g. 0.0005700000000000001 stays 0.00057).
	cost = math.Round(cost*1e6) / 1e6
	return TokenMetadata{
		TokensUsed:     used,
		TokensReturned: returned,
		EstimatedCost:  cost,
		Savings:        savings,
	}
}

// attachTokenMetadata stamps result with the structured token metadata for the
// call. Every tools/call response path (executed tools, pre-tool denials and
// confinement-gate rejections) routes through here so clients always get the
// token ledger.
func attachTokenMetadata(result map[string]any, tool string, args map[string]any, out string) {
	result["tokenMetadata"] = tokenMetadataFor(tool, args, out)
}
