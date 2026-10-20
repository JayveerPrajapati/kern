package doctor

import (
	"strings"
	"testing"
)

// TestCheckLLM pins the doctor rendering of the LLM wiring report: the
// classification lives in internal/llm.Status; doctor maps it to a Finding
// under the "llm" check. With Ollama unreachable and no agent CLIs on PATH
// the state is a warn naming the missing provider; with the stage pinned
// off it is ok deterministic mode.
func TestCheckLLM(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	t.Setenv("PATH", t.TempDir())
	f := checkLLM()
	if f.Check != "llm" || f.Level != "warn" || !strings.Contains(f.Detail, "no agent CLI installed") {
		t.Errorf("no-provider state: got %+v", f)
	}

	t.Setenv("KERN_LLM_PROVIDER", "none")
	f = checkLLM()
	if f.Level != "ok" || !strings.Contains(f.Detail, "disabled") {
		t.Errorf("disabled state: got %+v", f)
	}
}
