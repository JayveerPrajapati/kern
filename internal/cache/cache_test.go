package cache

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHashStableAndUnique(t *testing.T) {
	a := Hash([]byte("hello"))
	b := Hash([]byte("hello"))
	if a != b {
		t.Fatalf("hash must be deterministic: %s != %s", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(a))
	}
	if a == Hash([]byte("hello!")) {
		t.Fatal("different inputs must differ")
	}
}

func TestDirHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-test")
	if got := Dir(); got != filepath.Join("/tmp/xdg-test", "kern") {
		t.Fatalf("expected XDG root, got %s", got)
	}
}

func TestDirFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := Dir()
	want := filepath.Join(home, ".cache", "kern")
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestEnsure(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(Dir()); err != nil || !st.IsDir() {
		t.Fatalf("cache root not created: %v %v", st, err)
	}
}

func TestStoreLoadRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	type payload struct {
		Name  string
		Count int
	}
	in := payload{Name: "widget", Count: 7}
	if err := Store("test/roundtrip", in); err != nil {
		t.Fatal(err)
	}

	var out payload
	if err := Load("test/roundtrip", &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch: %+v != %+v", out, in)
	}
}

func TestLoadMissingReturnsNotExist(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var v any
	err := Load("test/nope", &v)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestExists(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if Exists("test/exists") {
		t.Fatal("must not exist before store")
	}
	if err := Store("test/exists", map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	if !Exists("test/exists") {
		t.Fatal("must exist after store")
	}
	if Exists("test/never") {
		t.Fatal("never-stored key must not exist")
	}
}

func TestStorePersistsJSON(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := Store("json/check", struct{ K string }{K: "v"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path("data", "json/check.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"K":"v"}` {
		t.Fatalf("unexpected serialized form: %s", raw)
	}
}
