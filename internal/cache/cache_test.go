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
	// Dir() is resolved once at first use (memoized) and TestDirHonoursXDG
	// already warmed it, so the fallback resolution is exercised through the
	// extracted resolver directly.
	t.Setenv("XDG_CACHE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := resolveCacheDir()
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

func TestRemoveDeletesEntryAndTwin(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := Store("rm/entry", map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	if !Exists("rm/entry") {
		t.Fatal("entry must exist after store")
	}
	if err := Remove("rm/entry"); err != nil {
		t.Fatal(err)
	}
	if Exists("rm/entry") {
		t.Fatal("entry must not exist after Remove")
	}
	if err := Remove("rm/entry"); err != nil {
		t.Fatalf("removing an absent key must be a no-op, got %v", err)
	}
	// A dormant .gz twin is removed too.
	if err := Store("rm/twin", map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path("data", "rm/twin.json.gz"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Remove("rm/twin"); err != nil {
		t.Fatal(err)
	}
	if Exists("rm/twin") {
		t.Fatal("gzip twin must not survive Remove")
	}
}
