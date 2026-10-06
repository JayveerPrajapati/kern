// Verify-type vocabulary and gates. Verify-types knowledge is tool-param
// knowledge (kern_verify's `types` argument), so it lives with the tool
// definitions in catalog; the meta router and highlevel handlers consume it
// from here.
package catalog

import (
	"fmt"
	"strings"
)

// VerifyTypesExec reports whether any requested verification type executes
// host commands, mirroring the engine's Verify dispatch (substring match).
// The engine runs build, test (unit/integration), e2e, static-analysis
// (vet/lint) and performance (bench) through validate.Run / sandbox.Run —
// arbitrary host code — and the CI check through its adapter (gh et al).
// cve shells out to govulncheck and secrets/reuse to `git` (`git log` /
// `git status`), so both are host command execution too. Architecture,
// security, dependency and license are in-process (index scans, sec rules,
// manifest parsing) and never shell out, so a request limited to those types
// must NOT require the exec allowlist.
func VerifyTypesExec(types []string) bool {
	if len(types) == 0 {
		return true // engine default runs build+test: exec
	}
	for _, t := range types {
		t = strings.ToLower(strings.TrimSpace(t))
		switch {
		case strings.Contains(t, "cve"):
			return true
		case strings.Contains(t, "secret"):
			return true
		case strings.Contains(t, "reuse"):
			return true // git status --porcelain
		case strings.Contains(t, "build"):
			return true
		case strings.Contains(t, "test"), strings.Contains(t, "unit"), strings.Contains(t, "integration"):
			return true
		case strings.Contains(t, "e2e"), strings.Contains(t, "end-to-end"):
			return true
		case strings.Contains(t, "static"), strings.Contains(t, "analysis"), strings.Contains(t, "vet"), strings.Contains(t, "lint"):
			return true
		case strings.Contains(t, "perf"), strings.Contains(t, "bench"):
			return true
		case strings.Contains(t, "ci"):
			return true
		}
	}
	return false
}

// VerifyTypesKnown are the canonical verification types the unified engine
// accepts (verification.Engine.Verify, substring dispatch). A request token is
// valid only when it matches one of them; anything else (e.g. a number
// coerced to "123") is rejected up front so a garbage types list can never
// degrade into a vacuous "summary: PASS" run where every sub-check is
// silently skipped.
var VerifyTypesKnown = []string{"build", "test", "security", "architecture", "dependency", "reuse", "e2e", "static-analysis", "performance", "cve", "license", "secrets", "ci", "arch"}

// KnownVerifyType reports whether a token names a verification the engine can
// run. It mirrors verification.Engine.Verify's substring dispatch exactly, so
// valid aliases the engine accepts (unit/integration for test, vet/lint for
// static-analysis, sec for security, dep for dependency, bench for
// performance) stay accepted and only unrecognized garbage is rejected.
func KnownVerifyType(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case strings.Contains(t, "cve"):
		return true
	case strings.Contains(t, "licen"):
		return true
	case strings.Contains(t, "secret"):
		return true
	case strings.Contains(t, "build"):
		return true
	case strings.Contains(t, "test"), strings.Contains(t, "unit"), strings.Contains(t, "integration"):
		return true
	case strings.Contains(t, "security"), strings.Contains(t, "sec"):
		return true
	case strings.Contains(t, "arch"):
		return true
	case strings.Contains(t, "depend"), strings.Contains(t, "dep"):
		return true
	case strings.Contains(t, "reuse"):
		return true
	case strings.Contains(t, "e2e"), strings.Contains(t, "end-to-end"):
		return true
	case strings.Contains(t, "static"), strings.Contains(t, "analysis"), strings.Contains(t, "vet"), strings.Contains(t, "lint"):
		return true
	case strings.Contains(t, "perf"), strings.Contains(t, "bench"):
		return true
	case strings.Contains(t, "ci"):
		return true
	}
	return false
}

// ValidateVerifyTypes rejects any requested verification type the engine
// cannot run. It must run BEFORE the exec firewall and before any check, so a
// garbage types list (types=123 coerced to "123") errors out instead of
// producing a vacuous PASS.
func ValidateVerifyTypes(types []string) error {
	for _, t := range types {
		if !KnownVerifyType(t) {
			return fmt.Errorf("unknown verify type: %s (known: %s)", t, strings.Join(VerifyTypesKnown, ", "))
		}
	}
	return nil
}
