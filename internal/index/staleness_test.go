package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStalenessBannerFresh: a built index must report no staleness for the
// files it cited, as long as none of them changed.
func TestStalenessBannerFresh(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a.go": "package a\nfunc A() {}\n",
		"b.go": "package a\nfunc B() {}\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b := ix.StalenessBanner([]string{"a.go", "b.go"}); b != "" {
		t.Errorf("fresh index reported staleness: %q", b)
	}
}

// TestStalenessBannerModifiedFile: editing a cited file after the build must
// produce the one-line warning banner with the exact changed count.
func TestStalenessBannerModifiedFile(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a.go": "package a\nfunc A() {}\n",
		"b.go": "package a\nfunc B() {}\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("package a\nfunc A() { println(\"changed\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ix.StalenessBanner([]string{"a.go", "b.go"})
	if want := "⚠️ 1 file(s) changed since index — kern health"; got != want {
		t.Errorf("banner = %q, want %q", got, want)
	}
}

// TestStalenessBannerDeletedFile: a cited file that vanished since indexing
// is the strongest staleness signal — it must be reported, not skipped.
func TestStalenessBannerDeletedFile(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.go": "package a\nfunc A() {}\n"})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "a.go")); err != nil {
		t.Fatal(err)
	}
	if b := ix.StalenessBanner([]string{"a.go"}); b == "" {
		t.Error("deleted cited file must be reported stale")
	}
}

// TestStalenessBannerUnindexedFile: a file the index never recorded (never
// existed at build time, or non-indexable) cannot be stale relative to it —
// the index made no claim about that file.
func TestStalenessBannerUnindexedFile(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.go": "package a\nfunc A() {}\n"})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b := ix.StalenessBanner([]string{"never-existed.go"}); b != "" {
		t.Errorf("unrecorded file reported stale: %q", b)
	}
}

// TestStalenessBannerAbsolutePaths: absolute paths under the root must be
// normalized to the relative keys FileHashes uses.
func TestStalenessBannerAbsolutePaths(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.go": "package a\nfunc A() {}\n"})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("package a\nfunc A() { println(\"x\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := ix.StalenessBanner([]string{p}); !strings.Contains(b, "1 file(s) changed") {
		t.Errorf("absolute path not matched: %q", b)
	}
}

// TestStalenessBannerOutsideRoot: paths outside the root are ignored — the
// index has no opinion about files it does not cover.
func TestStalenessBannerOutsideRoot(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.go": "package a\nfunc A() {}\n"})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	other := filepath.Join(outside, "a.go")
	if err := os.WriteFile(other, []byte("package a\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := ix.StalenessBanner([]string{other}); b != "" {
		t.Errorf("outside-root path reported stale: %q", b)
	}
}

// TestStalenessBannerNilIndex: a nil index or an index with no hashes must
// never panic and must never report staleness.
func TestStalenessBannerNilIndex(t *testing.T) {
	if b := (*Index)(nil).StalenessBanner([]string{"a.go"}); b != "" {
		t.Errorf("nil index reported staleness: %q", b)
	}
	if b := (&Index{}).StalenessBanner([]string{"a.go"}); b != "" {
		t.Errorf("hashless index reported staleness: %q", b)
	}
}

// TestStalenessBannerDedupes: the same file cited twice counts once.
func TestStalenessBannerDedupes(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.go": "package a\nfunc A() {}\n"})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc A() { println(\"y\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ix.StalenessBanner([]string{"a.go", "a.go", "a.go"})
	if want := "⚠️ 1 file(s) changed since index — kern health"; got != want {
		t.Errorf("banner = %q, want %q", got, want)
	}
}

// TestRelPath: path normalization across the relative/absolute/outside forms.
func TestRelPath(t *testing.T) {
	dir := t.TempDir()
	ix := &Index{Root: dir}
	cases := []struct {
		in, want string
	}{
		{"a.go", "a.go"},
		{"./a.go", "a.go"},
		{"sub/a.go", "sub/a.go"},
		{filepath.Join(dir, "a.go"), "a.go"},
		{filepath.Join(dir, "sub", "a.go"), "sub/a.go"},
		{filepath.Join(filepath.Dir(dir), "other.go"), ""}, // outside root
		{"../other.go", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := ix.relPath(c.in); got != c.want {
			t.Errorf("relPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}