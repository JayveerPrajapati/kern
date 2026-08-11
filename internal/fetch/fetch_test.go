package fetch

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// allowLoopback swaps in the guarded-but-loopback-tolerant dialer for tests
// that run an httptest server on 127.0.0.1.
func allowLoopback(t *testing.T) {
	t.Helper()
	old := dialContext
	dialContext = guardedAllowLoopbackDialContext
	t.Cleanup(func() { dialContext = old })
}

func TestHtmlToTextStripsMarkup(t *testing.T) {
	html := `<html><head><title>Install Guide</title><style>body{color:red}</style></head>
<body><nav><a href="/x">skip</a></nav>
<h1>Welcome</h1><p>Run <code>kern setup</code> &amp; enjoy.</p>
<script>var x = 1;</script>
<ul><li>one</li><li>two</li></ul></body></html>`
	res := htmlToText([]byte(html))
	if res.Title != "Install Guide" {
		t.Fatalf("title = %q", res.Title)
	}
	for _, want := range []string{"Welcome", "kern setup", "&", "one", "two"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("text missing %q:\n%s", want, res.Text)
		}
	}
	for _, bad := range []string{"<p>", "<script", "var x", "color:red", "<title>"} {
		if strings.Contains(res.Text, bad) {
			t.Fatalf("text contains markup %q:\n%s", bad, res.Text)
		}
	}
}

func TestFetchRejectsNonHTTP(t *testing.T) {
	if _, err := Fetch("file:///etc/passwd", 0); err == nil {
		t.Fatal("file:// must be rejected")
	}
	if _, err := Fetch("ftp://example.com/a", 0); err == nil {
		t.Fatal("ftp:// must be rejected")
	}
}

func TestFetchRoundTrip(t *testing.T) {
	allowLoopback(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<title>Docs</title><h1>Hello</h1><p>world</p>`))
	}))
	defer srv.Close()

	res, err := Fetch(srv.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Docs" || !strings.Contains(res.Text, "Hello") {
		t.Fatalf("bad result: %+v", res)
	}
}

func TestFetchCapsBody(t *testing.T) {
	allowLoopback(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("a", 1000)))
	}))
	defer srv.Close()
	if _, err := Fetch(srv.URL, 100); err == nil {
		t.Fatal("expected size-cap error")
	}
}

func TestFetchRejectsBinaryContentType(t *testing.T) {
	allowLoopback(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("zzz"))
	}))
	defer srv.Close()
	if _, err := Fetch(srv.URL, 0); err == nil {
		t.Fatal("binary content type must be rejected")
	}
}

func TestFetchRejectsPrivateAddresses(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1/x",
		"http://localhost/x",
		"http://[::1]/x",
		"http://10.0.0.1/x",
		"http://192.168.1.1/x",
		"http://172.16.0.1/x",
		"http://169.254.169.254/latest/meta-data/",
	} {
		if _, err := Fetch(u, 0); err == nil {
			t.Fatalf("%s: expected private-address rejection", u)
		}
	}
}

func TestFetchRedirectToPrivateRejected(t *testing.T) {
	allowLoopback(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()
	_, err := Fetch(srv.URL, 0)
	if err == nil {
		t.Fatal("redirect to private address must be rejected")
	}
	if !strings.Contains(err.Error(), "private address") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrivateIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.0.1", true},
		{"169.254.169.254", true},
		{"0.0.0.0", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
	}
	for _, c := range cases {
		if got := privateIP(net.ParseIP(c.ip)); got != c.want {
			t.Fatalf("privateIP(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}
