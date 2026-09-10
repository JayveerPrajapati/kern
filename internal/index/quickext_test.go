package index

import "testing"

// TestQuickExt pins the index's extension pre-filter: QuickExt accepts any
// source extension in quickExt's case list (case-insensitively) and rejects
// everything else. Note .md/.markdown ARE accepted — quickExt's list is the
// same language set detectLang indexes, which includes Markdown.
func TestQuickExt(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		want bool
	}{
		// Accepted source extensions (from quickExt's case list).
		{name: "go", rel: "cmd/kern/main.go", want: true},
		{name: "python", rel: "scripts/run.py", want: true},
		{name: "javascript", rel: "web/app.js", want: true},
		{name: "typescript", rel: "web/app.ts", want: true},
		{name: "markdown", rel: "README.md", want: true},
		{name: "markdown long", rel: "docs/guide.markdown", want: true},
		{name: "json", rel: "config.json", want: true},
		{name: "yaml", rel: "ci.yml", want: true},
		{name: "rust", rel: "src/lib.rs", want: true},
		{name: "shell", rel: "scripts/deploy.sh", want: true},
		{name: "dart", rel: "app.dart", want: true},
		// Extension matching is case-insensitive.
		{name: "uppercase extension", rel: "MAIN.GO", want: true},
		// Rejected: not in quickExt's case list.
		{name: "no extension", rel: "README", want: false},
		{name: "no extension nested", rel: "path/to/file", want: false},
		{name: "txt", rel: "notes.txt", want: false},
		{name: "log", rel: "out.log", want: false},
		{name: "zip", rel: "archive.zip", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QuickExt(tt.rel); got != tt.want {
				t.Errorf("QuickExt(%q) = %v, want %v", tt.rel, got, tt.want)
			}
		})
	}
}
