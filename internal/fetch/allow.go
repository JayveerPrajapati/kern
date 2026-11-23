package fetch

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// DocFetchAllowed enforces the operator-approved domain allowlist for
// kern_doc action=fetch and `kern docs fetch`.
//
// KERN_DOC_FETCH_DOMAINS is a comma-separated allowlist. `*` is the explicit
// operator opt-in for arbitrary public URLs. KERN_ALLOW_LOOPBACK_FETCH=1 is a
// separate opt-in for loopback hosts; public domains still require
// KERN_DOC_FETCH_DOMAINS.
func DocFetchAllowed(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("url has no host")
	}
	if os.Getenv("KERN_ALLOW_LOOPBACK_FETCH") == "1" {
		switch host {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
	}
	for _, domain := range strings.Split(os.Getenv("KERN_DOC_FETCH_DOMAINS"), ",") {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		if domain == "*" || host == domain || strings.HasSuffix(host, "."+domain) {
			return nil
		}
	}
	return fmt.Errorf("docs fetch host %q is not in KERN_DOC_FETCH_DOMAINS", host)
}
