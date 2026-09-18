package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reviewRiskFixture writes a tiny Go module with a heavily-depended-on
// symbol (Base called by 14 callers) so the shared risk classifier sees a
// wide change (>10 affected) under both kern risk and kern simulate. It
// mirrors the D3 repro shape at a scale that stays fast to index.
func reviewRiskFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var src strings.Builder
	src.WriteString("package main\n\nfunc Base() {}\n\n")
	for i := 1; i <= 14; i++ {
		src.WriteString("func Caller" + string(rune('A'+i)) + "() { Base() }\n\n")
	}
	src.WriteString("func main() { CallerA() }\n")
	files := map[string]string{
		"go.mod":  "module reviewrisk\n\ngo 1.20\n",
		"main.go": src.String(),
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

// TestRiskAndSimulateAgreeOnSameChangeString is the D3 regression at the CLI
// layer: `kern risk` and `kern simulate` invoked with the IDENTICAL change
// string must report the same risk tier. Before the shared classifier, risk
// counted only the root symbols (always 1 for a symbol change ->
// "blast-radius:isolated") while simulate counted the transitively affected
// set (-> high for wide changes).
func TestRiskAndSimulateAgreeOnSameChangeString(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := reviewRiskFixture(t)
	const change = "rename Base to Base2"

	riskOut := captureStdout(t, func() { runRisk([]string{change, "--root", root}) })
	simOut := captureStdout(t, func() { runWhatIf("simulate", []string{change, "--root", root}) })

	riskTier := riskLevelFromOutput(t, riskOut)
	simTier := simulateRiskFromOutput(t, simOut)
	if riskTier != simTier {
		t.Errorf("kern risk tier %q != kern simulate tier %q for the same change %q\nrisk output:\n%s\nsimulate output:\n%s",
			riskTier, simTier, change, riskOut, simOut)
	}
	if riskTier != "high" {
		t.Errorf("kern risk tier = %q, want high for a wide change — the old root-count model reported isolated\nrisk output:\n%s", riskTier, riskOut)
	}
	if !strings.Contains(riskOut, "blast-radius:large") {
		t.Errorf("kern risk output should carry blast-radius:large, got:\n%s", riskOut)
	}
}

// riskLevelFromOutput extracts the risk level line from `kern risk` output
// ("RISK for: <change>\n<LEVEL>\n  factor: ...") and lowercases it.
func riskLevelFromOutput(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		if strings.HasPrefix(ln, "RISK for:") && i+1 < len(lines) {
			return strings.ToLower(strings.TrimSpace(lines[i+1]))
		}
	}
	t.Fatalf("no risk level found in output:\n%s", out)
	return ""
}

// simulateRiskFromOutput extracts the "risk: <tier>" line from `kern
// simulate` output.
func simulateRiskFromOutput(t *testing.T, out string) string {
	t.Helper()
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "risk: ") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "risk: "))
		}
	}
	t.Fatalf("no risk line found in simulate output:\n%s", out)
	return ""
}
