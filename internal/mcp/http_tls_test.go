package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTestCert generates a self-signed ECDSA certificate valid for
// 127.0.0.1/localhost and writes it to temp files, returning their paths.
func writeTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1", Organization: []string{"kern test"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

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

func TestServeHTTPContextWithTLSIncompleteConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, cfg := range []*TLSConfig{
		{CertFile: "cert.pem"},
		{KeyFile: "key.pem"},
		{},
	} {
		if err := ServeHTTPContextWithTLS(ctx, "127.0.0.1:0", cfg); err == nil {
			t.Fatalf("expected error for incomplete TLS config %+v", cfg)
		} else if !strings.Contains(err.Error(), "certificate and key") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestServeHTTPContextWithTLSServesHTTPS(t *testing.T) {
	certFile, keyFile := writeTestCert(t)

	// Pick a free loopback port (listen+close+reuse; standard test pattern).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ServeHTTPContextWithTLS(ctx, addr, &TLSConfig{CertFile: certFile, KeyFile: keyFile}) }()

	// Trust the self-signed cert through the cert pool (no InsecureSkipVerify).
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatal("failed to append test cert to pool")
	}
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}

	// Wait for readiness over TLS.
	deadline := time.Now().Add(15 * time.Second)
	healthy := false
	for time.Now().Before(deadline) {
		resp, err := client.Get("https://" + addr + "/health")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				healthy = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthy {
		t.Fatal("TLS server never became ready")
	}

	// A JSON-RPC initialize round-trips over TLS.
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + protocolVersion + `","capabilities":{},"clientInfo":{"name":"tls-test"}}}`
	req, err := http.NewRequest(http.MethodPost, "https://"+addr+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("initialize over TLS: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize over TLS: status %d: %s", resp.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("initialize over TLS: decode: %v (%s)", err, raw)
	}
	if _, ok := out["result"]; !ok {
		t.Fatalf("initialize over TLS: no result in %s", raw)
	}
}

func TestServeHTTPContextWithTLSPlainHTTPWhenNil(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ServeHTTPContextWithTLS(ctx, addr, nil) }()

	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(15 * time.Second)
	healthy := false
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/health")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				healthy = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !healthy {
		t.Fatal("plain-HTTP server never became ready")
	}

	// A TLS client must NOT be able to talk to the plain-HTTP listener.
	if _, err := client.Get("https://" + addr + "/health"); err == nil {
		t.Fatal("expected TLS handshake against plain-HTTP server to fail")
	}
}
