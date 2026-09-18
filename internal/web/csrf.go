package web

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Cross-Origin / DNS-rebinding protection for state-mutating endpoints.
//
// The console authenticates with a bearer header, not cookies, so classic
// cookie-based CSRF does not apply. Two browser-borne vectors remain:
//
//  1. Cross-origin writes: a malicious page POSTs to the console. The
//     browser attaches an Origin header whose host differs from the
//     request Host. We reject mutating requests with a mismatched Origin.
//  2. DNS rebinding: the malicious page's domain resolves to 127.0.0.1, so
//     the browser sends Host: attacker.com AND Origin: http://attacker.com
//     (they match). The Origin/Host comparison cannot catch this — the
//     Host itself must be one we trust. Requests that carry an Origin
//     header (i.e. browser-initiated) are rejected unless their Host is in
//     the allowed set (loopback by default, extended via
//     KERN_WEB_ALLOWED_HOSTS for reverse-proxy deployments).
//
// curl/API clients send no Origin header, so neither check applies to
// them: scripted access (including bearer-token use against a
// network-exposed console) is unaffected.

// allowedHostsEnv extends the default loopback Host allowlist for
// reverse-proxy deployments (comma-separated hostnames).
const allowedHostsEnv = "KERN_WEB_ALLOWED_HOSTS"

// defaultAllowedHosts are the Host header values accepted for
// browser-initiated mutating requests when no override is set.
var defaultAllowedHosts = map[string]bool{
	"127.0.0.1": true,
	"localhost": true,
	"::1":       true,
	"[::1]":     true,
}

// allowedHosts resolves the effective Host allowlist: the loopback defaults
// plus any comma-separated KERN_WEB_ALLOWED_HOSTS entries.
func allowedHosts() map[string]bool {
	set := make(map[string]bool, len(defaultAllowedHosts)+4)
	for h := range defaultAllowedHosts {
		set[h] = true
	}
	for _, h := range strings.Split(os.Getenv(allowedHostsEnv), ",") {
		if h = strings.TrimSpace(h); h != "" {
			set[h] = true
		}
	}
	return set
}

// csrfViolation returns a non-empty reason when a state-mutating request
// must be rejected as cross-origin or DNS-rebinding. It applies only to
// browser-initiated requests: those carrying an Origin header. Requests
// without Origin (curl, SDKs, bearer-token clients) always pass.
func csrfViolation(r *http.Request, allowed map[string]bool) string {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return ""
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return "cross-origin request rejected: unparseable Origin"
	}
	// Vector 1: Origin host must match the request Host. Ports are
	// compared on the Host side only (Origin carries the scheme; the
	// console serves one port).
	if !hostMatches(u.Host, r.Host) {
		return "cross-origin request rejected: Origin host does not match Host"
	}
	// Vector 2 (DNS rebinding): a browser-initiated request whose Host is
	// outside the allowlist is a rebinding attempt (Host and Origin both
	// name the attacker's domain). The allowlist holds bare hostnames, so
	// the port is stripped from the Host header before the check.
	bareHost := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		bareHost = h
	}
	if !allowed[bareHost] {
		return "cross-origin request rejected: untrusted Host (DNS rebinding guard)"
	}
	return ""
}

// hostMatches compares an Origin host (may include a scheme and/or port) to
// the request Host (always includes the port for HTTP/1.1). A bare hostname
// matches the same hostname with the default port (80/443).
func hostMatches(originHost, reqHost string) bool {
	// Defensively strip a scheme prefix: callers normally pass url.Parse's
	// .Host (no scheme), but raw Origin values are tolerated.
	if i := strings.Index(originHost, "://"); i >= 0 {
		originHost = originHost[i+3:]
	}
	oh, _, err := net.SplitHostPort(originHost)
	if err != nil {
		oh = originHost
	}
	rh, rp, err := net.SplitHostPort(reqHost)
	if err != nil {
		return strings.EqualFold(oh, reqHost)
	}
	if !strings.EqualFold(oh, rh) {
		return false
	}
	// Origin omitting the port matches the standard port; otherwise ports
	// must be equal. (The console normally serves plain HTTP on 8090, so
	// the default-port rule keeps http://127.0.0.1 matching 127.0.0.1:8090.)
	return rp == "80" || rp == "443" || strings.HasSuffix(originHost, ":"+rp)
}
