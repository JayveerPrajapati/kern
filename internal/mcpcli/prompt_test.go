package mcpcli

import "testing"

// TestRunPromptFillNoTemplateExits2 pins the usage contract: a prompt-fill
// without --template (and no positional template) is a usage error (exit 2).
func TestRunPromptFillNoTemplateExits2(t *testing.T) {
	if code := RunPromptFill(nil); code != 2 {
		t.Fatalf("RunPromptFill(nil) = %d, want 2 (usage error)", code)
	}
}
