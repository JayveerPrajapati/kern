package fetch

import "testing"

func TestDocFetchAllowedDefaultDeniesPublicHost(t *testing.T) {
	if err := DocFetchAllowed("https://example.com/docs"); err == nil {
		t.Fatal("expected default docs-fetch host to be denied")
	}
}

func TestDocFetchAllowedExactAndSubdomain(t *testing.T) {
	t.Setenv("KERN_DOC_FETCH_DOMAINS", "go.dev, pkg.go.dev")
	if err := DocFetchAllowed("https://go.dev/ref/spec"); err != nil {
		t.Fatalf("exact host must be allowed: %v", err)
	}
	if err := DocFetchAllowed("https://pkg.go.dev/net/http"); err != nil {
		t.Fatalf("second exact host must be allowed: %v", err)
	}
	if err := DocFetchAllowed("https://example.com/docs"); err == nil {
		t.Fatal("unlisted host must be denied")
	}
}

func TestDocFetchAllowedWildcardOptIn(t *testing.T) {
	t.Setenv("KERN_DOC_FETCH_DOMAINS", "*")
	if err := DocFetchAllowed("https://example.com/docs"); err != nil {
		t.Fatalf("wildcard must allow any public host: %v", err)
	}
}

func TestDocFetchAllowedLoopbackOptIn(t *testing.T) {
	t.Setenv("KERN_ALLOW_LOOPBACK_FETCH", "1")
	if err := DocFetchAllowed("http://127.0.0.1:8080/docs"); err != nil {
		t.Fatalf("loopback opt-in must allow 127.0.0.1: %v", err)
	}
}
