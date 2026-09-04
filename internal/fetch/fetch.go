// Package fetch retrieves a web document and converts it to plain text so it
// can be indexed locally. It is the only network-touching package in kern, and
// every call is user-invoked (CLI `kern docs fetch` or MCP `kern_doc_fetch`):
// nothing in the runtime fetches on its own. Retrieved text is cached on disk
// under the kern cache dir, so a fetched page stays searchable offline.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	// DefaultMaxBytes caps a fetched body (HTML pages can be huge).
	DefaultMaxBytes = 4 << 20
	// fetchTimeout bounds the whole request including redirects.
	fetchTimeout = 20 * time.Second
	// maxRedirects bounds redirect following.
	maxRedirects = 5
)

// Result is the fetched and cleaned document.
type Result struct {
	Title string // <title> for HTML, otherwise ""
	Text  string // cleaned text (HTML stripped)
}

var (
	titleRe    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	commentRe  = regexp.MustCompile(`(?s)<!--.*?-->`)
	blockRe    = regexp.MustCompile(`(?i)</?(br|p|div|li|ul|ol|h[1-6]|tr|pre|blockquote|section|article|header|footer|table)[^>]*>`)
	tagRe      = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRe    = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankLines = regexp.MustCompile(`\n{3,}`)
)

// embeddedBlockRe strips a whole <script>…</script> style block. RE2 has no
// backreferences, so one compiled regex per tag name.
var embeddedBlockRes = func() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, tag := range []string{"script", "style", "noscript", "template"} {
		out = append(out, regexp.MustCompile(`(?is)<`+tag+`\b[^>]*>.*?</`+tag+`>`))
	}
	return out
}()

// Fetch downloads url (http/https only) and returns it as cleaned text. The
// body is capped at maxBytes (0 = DefaultMaxBytes). Non-HTML text bodies are
// returned as-is; binary content types are rejected. Private, loopback,
// link-local and unspecified destinations are refused (SSRF guard) — this
// covers both the literal host and every IP a hostname resolves to, and
// redirects are re-checked because they dial through the same guarded dialer.
func Fetch(rawURL string, maxBytes int) (*Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q (only http/https)", u.Scheme)
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	client := &http.Client{
		Timeout: fetchTimeout,
		Transport: &http.Transport{
			DialContext: dialContext,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("bad request: %w", err)
	}
	req.Header.Set("User-Agent", "kern-doc-fetch/1.0 (+local context optimizer)")
	req.Header.Set("Accept", "text/html, text/plain, text/markdown, */*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch failed: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}
	if len(body) > maxBytes {
		return nil, fmt.Errorf("body exceeds %d bytes", maxBytes)
	}

	ctype := resp.Header.Get("Content-Type")
	if !strings.Contains(ctype, "text/") && !strings.Contains(ctype, "html") && ctype != "" {
		return nil, fmt.Errorf("unexpected content type %q", ctype)
	}
	if strings.Contains(ctype, "html") {
		return htmlToText(body), nil
	}
	return &Result{Text: string(body)}, nil
}

// htmlToText strips HTML to readable plain text: <script>/<style> blocks are
// removed, block elements become newlines, tags are dropped and common
// entities are decoded. Deterministic and dependency-free.
func htmlToText(b []byte) *Result {
	s := string(b)
	res := &Result{}
	if m := titleRe.FindStringSubmatch(s); len(m) > 1 {
		res.Title = strings.TrimSpace(stripTags(m[1]))
	}
	for _, re := range embeddedBlockRes {
		s = re.ReplaceAllString(s, " ")
	}
	s = commentRe.ReplaceAllString(s, " ")
	s = blockRe.ReplaceAllString(s, "\n")
	s = stripTags(s)
	s = decodeEntities(s)
	s = spaceRe.ReplaceAllString(s, " ")
	s = blankLines.ReplaceAllString(s, "\n\n")
	res.Text = strings.TrimSpace(s)
	return res
}

func stripTags(s string) string { return strings.TrimSpace(tagRe.ReplaceAllString(s, " ")) }

// privateIP reports whether ip must never be fetched: loopback, private,
// link-local (unicast and multicast), unspecified, or multicast. These cover
// local services, RFC 1918/4193 nets, and cloud metadata (169.254.169.254).
func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// dialContext performs the guarded connection; tests swap it for a variant
// that tolerates loopback servers.
var dialContext = guardedDialContext

// guardedDialContext resolves the host up front and rejects the connection
// when any candidate address is private, then dials the first public address
// directly. Resolving and checking here (rather than only validating the URL)
// means DNS rebinding and redirects cannot smuggle a request to an internal
// host: every dial — initial and redirected — passes through this check.
func guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return guardedDial(ctx, network, addr, false)
}

// guardedAllowLoopbackDialContext is the test variant: same guard, but a
// loopback address is allowed so httptest servers keep working.
func guardedAllowLoopbackDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return guardedDial(ctx, network, addr, true)
}

func guardedDial(ctx context.Context, network, addr string, allowLoopback bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host == "" {
		return nil, fmt.Errorf("empty host in %q", addr)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses for %s", host)
	}
	for _, ip := range ips {
		if privateIP(ip) && !(ip.IsLoopback() && (allowLoopback || allowLoopbackEnv())) {
			return nil, fmt.Errorf("refusing to fetch private address %s (host %s)", ip, host)
		}
	}
	return (&net.Dialer{Timeout: fetchTimeout}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

// allowLoopbackEnv opts out of the loopback rejection only: set
// KERN_ALLOW_LOOPBACK_FETCH=1 to fetch docs served on localhost (e.g. a
// local doc site). Private/link-local/metadata addresses stay blocked.
func allowLoopbackEnv() bool {
	return os.Getenv("KERN_ALLOW_LOOPBACK_FETCH") == "1"
}

var entityRe = regexp.MustCompile(`&(?:#x([0-9a-fA-F]+)|#([0-9]+)|([a-zA-Z]+));`)

var namedEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": "\"", "apos": "'",
	"nbsp": " ", "hellip": "...", "mdash": "-", "ndash": "-",
	"ldquo": "\"", "rdquo": "\"", "lsquo": "'", "rsquo": "'",
	"copy": "©", "reg": "®", "trade": "™", "times": "×", "divide": "÷",
}

func decodeEntities(s string) string {
	return entityRe.ReplaceAllStringFunc(s, func(m string) string {
		g := entityRe.FindStringSubmatch(m)
		if len(g) != 4 {
			return m
		}
		var cp int64
		switch {
	case g[1] != "":
		_, _ = fmt.Sscanf(g[1], "%x", &cp)
	case g[2] != "":
		_, _ = fmt.Sscanf(g[2], "%d", &cp)
		default:
			if v, ok := namedEntities[g[3]]; ok {
				return v
			}
			return m
		}
		if cp > 0 {
			return string(rune(cp))
		}
		return m
	})
}
