package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestIsTestRejectsDocSymbols pins A2/F3 (deep-dive 2026-10-03): markdown
// headings named "Test with curl" / "Testing Commands" matched the Test-prefix
// alone and were returned as test nodes by WhatTestsCover, polluting plan
// validation steps with prose. Doc symbols (heading kind or doc-file
// extensions) never qualify as tests.
func TestIsTestRejectsDocSymbols(t *testing.T) {
	reject := []domain.Symbol{
		{Name: "Test with curl", Kind: "heading", File: "README.md"},
		{Name: "Testing Commands", Kind: "heading", File: "docs/guide.md"},
		{Name: "Test with curl", Kind: "func", File: "notes.markdown"},
		{Name: "Test flow", Kind: "func", File: "runbook.html"},
	}
	for _, s := range reject {
		if isTest(&s) {
			t.Fatalf("isTest(%q in %s, kind %s) = true, want false (doc symbol)", s.Name, s.File, s.Kind)
		}
	}
	accept := []domain.Symbol{
		{Name: "TestPublic", Kind: "func", File: "lib_test.go"},
		{Name: "TestWhatever", Kind: "func", File: "lib.go"}, // Go Test-convention prefix in a code file
		{Name: "helper", Kind: "func", File: "pkg/util_test.go"},
	}
	for _, s := range accept {
		if !isTest(&s) {
			t.Fatalf("isTest(%q in %s, kind %s) = false, want true", s.Name, s.File, s.Kind)
		}
	}
}

// TestIsTestLanguageAware pins F7 (campaign 2026-10-04): test-symbol
// recognition must follow each language's own convention, not just Go's —
// `kern impact` on Python repos listed pytest functions as direct callers
// yet reported "Tests that cover it: 0" because isTest was Go-shaped.
func TestIsTestLanguageAware(t *testing.T) {
	accept := []domain.Symbol{
		// Python: test_*.py / *_test.py file, or test_-prefixed name.
		{Name: "test_run_live", Kind: "func", File: "log_checker/test_log_checker.py"},
		{Name: "helper", Kind: "func", File: "log_checker/test_log_checker.py"},
		{Name: "test_config", Kind: "func", File: "log_checker/config_test.py"},
		{Name: "test_config", Kind: "func", File: "log_checker/config.py"},
		// TS/JS: *.test.* / *.spec.* files.
		{Name: "helper", Kind: "func", File: "src/widget.test.ts"},
		{Name: "helper", Kind: "func", File: "src/widget.test.tsx"},
		{Name: "helper", Kind: "func", File: "src/widget.spec.ts"},
		{Name: "itWorks", Kind: "func", File: "src/widget.spec.js"},
		{Name: "helper", Kind: "func", File: "src/widget.test.mjs"},
		// Go convention unchanged.
		{Name: "TestWhatever", Kind: "func", File: "lib.go"},
		{Name: "helper", Kind: "func", File: "pkg/util_test.go"},
	}
	reject := []domain.Symbol{
		{Name: "run_live", Kind: "func", File: "log_checker/log_checker.py"},
		{Name: "TestSomething", Kind: "func", File: "log_checker/log_checker.py"}, // Go-style name is not a pytest convention
		{Name: "helper", Kind: "func", File: "src/widget.ts"},
		{Name: "helper", Kind: "func", File: "src/widget.js"},
		{Name: "helper", Kind: "func", File: "src/widget.py"},
	}
	for _, s := range accept {
		if !isTest(&s) {
			t.Fatalf("isTest(%q in %s, kind %s) = false, want true", s.Name, s.File, s.Kind)
		}
	}
	for _, s := range reject {
		if isTest(&s) {
			t.Fatalf("isTest(%q in %s, kind %s) = true, want false", s.Name, s.File, s.Kind)
		}
	}
}
