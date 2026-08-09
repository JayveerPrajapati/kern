package fetch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("zzz"))
	}))
	defer srv.Close()
	if _, err := Fetch(srv.URL, 0); err == nil {
		t.Fatal("binary content type must be rejected")
	}
}
