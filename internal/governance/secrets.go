// Package governance secret stripping: defense-in-depth filtering of
// secret-carrying environment variables before a subprocess spawn.
package governance

import (
	"strings"
)

// SecretFilter defines which environment variables StripSecrets removes from
// a subprocess environment. Matching is on the variable NAME only, never the
// value (values may be arbitrarily encoded). An empty (zero-value) filter
// strips nothing — safe no-op.
type SecretFilter struct {
	Patterns   []string // case-insensitive substrings matched against the name
	Exact      []string // exact names always stripped
	Allowlist  []string // exact names always kept, even when matching a pattern
	RedactWith string   // replacement value; "" drops the variable entirely
}

// DefaultSecretFilter returns the built-in filter: strip common secret
// carriers (token/secret/password/api_key/access_key/private_key/client_secret/
// credential/authorization/bearer/jwt/webhook/signing_key/signing_secret/
// session_key/encryption_key patterns; AWS/GitHub/GitLab/npm/OpenAI/Anthropic/
// Google/KERN_*_TOKEN exact names) while keeping kern's own operational vars
// (KERN_EMBED_MODEL, KERN_LLM_PROVIDER, KERN_ALLOW_*, KERN_SANDBOX_MAX_SNAPSHOT_BYTES,
// KERN_DEPLOY_TIMEOUT, KERN_COST_PER_TOKEN, PATH, HOME, LANG, LC_ALL, LC_CTYPE,
// TERM, TZ, TMPDIR, TMP, TEMP).
func DefaultSecretFilter() SecretFilter {
	return SecretFilter{
		Patterns: []string{
			"token", "secret", "password", "api_key", "access_key", "private_key",
			"client_secret", "credential", "authorization", "bearer", "jwt",
			"webhook", "signing_key", "signing_secret", "session_key", "encryption_key",
		},
		Exact: []string{
			"AWS_SECRET_ACCESS_KEY", "AWS_ACCESS_KEY_ID", "AWS_SESSION_TOKEN",
			"GITHUB_TOKEN", "GITLAB_TOKEN", "NPM_TOKEN",
			"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY",
		},
		Allowlist: []string{
			"KERN_EMBED_MODEL", "KERN_LLM_PROVIDER", "KERN_ALLOW_NET",
			"KERN_ALLOW_NO_ISOLATE", "KERN_ALLOW_UNISOLATED",
			"KERN_SANDBOX_MAX_SNAPSHOT_BYTES", "KERN_DEPLOY_TIMEOUT", "KERN_COST_PER_TOKEN",
			"PATH", "HOME", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "TZ",
			"TMPDIR", "TMP", "TEMP",
		},
		RedactWith: "",
	}
}

// IsSecret reports whether an env var NAME should be stripped. Allowlist wins
// over everything; then exact names; then case-insensitive substring patterns.
func (f SecretFilter) IsSecret(name string) bool {
	for _, a := range f.Allowlist {
		if a == name {
			return false
		}
	}
	for _, e := range f.Exact {
		if e == name {
			return true
		}
	}
	lower := strings.ToLower(name)
	for _, p := range f.Patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// StripSecrets returns a copy of env with secret-carrying variables removed
// (or replaced by RedactWith when set). Entries without '=' are dropped.
func StripSecrets(env []string, f SecretFilter) []string {
	if env == nil {
		return nil
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		name := kv[:eq]
		if !f.IsSecret(name) {
			out = append(out, kv)
			continue
		}
		if f.RedactWith != "" {
			out = append(out, name+"="+f.RedactWith)
		}
	}
	return out
}
