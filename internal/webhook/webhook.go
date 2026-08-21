// Package webhook delivers internal system events to outbound HTTP webhooks
// (master spec §2 "interfaces": webhooks). It is a small, defensive, stdlib-only
// adapter: it POSTs eventbus events as JSON to registered URLs and reports any
// delivery failures without panicking. A dead URL never crashes the caller.
package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// defaultTimeout bounds each outbound delivery request.
const defaultTimeout = 5 * time.Second

// Client delivers eventbus events to registered webhook URLs over HTTP.
// It is safe for concurrent use: the hook table is guarded by a mutex.
//
// Idempotency (Invariant 9): a delivered event is never redelivered to the
// same URL. The dedup key is "eventID|url"; once an event has been POSTed to
// a URL (successfully or not — the attempt itself is the dedup point), it is
// skipped on subsequent Deliver calls. This makes redelivery a no-op for the
// same event to the same URL.
type Client struct {
	mu        sync.Mutex
	hooks     map[string]string // name -> url
	http      *http.Client
	timeout   time.Duration
	delivered map[string]bool // "eventID|url" -> true (Invariant 9 idempotency)
}

// New returns an empty webhook client with the default 5s delivery timeout.
func New() *Client {
	return &Client{
		hooks: map[string]string{},
		http: &http.Client{
			Timeout: defaultTimeout,
			// Never follow redirects when delivering a webhook: a compromised or
			// malicious URL could otherwise redirect us to an internal service
			// (SSRF). Treat any redirect as the final response.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		timeout:   defaultTimeout,
		delivered: map[string]bool{},
	}
}

// SetTimeout overrides the per-delivery request timeout.
func (c *Client) SetTimeout(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeout = d
	c.http.Timeout = d
}

// Add registers a webhook URL under name. It rejects URLs whose scheme is not
// http/https, URLs that target a loopback, private or link-local address (SSRF
// defense-in-depth), and duplicate names.
func (c *Client) Add(name, u string) error {
	parsed, err := url.Parse(u)
	if err != nil {
		return fmt.Errorf("webhook: invalid url %q: %w", u, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("webhook: unsupported scheme %q (must be http or https)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("webhook: url %q missing host", u)
	}
	if restricted, why := restrictedHost(parsed.Host); restricted {
		return fmt.Errorf("webhook: refusing url %q: host resolves to a %s address", u, why)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.hooks[name]; ok {
		return fmt.Errorf("webhook: duplicate hook name %q", name)
	}
	c.hooks[name] = u
	return nil
}

// restrictedHost reports whether host refers to a loopback, private, link-local
// or otherwise non-routable literal address that should not be the target of a
// server-initiated outbound request (SSRF defense-in-depth). Domain names are
// not resolved here (that would require DNS at registration time), so only the
// literal IP ranges and the "localhost" alias are rejected.
func restrictedHost(host string) (bool, string) {
	h := host
	// Strip an optional port: "[::1]:443" or "127.0.0.1:8080".
	if strings.HasPrefix(h, "[") {
		if j := strings.LastIndex(h, "]"); j >= 0 {
			h = h[:j+1]
		}
	} else if i := strings.LastIndex(h, ":"); i >= 0 {
		h = h[:i]
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true, "loopback (localhost)"
	}
	ip := net.ParseIP(h)
	if ip == nil {
		// Not a literal IP (e.g. a domain name); skip DNS-based checks.
		return false, ""
	}
	switch {
	case ip.IsLoopback():
		return true, "loopback"
	case ip.IsPrivate():
		return true, "private"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return true, "link-local"
	case ip.IsUnspecified():
		return true, "unspecified"
	}
	return false, ""
}

// Remove unregisters the hook named name. Removing an unknown name is a no-op.
func (c *Client) Remove(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.hooks, name)
}

// URLs returns the currently registered webhook URLs, sorted for determinism.
func (c *Client) URLs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.hooks))
	for _, u := range c.hooks {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// Deliver POSTs the event as JSON to every registered URL and returns a map of
// url -> error for the deliveries that failed. An empty map means every
// delivery succeeded. It is defensive: a malformed or unreachable URL produces
// an entry in the error map rather than a panic.
func (c *Client) Deliver(ev eventbus.Event) map[string]error {
	c.mu.Lock()
	urls := make([]string, 0, len(c.hooks))
	for _, u := range c.hooks {
		// Invariant 9 idempotency: skip URLs that have already received this
		// event ID. The dedup key is "eventID|url".
		key := ev.ID + "|" + u
		if c.delivered[key] {
			continue
		}
		c.delivered[key] = true
		urls = append(urls, u)
	}
	cli := c.http
	c.mu.Unlock()

	// Project only the stable event fields; timestamps serialize as RFC3339.
	payload, err := json.Marshal(map[string]interface{}{
		"id":          ev.ID,
		"kind":        string(ev.Kind),
		"source":      ev.Source,
		"subject":     ev.Subject,
		"service":     ev.Service,
		"occurred_at": ev.OccurredAt,
	})
	if err != nil {
		errs := map[string]error{}
		for _, u := range urls {
			errs[u] = err
		}
		return errs
	}

	errs := map[string]error{}
	for _, u := range urls {
		req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(payload))
		if err != nil {
			errs[u] = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := cli.Do(req)
		if err != nil {
			errs[u] = err
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			errs[u] = fmt.Errorf("webhook: %s returned status %d", u, resp.StatusCode)
		}
	}
	return errs
}
