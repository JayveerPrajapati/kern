package runtime

import (
	"fmt"
	"path/filepath"
	"time"
)

// HighErrorRate is the error-rate threshold (percent) above which a changed
// file's runtime overlay is flagged.
const HighErrorRate = 5.0

// Overlay returns a per-file review-overlay renderer bound to src: for each
// changed file it renders the package's service profile line (directory base
// name matched against service names), flagged when the error rate is high,
// or stating explicitly that no telemetry exists for the package. A nil
// source, or one without events, yields nil — the caller then renders no
// overlay. The overlay is a pure function of the source's current snapshot,
// so it is safe to reuse across many files.
func Overlay(src Source) func(file string) string {
	profiles := ServiceProfiles(src)
	if profiles == nil {
		return nil
	}
	return func(file string) string {
		base := filepath.Base(filepath.Dir(file))
		p := ProfileFor(profiles, base)
		if p == nil {
			return fmt.Sprintf("runtime: no telemetry for package dir %q\n", base)
		}
		line := fmt.Sprintf("runtime: svc %q · %d events · %.1f%% errors", p.Name, p.Events, p.ErrorRate)
		if !p.Last.IsZero() {
			// A future timestamp (clock skew, fixture) renders as "last in X"
			// rather than a negative "ago".
			if ago := time.Since(p.Last).Round(time.Second); ago < 0 {
				line += fmt.Sprintf(" · last in %s", -ago)
			} else {
				line += fmt.Sprintf(" · last %s ago", ago)
			}
		}
		if p.ErrorRate >= HighErrorRate {
			line += fmt.Sprintf(" · FLAG: error rate above %.0f%%", HighErrorRate)
		}
		return line + "\n"
	}
}
