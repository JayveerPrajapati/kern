package index

import (
	"strings"
	"testing"
)

// TestSynthesizeDispatchEdgesNetHTTP: http.HandleFunc/Handle registrations
// synthesize MEDIUM router:net-http edges from the setup function to the
// handlers.
func TestSynthesizeDispatchEdgesNetHTTP(t *testing.T) {
	src := `package main

import "net/http"

func rootHandler(w http.ResponseWriter, r *http.Request) {}

func setup() {
	http.HandleFunc("/", rootHandler)
	http.Handle("/health", http.HandlerFunc(healthHandler))
}
`
	syms, calls, _, _, err := extract("main.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	_ = syms
	edges := calls["setup"]
	want := []struct {
		target string
		synth  string
	}{
		{"rootHandler", "router:net-http"},
		{"healthHandler", "router:net-http"},
	}
	for _, w := range want {
		ok := false
		for _, e := range edges {
			if e.Target == w.target {
				ok = true
				if e.Confidence != ConfidenceMedium {
					t.Errorf("setup -> %s confidence = %s, want MEDIUM", w.target, e.Confidence)
				}
				if e.Synth != w.synth {
					t.Errorf("setup -> %s synth = %q, want %q", w.target, e.Synth, w.synth)
				}
			}
		}
		if !ok {
			t.Errorf("setup -> %s missing from %v", w.target, edges)
		}
	}
}

// TestSynthesizeDispatchEdgesChiGin: verb registrations on router-shaped
// variables (chi r.Get, gin engine.GET) synthesize router:http-route edges;
// chi-specific group methods synthesize router:chi.
func TestSynthesizeDispatchEdgesChiGin(t *testing.T) {
	src := `package main

import "net/http"

func listUsers(w http.ResponseWriter, r *http.Request) {}
func ping(w http.ResponseWriter, r *http.Request) {}

func main() {
	r := newRouter()
	r.Get("/users", listUsers)
	r.Post("/users", createUser)
	engine := newGin()
	engine.GET("/ping", ping)
	engine.Group("/admin", adminGroup)
}
`
	_, calls, _, _, err := extract("main.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	edges := calls["main"]
	for _, w := range []struct{ target, synth string }{
		{"listUsers", "router:http-route"},
		{"createUser", "router:http-route"},
		{"ping", "router:http-route"},
		{"adminGroup", "router:chi"},
	} {
		found := false
		for _, e := range edges {
			if e.Target == w.target {
				found = true
				if e.Synth != w.synth {
					t.Errorf("main -> %s synth = %q, want %q", w.target, e.Synth, w.synth)
				}
			}
		}
		if !found {
			t.Errorf("main -> %s missing from %v", w.target, edges)
		}
	}
}

// TestSynthesizeDispatchEdgesIgnoresStdlibAndClosures: http.Get(url) is not
// a registration (http is not a router variable), closures have no named
// target, and a syntactic call already recorded wins over synthesis.
func TestSynthesizeDispatchEdgesIgnoresStdlibAndClosures(t *testing.T) {
	src := `package main

import "net/http"

func direct() {}

func setup() {
	http.Get("https://example.com") // stdlib client call, NOT a registration
	r := newRouter()
	r.Get("/anon", func(w http.ResponseWriter, r *http.Request) {})
	direct() // syntactic call already recorded — no synth duplicate
}
`
	_, calls, _, _, err := extract("main.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	edges := calls["setup"]
	for _, e := range edges {
		// http.Get(url) is a syntactic client call (HIGH, no synth) — it
		// must never be marked as a router dispatch.
		if strings.Contains(e.Target, "http.Get") && e.Synth != "" {
			t.Errorf("http.Get got a synthesis marker: %+v", e)
		}
		if e.Synth != "" {
			t.Errorf("unexpected synthesized edge %+v", e)
		}
	}
	// direct() is the syntactic edge; it must not be duplicated with Synth.
	directCount, synthCount := 0, 0
	for _, e := range edges {
		if e.Target == "direct" {
			directCount++
			if e.Synth != "" {
				synthCount++
			}
		}
	}
	if directCount != 1 || synthCount != 0 {
		t.Errorf("direct edges = %d (synth %d), want exactly 1 non-synth", directCount, synthCount)
	}
}

// TestSynthesizeDispatchEdgesReceiverOwner: a method that registers routes
// owns the synthesized edges under its receiver-qualified name.
func TestSynthesizeDispatchEdgesReceiverOwner(t *testing.T) {
	src := `package main

import "net/http"

type Server struct{}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {}

func (s *Server) Routes() {
	s.mux.HandleFunc("/", s.home)
}
`
	_, calls, _, _, err := extract("main.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	edges := calls["Server.Routes"]
	found := false
	for _, e := range edges {
		if e.Target == "home" && e.Synth == "router:net-http" {
			found = true
		}
	}
	if !found {
		t.Errorf("Server.Routes -> home (router:net-http) missing from %v", edges)
	}
}

// TestSynthFieldSurvivesSaveLoad: the Synth provenance must round-trip
// through the JSON index format so downstream renderers can distinguish
// synthesized edges after a reload.
func TestSynthFieldSurvivesSaveLoad(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"main.go": `package main

import "net/http"

func home(w http.ResponseWriter, r *http.Request) {}

func setup() {
	http.HandleFunc("/", home)
}
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	edges := ix.Calls["setup"]
	var synth string
	for _, e := range edges {
		if e.Target == "home" {
			synth = e.Synth
		}
	}
	if synth != "router:net-http" {
		t.Fatalf("pre-save synth = %q, want router:net-http", synth)
	}
	// The saved JSON must carry the field.
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reloaded.Calls["setup"] {
		if e.Target == "home" && e.Synth != "router:net-http" {
			t.Errorf("post-load synth = %q, want router:net-http", e.Synth)
		}
	}
}