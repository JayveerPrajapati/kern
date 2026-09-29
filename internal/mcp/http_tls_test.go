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

	"github.com/JayveerPrajapati/kern/internal/mcp/transport"
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

func TestServeHTTPContextWithTLSIncompleteConfig(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, cfg := range []*transport.TLSConfig{
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
	t.Parallel()
	certFile, keyFile := writeTestCert(t)

	// Bind a free loopback port ONCE and hand the listener to the server.
	// The listen-close-rebind pattern races under parallel -race runs: the
	// OS reuses the just-freed port for another test's server, which then
	// serves a different cert and the readiness probe times out.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }() // error-return path: ownership reverts
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = ServeHTTPContextWithTLSOn(ctx, ln, &transport.TLSConfig{CertFile: certFile, KeyFile: keyFile})
	}()

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
		// Generous per-request timeout: under the full -race CI suite the
		// machine is heavily oversubscribed and an ECDSA TLS handshake can
		// exceed a tight timeout even though the server is healthy.
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}

	// Wait for readiness over TLS. The deadline is generous (60s) so the
	// probe survives the parallel -race load of the whole CI race step;
	// the test's contract is "TLS serving works", not a startup benchmark.
	deadline := time.Now().Add(60 * time.Second)
	healthy := false
	for time.Now().Before(deadline) {
		resp, err := client.Get("https://" + addr + "/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
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
	defer func() { _ = resp.Body.Close() }()
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
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }() // error-return path: ownership reverts
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ServeHTTPContextWithTLSOn(ctx, ln, nil) }()

	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	healthy := false
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
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
