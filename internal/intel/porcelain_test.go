package intel

import "testing"

func TestParsePorcelainRenameAndQuotes(t *testing.T) {
	// NUL-separated: rename with spaces (no quoting), plus untracked + modified.
	out := "R  new file.txt\x00old file.txt\x00M  plain.go\x00?? weird \"name\".txt\x00"
	got := parsePorcelain(out)
	want := []string{"new file.txt", "plain.go", "weird \"name\".txt"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
