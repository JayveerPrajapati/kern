package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTLSConfigValid(t *testing.T) {
	cases := []struct {
		name string
		cfg  *TLSConfig
		want bool
	}{
		{"nil", nil, false},
		{"complete", &TLSConfig{CertFile: "c.pem", KeyFile: "k.pem"}, true},
		{"missing cert", &TLSConfig{KeyFile: "k.pem"}, false},
		{"missing key", &TLSConfig{CertFile: "c.pem"}, false},
		{"empty", &TLSConfig{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Valid(); got != tc.want {
				t.Fatalf("Valid() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTLSOptionsFromEnv(t *testing.T) {
	t.Run("unset returns nil", func(t *testing.T) {
		t.Setenv("KERN_MCP_TLS_CERT", "")
		t.Setenv("KERN_MCP_TLS_KEY", "")
		if cfg := TLSOptionsFromEnv(); cfg != nil {
			t.Fatalf("expected nil, got %+v", cfg)
		}
	})
	t.Run("both set is valid", func(t *testing.T) {
		t.Setenv("KERN_MCP_TLS_CERT", "/tmp/c.pem")
		t.Setenv("KERN_MCP_TLS_KEY", "/tmp/k.pem")
		cfg := TLSOptionsFromEnv()
		if cfg == nil || !cfg.Valid() {
			t.Fatalf("expected valid config, got %+v", cfg)
		}
	})
	t.Run("partial is invalid", func(t *testing.T) {
		t.Setenv("KERN_MCP_TLS_CERT", "/tmp/c.pem")
		t.Setenv("KERN_MCP_TLS_KEY", "")
		cfg := TLSOptionsFromEnv()
		if cfg == nil {
			t.Fatal("expected non-nil config for partial env")
		}
		if cfg.Valid() {
			t.Fatal("expected partial env config to be invalid")
		}
	})
}

func TestTLSOptionsPrecedence(t *testing.T) {
	t.Setenv("KERN_MCP_TLS_CERT", "/env/c.pem")
	t.Setenv("KERN_MCP_TLS_KEY", "/env/k.pem")
	t.Run("flags win", func(t *testing.T) {
		cfg := TLSOptions("/flag/c.pem", "/flag/k.pem")
		if cfg.CertFile != "/flag/c.pem" || cfg.KeyFile != "/flag/k.pem" {
			t.Fatalf("expected flag paths, got %+v", cfg)
		}
	})
	t.Run("env fills missing flag", func(t *testing.T) {
		cfg := TLSOptions("/flag/c.pem", "")
		if cfg.CertFile != "/flag/c.pem" || cfg.KeyFile != "/env/k.pem" {
			t.Fatalf("expected merged paths, got %+v", cfg)
		}
	})
	t.Run("env only", func(t *testing.T) {
		cfg := TLSOptions("", "")
		if cfg.CertFile != "/env/c.pem" || cfg.KeyFile != "/env/k.pem" {
			t.Fatalf("expected env paths, got %+v", cfg)
		}
	})
	t.Run("nothing set", func(t *testing.T) {
		t.Setenv("KERN_MCP_TLS_CERT", "")
		t.Setenv("KERN_MCP_TLS_KEY", "")
		if cfg := TLSOptions("", ""); cfg != nil {
			t.Fatalf("expected nil, got %+v", cfg)
		}
	})
}

func TestLocalhostAddr(t *testing.T) {
	cases := []struct {
		addr string
		want string
		fail bool
	}{
		{"", "127.0.0.1:8080", false},
		{":9000", "127.0.0.1:9000", false},
		{"9000", "127.0.0.1:9000", false},
		{"localhost:8080", "127.0.0.1:8080", false},
		{"127.0.0.1:8080", "127.0.0.1:8080", false},
		{"0.0.0.0:8080", "127.0.0.1:8080", false},
		{"[::1]:8080", "127.0.0.1:8080", false},
		{"0.0.0.0:8080", "127.0.0.1:8080", false},
		{"evil.example:8080", "", true},
	}
	for _, c := range cases {
		got, err := LocalhostAddr(c.addr)
		if c.fail {
			if err == nil {
				t.Errorf("LocalhostAddr(%q) = %q, want error", c.addr, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("LocalhostAddr(%q) error: %v", c.addr, err)
			continue
		}
		if got != c.want {
			t.Errorf("LocalhostAddr(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}

func TestIsLocalhostOrigin(t *testing.T) {
	cases := []struct {
		origin string
		want   bool
	}{
		{"", true},
		{"http://localhost:5173", true},
		{"http://127.0.0.1:8080", true},
		{"https://[::1]:9999", true},
		{"https://evil.example", false},
		{"not a url", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(""))
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if got := IsLocalhostOrigin(req); got != c.want {
			t.Errorf("IsLocalhostOrigin(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
}
