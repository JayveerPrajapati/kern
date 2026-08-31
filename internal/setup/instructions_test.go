package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstructionWriters exercises the Continue, Windsurf and Kiro kern-first
// rule writers: each must write its rule file at the expected path with the
// kern guidance, and a re-run must not duplicate the content.
func TestInstructionWriters(t *testing.T) {
	cases := []struct {
		name  string
		write func(root string) Status
		path  string
		want  []string
	}{
		{
			name:  "continue",
			write: wireContinueInstructions,
			path:  filepath.Join(".continue", "rules", "kern.md"),
			want:  []string{"kern_compact_file"},
		},
		{
			name:  "windsurf",
			write: wireWindsurfInstructions,
			path:  filepath.Join(".windsurf", "rules", "kern-first.md"),
			want:  []string{"trigger: always_on", "kern_compact_file"},
		},
		{
			name:  "kiro",
			write: wireKiroInstructions,
			path:  filepath.Join(".kiro", "steering", "kern-first.md"),
			want:  []string{"kern_compact_file"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			st := tc.write(dir)
			if !st.Installed {
				t.Fatalf("writer failed: %s", st.Note)
			}
			p := filepath.Join(dir, tc.path)
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("instruction file not written: %v", err)
			}
			content := string(b)
			for _, want := range tc.want {
				if !strings.Contains(content, want) {
					t.Errorf("instruction missing %q:\n%s", want, content)
				}
			}

			// Idempotent: a re-run rewrites the same embedded content — the
			// file must not grow or duplicate guidance.
			if st := tc.write(dir); !st.Installed {
				t.Fatalf("second run failed: %s", st.Note)
			}
			b2, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(b2) != string(b) {
				t.Fatalf("instruction content changed on re-run:\nbefore:\n%s\nafter:\n%s", b, b2)
			}
			if strings.Count(string(b2), "kern_compact_file") != 1 {
				t.Fatal("instruction content duplicated on re-run")
			}
		})
	}
}
