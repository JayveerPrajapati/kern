// Package govtoken implements signed agent identities for org mode:
// HMAC-SHA256 bearer tokens that prove an asserted agent_id actually holds
// the identity's enrolled secret. agent_id alone is client-asserted and
// untrusted — the MCP boundary (internal/mcp/gov, internal/mcp/governance)
// fails closed for agents enrolled with a TokenSecret until a valid token
// verifies. The package is a leaf (govrbac precedent): it depends only on
// the AgentIdentity type, never on the registry or firewall.
package govtoken

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// tokenWindow is the default validity window for an agent token: an
// issued token proves the bearer holds the agent's secret for a bounded
// period around its embedded timestamp.
const tokenWindow = 5 * time.Minute

// Generate mints a fresh 32-byte hex HMAC secret for agent-token
// enrollment. Callers print it exactly once at enrollment; it is also
// persisted to the (0600) agent store so the server can verify tokens.
func Generate() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("govtoken: entropy source unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Issue returns a fresh bearer token for the given agent:
// "<unix-seconds>.<hex HMAC-SHA256(secret, id|unix)>". The secret must be
// enrolled (non-empty); token issuance is the enrollment counterpart of
// `kern org agents token <id>`.
func Issue(a *governance.AgentIdentity, now time.Time) (string, error) {
	if a == nil || a.TokenSecret == "" {
		return "", fmt.Errorf("govtoken: agent %q has no token secret enrolled (register with --generate-secret)", idOf(a))
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(a.TokenSecret))
	mac.Write([]byte(a.ID + "|" + ts))
	return ts + "." + hex.EncodeToString(mac.Sum(nil)), nil
}

// Verify checks a bearer token against the agent's enrolled
// secret. It fails closed on every malformed input, an expired timestamp
// (outside the window around now), and any HMAC mismatch (constant-time).
// Within the window a token is replayable — the minimal protocol trades
// replay-proof nonces for zero server-side state; the window bounds the
// exposure.
func Verify(a *governance.AgentIdentity, token string, now time.Time) error {
	if a == nil || a.TokenSecret == "" {
		return fmt.Errorf("govtoken: agent has no token secret enrolled")
	}
	ts, sig, ok := strings.Cut(token, ".")
	if !ok || ts == "" || sig == "" {
		return fmt.Errorf("govtoken: malformed agent token")
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("govtoken: malformed agent token timestamp")
	}
	if d := now.Sub(time.Unix(unix, 0)); d > tokenWindow || d < -tokenWindow {
		return fmt.Errorf("govtoken: agent token expired (issued %s, window %s)", time.Unix(unix, 0).UTC().Format(time.RFC3339), tokenWindow)
	}
	mac := hmac.New(sha256.New, []byte(a.TokenSecret))
	mac.Write([]byte(a.ID + "|" + ts))
	if !hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return fmt.Errorf("govtoken: agent token signature mismatch")
	}
	return nil
}

// idOf safely formats an agent's ID for error messages.
func idOf(a *governance.AgentIdentity) string {
	if a == nil {
		return "<nil>"
	}
	return a.ID
}
