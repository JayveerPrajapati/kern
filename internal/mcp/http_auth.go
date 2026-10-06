package mcp

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"
)

// mcpAuthTokenEnv opts the HTTP MCP transport into bearer authentication.
// It is deliberately separate from KERN_AUTH_TOKEN (web console / enterprise
// server): a machine that already exports that variable must not have its MCP
// clients start failing with 401 after an upgrade.
const mcpAuthTokenEnv = "KERN_MCP_AUTH_TOKEN"

func mcpAuthToken() string {
	return strings.TrimSpace(os.Getenv(mcpAuthTokenEnv))
}

// withMCPAuth wraps next so every request must carry "Authorization: Bearer
// <token>". An empty token returns next unchanged (auth off, the default: the
// unix-socket / loopback transport is already owner-only).
func withMCPAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	if token == "" {
		return next
	}
	want := []byte(token)
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) &&
			subtle.ConstantTimeCompare([]byte(h[len(prefix):]), want) == 1 {
			next(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="kern-mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}
