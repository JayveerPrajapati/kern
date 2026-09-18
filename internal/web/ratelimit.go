package web

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rate limiting for the web console's state-mutating surface.
//
// The console defaults to a loopback bind (trusted local), so the limiter
// is generous by default (600 req/min per IP) — it bounds a misbehaving
// client (e.g. an agent loop polling an LLM-heavy endpoint) without getting
// in the way of interactive use. When the console is network-exposed, the
// operator should lower it via KERN_WEB_RATE_LIMIT. A value of "0" disables
// rate limiting entirely (explicit opt-out only; anything unset or invalid
// keeps the default).

// rateLimitEnv selects the per-IP state-mutating request limit (req/min).
const rateLimitEnv = "KERN_WEB_RATE_LIMIT"

// defaultRateLimit is the per-IP requests-per-minute cap applied when the
// env var is unset. Generous enough for a loopback dashboard polling the
// API, tight enough to stop a single runaway client from saturating the
// console.
const defaultRateLimit = 600

// ipBucket is one client's request count within the current fixed window.
type ipBucket struct {
	count int
	at    time.Time
}

// rateLimiter is a fixed-window per-IP limiter over state-mutating
// requests. It is safe for concurrent use; bucket entries are created on
// demand and are naturally garbage-collected when the client's window rolls
// over (the old bucket is replaced, never leaked).
type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*ipBucket
}

// newRateLimiter builds a limiter with the given per-IP window cap. A
// non-positive limit returns nil, which callers treat as "no limiting".
func newRateLimiter(limit int) *rateLimiter {
	if limit <= 0 {
		return nil
	}
	return &rateLimiter{
		limit:   limit,
		window:  time.Minute,
		buckets: make(map[string]*ipBucket),
	}
}

// rateLimitFromEnv resolves the configured per-IP limit. Unset or invalid
// values keep the default; "0" explicitly disables limiting (nil limiter).
func rateLimitFromEnv() *rateLimiter {
	v := strings.TrimSpace(os.Getenv(rateLimitEnv))
	if v == "" {
		return newRateLimiter(defaultRateLimit)
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return newRateLimiter(defaultRateLimit)
	}
	return newRateLimiter(n)
}

// allow reports whether a request from ip may proceed. When it may not,
// retryAfter is the seconds until the window resets (>= 1).
func (rl *rateLimiter) allow(ip string, now time.Time) (ok bool, retryAfter int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, found := rl.buckets[ip]
	if !found || now.Sub(b.at) >= rl.window {
		b = &ipBucket{count: 0, at: now}
		rl.buckets[ip] = b
	}
	b.count++
	if b.count > rl.limit {
		retry := int(rl.window - now.Sub(b.at))
		if retry < 1 {
			retry = 1
		}
		return false, retry
	}
	return true, 0
}

// clientIP extracts the request's remote host, stripping the port. When the
// address is malformed (should not happen with net/http), the raw value is
// returned so the key is still stable for a given peer.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// mutatingMethod reports whether the request method can change server state
// or trigger heavy LLM/execution work. Only these are rate-limited: the
// read-only dashboard surface stays unthrottled.
func mutatingMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
