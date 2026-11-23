package verifycmd

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// BenchmarkVerifyCommandFreshVsReused measures the T-02 target: repeat
// verification of an identical command. The fresh path re-executes the
// command every iteration; the reused path serves the kept run after the
// pre-warm (repo state unchanged). The reuse win grows with the real
// command's runtime — this benchmark uses a fast command to measure the
// mechanism's own overhead honestly.
func BenchmarkVerifyCommandFreshVsReused(b *testing.B) {
	ctx := context.Background()
	cmd := "git log --oneline"

	b.Run("fresh", func(b *testing.B) {
		dir := newReuseRepo(b)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Distinct command per iteration so no reuse can serve it —
			// production config (reuse enabled) with a real execution,
			// fingerprint and manifest save every time.
			cmd := fmt.Sprintf("git log --oneline -%d", i+1)
			if _, exit, err := VerifyCommand(ctx, map[string]any{"root": dir, "command": cmd}); err != nil || exit != 0 {
				b.Fatalf("exit=%d err=%v", exit, err)
			}
		}
	})

	b.Run("reused", func(b *testing.B) {
		dir := newReuseRepo(b)
		if _, exit, err := VerifyCommand(ctx, map[string]any{"root": dir, "command": cmd}); err != nil || exit != 0 {
			b.Fatalf("pre-warm: exit=%d err=%v", exit, err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			out, exit, err := VerifyCommand(ctx, map[string]any{"root": dir, "command": cmd})
			if err != nil || exit != 0 {
				b.Fatalf("exit=%d err=%v", exit, err)
			}
			if !strings.Contains(out, "[reused]") {
				b.Fatalf("iteration %d was not served from reuse:\n%s", i, out)
			}
		}
	})
}
