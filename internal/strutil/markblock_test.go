package strutil

import "testing"

func TestRemoveMarkedBlock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		in    string
		open  string
		close string
		want  string
	}{
		{
			name:  "LF mid-file block removed with its terminator",
			in:    "before\n# BEGIN\nblock\n# END\nafter\n",
			open:  "# BEGIN",
			close: "# END",
			want:  "before\nafter\n",
		},
		{
			name:  "CRLF terminator dropped whole",
			in:    "before\r\n# BEGIN\r\nblock\r\n# END\r\nafter\r\n",
			open:  "# BEGIN",
			close: "# END",
			want:  "before\r\nafter\r\n",
		},
		{
			name:  "missing open marker returns input unchanged",
			in:    "nothing here\n# END\n",
			open:  "# BEGIN",
			close: "# END",
			want:  "nothing here\n# END\n",
		},
		{
			name:  "missing close marker returns input unchanged",
			in:    "# BEGIN\nunterminated\n",
			open:  "# BEGIN",
			close: "# END",
			want:  "# BEGIN\nunterminated\n",
		},
		{
			name:  "block at EOF with trailing newline drops it",
			in:    "keep\n# BEGIN\nblock\n# END\n",
			open:  "# BEGIN",
			close: "# END",
			want:  "keep\n",
		},
		{
			name:  "block at EOF without trailing newline",
			in:    "keep\n# BEGIN\nblock\n# END",
			open:  "# BEGIN",
			close: "# END",
			want:  "keep\n",
		},
		{
			name:  "only the block leaves empty string",
			in:    "# BEGIN\nblock\n# END\n",
			open:  "# BEGIN",
			close: "# END",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := RemoveMarkedBlock(tt.in, tt.open, tt.close); got != tt.want {
				t.Fatalf("RemoveMarkedBlock(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRemoveMarkedBlockIdempotentCycles(t *testing.T) {
	t.Parallel()
	// The real contract: remove followed by re-insert must be byte-stable
	// across cycles (this is what setup↔check rely on for .gitignore).
	in := "user content\n# BEGIN\nmanaged\n# END\n"
	for i := 0; i < 5; i++ {
		stripped := RemoveMarkedBlock(in, "# BEGIN", "# END")
		rebuilt := stripped + "# BEGIN\nmanaged\n# END\n"
		if rebuilt != in {
			t.Fatalf("cycle %d not stable:\nstripped=%q\nrebuilt=%q\nin=%q", i, stripped, rebuilt, in)
		}
	}
}
