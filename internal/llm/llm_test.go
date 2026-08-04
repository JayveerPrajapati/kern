package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := New("")
	if c.Model != DefaultModel {
		t.Fatalf("model = %q, want %q", c.Model, DefaultModel)
	}
	if c.Base == "" {
		t.Fatal("base URL must default to localhost")
	}
}

func TestAvailableFalseWhenUnreachable(t *testing.T) {
	// Point at a closed port on localhost; connect should be refused fast.
	c := New("test-model")
	c.Base = "http://127.0.0.1:1"
	if c.Available() {
		t.Fatal("expected unreachable server to be unavailable")
	}
}

func TestCompressUsesServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[]}`))
			return
		}
		if r.URL.Path != "/api/generate" {
			t.Fatalf("path = %q, want /api/generate", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("bad body: %v", err)
		}
		if body["model"] != "llama-x" {
			t.Fatalf("model = %v, want llama-x", body["model"])
		}
		if body["stream"] != false {
			t.Fatalf("stream = %v, want false", body["stream"])
		}
		if !strings.Contains(body["prompt"].(string), "hello kern") {
			t.Fatalf("prompt missing input text")
		}
		w.Write([]byte(`{"response":"hello"}`))
	}))
	defer srv.Close()

	c := New("llama-x")
	c.Base = srv.URL
	got, err := c.Compress("hello kern")
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if got != "hello" {
		t.Fatalf("got %q, want hello", got)
	}
}

func TestCompressEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"response":"   "}`))
	}))
	defer srv.Close()

	c := New("")
	c.Base = srv.URL
	if _, err := c.Compress("x"); err == nil {
		t.Fatal("expected error on empty response")
	}
}

func TestCompressNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New("")
	c.Base = srv.URL
	if _, err := c.Compress("x"); err == nil {
		t.Fatal("expected error on non-200")
	}
}

func TestCompressUnreachable(t *testing.T) {
	c := New("")
	c.Base = "http://127.0.0.1:1"
	if _, err := c.Compress("x"); err == nil {
		t.Fatal("expected error when unreachable")
	}
}
