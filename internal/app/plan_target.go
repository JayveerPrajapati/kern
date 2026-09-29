// Plan-target grounding: deterministic resolution of a concrete target path
// named in a plan intent (e.g. "Add a Greet function to
// internal/strutil/strutil.go") so `kern plan` grounds its steps in real
// files and scopes its risk to the named target instead of the tree-global
// packet risk — which for net-new feature requests can describe an unrelated
// symbol's blast radius (the intent fuzzy-resolved to some other symbol).

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// planTargetPathRe extracts a concrete target path from a plan intent: a
// package-rooted path (internal/, pkg/, cmd/, ...) such as
// "internal/strutil/strutil.go" or "pkg/http/middleware". Matches are
// verified against the filesystem by resolvePlanTarget, so prose that merely
// looks like a path never grounds a plan.
var planTargetPathRe = regexp.MustCompile(`(?i)\b((?:internal|pkg|cmd|lib|src|api|app|tools)/[a-z0-9_./-]+)`)

// planSymbolNameRe matches CamelCase identifiers that could name a new symbol
// in a net-new feature intent ("Add a Greet function ..." → "Greet").
var planSymbolNameRe = regexp.MustCompile(`\b[A-Z][A-Za-z0-9]*\b`)

// resolvePlanTarget resolves a concrete target path named in the intent
// against root. It returns the root-relative package directory, the named
// file's base name ("" when the intent names only a directory), and whether
// the target directory exists on disk. A target that cannot be resolved
// returns ok=false so callers keep the generic plan template (honest
// fallback — never guess).
func resolvePlanTarget(change, root string) (dir, file string, ok bool) {
	if root == "" {
		root = "."
	}
	m := planTargetPathRe.FindStringSubmatch(change)
	if m == nil {
		return "", "", false
	}
	p := strings.TrimRight(m[1], ".,;:()[]{}'\"` \t")
	if i := strings.LastIndexByte(p, ':'); i >= 0 {
		p = p[:i] // strip any :line suffix
	}
	p = strings.Trim(p, "/")
	if p == "" {
		return "", "", false
	}
	if filepath.Ext(p) != "" {
		file = filepath.Base(p)
		dir = filepath.Dir(p)
	} else {
		dir = p
	}
	if dir == "" || dir == "." {
		return "", "", false
	}
	st, err := os.Stat(filepath.Join(root, dir))
	if err != nil || !st.IsDir() {
		// The named target does not resolve on disk: step grounding must fall
		// back to the generic template, but the extracted dir is still the
		// intent's named target for risk scoping.
		return dir, file, false
	}
	return dir, file, true
}

// riskScopedToTarget returns the plan risk level scoped to a concrete target
// directory named in the intent. The packet's risk rows are computed from
// whatever symbol the intent resolved to — for net-new feature requests that
// can be an unrelated symbol with a large blast radius (the packet risk is
// tree-global, not target-scoped). When the intent names a concrete target
// and the packet's own scope does not include it, the rows are not evidence
// about the target: calibrate to low unless the target path itself is
// security-sensitive (a genuine high-risk change). When the packet already
// covers the target, its risk rows are authoritative and kept as-is —
// scope, don't guess.
func riskScopedToTarget(pkt domain.ContextPacket, targetDir string) string {
	packetRisk := riskLevelString(pkt.Risks)
	if targetDir == "" {
		return packetRisk
	}
	if packetTouchesDir(pkt, targetDir) {
		return packetRisk
	}
	if isSecuritySensitivePath(targetDir) {
		return "high"
	}
	return "low"
}

// packetTouchesDir reports whether the packet's own scope (files or symbols)
// includes the target directory — when it does, the packet's risk rows are
// genuinely about the target.
func packetTouchesDir(pkt domain.ContextPacket, dir string) bool {
	d := strings.Trim(dir, "/")
	for _, f := range pkt.Files {
		if pathUnder(f.Path, d) {
			return true
		}
	}
	for _, s := range pkt.Symbols {
		if pathUnder(s.File, d) {
			return true
		}
	}
	return false
}

// pathUnder reports whether p equals dir or lives under it.
func pathUnder(p, dir string) bool {
	p = strings.Trim(p, "/")
	if p == "" || dir == "" {
		return false
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// isSecuritySensitivePath mirrors the engine's security-file classification
// (context.Engine.isSecurityFile) for a target path: a target under a
// security-sensitive directory (auth, credentials, secrets, TLS, ...) is a
// genuine high-risk change even when the packet's risk rows describe an
// unrelated symbol.
func isSecuritySensitivePath(path string) bool {
	lower := strings.ToLower(path)
	for _, k := range []string{
		"auth", "credential", "secret", "token", "password", "security",
		"tls", "pem", "crt", "key", "oauth", "session", "vault",
	} {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// planSymbolName extracts a candidate new-symbol name from a net-new intent
// (e.g. "Add a Greet function to internal/strutil/strutil.go" → "Greet").
// Conservative: the first CamelCase identifier that is not a change verb, not
// the named file's stem, and not common prose. Returns "" when the intent
// names no plausible symbol, so callers fall back to file-only grounding.
func planSymbolName(change, fileBase string) string {
	stem := strings.TrimSuffix(fileBase, filepath.Ext(fileBase))
	for _, m := range planSymbolNameRe.FindAllString(change, -1) {
		if whatif.IsChangeVerb(strings.ToLower(m)) {
			continue
		}
		if stem != "" && strings.EqualFold(m, stem) {
			continue
		}
		switch strings.ToLower(m) {
		case "the", "this", "that", "what", "how", "when", "where", "why",
			"make", "use", "get", "set", "run", "fix", "add", "new":
			continue
		}
		return m
	}
	return ""
}

// netNewGroundedSteps builds implementation steps for a net-new feature that
// names a resolvable target: real file names in the target package. dir is
// the resolved root-relative package directory, file the named file's base
// name ("" when the intent names only a directory), and sym the extracted
// symbol name ("" when none). Callers append their own test/changelog steps.
func netNewGroundedSteps(change, dir, file string) []string {
	sym := planSymbolName(change, file)
	switch {
	case file != "" && sym != "":
		return []string{fmt.Sprintf("Add %s to %s.", sym, filepath.Join(dir, file))}
	case file != "":
		return []string{fmt.Sprintf("Implement the requested functionality in %s.", filepath.Join(dir, file))}
	case sym != "":
		return []string{fmt.Sprintf("Create %s/%s.go implementing %s.", dir, strings.ToLower(sym), sym)}
	default:
		return []string{fmt.Sprintf("Implement the feature in a new file under %s/.", dir)}
	}
}

// statelessTestStep returns the "add unit tests" step for a stateless plan,
// grounded in the resolved target when available (real test-file name); the
// generic phrase is used otherwise so unchanged plans keep their exact text.
func statelessTestStep(dir, file string) string {
	if file != "" {
		stem := strings.TrimSuffix(file, filepath.Ext(file))
		return fmt.Sprintf("Add unit tests in %s.", filepath.Join(dir, stem+"_test.go"))
	}
	if dir != "" {
		return fmt.Sprintf("Add unit tests alongside the new code in %s/.", dir)
	}
	return "Add unit tests alongside the new code."
}
