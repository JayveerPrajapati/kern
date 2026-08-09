// Package fetch retrieves a web document and converts it to plain text so it
// can be indexed locally. It is the only network-touching package in kern, and
// every call is user-invoked (CLI `kern docs fetch` or MCP `kern_doc_fetch`):
// nothing in the runtime fetches on its own. Retrieved text is cached on disk
// under the kern cache dir, so a fetched page stays searchable offline.
package fetch

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
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
// returned as-is; binary content types are rejected.
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
			fmt.Sscanf(g[1], "%x", &cp)
		case g[2] != "":
			fmt.Sscanf(g[2], "%d", &cp)
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
