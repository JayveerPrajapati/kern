// Package main is the Blueprint CLI entry point.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/audit"
	"github.com/JayveerPrajapati/kern/internal/blueprint/gates"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
)

// runDoctor implements `blueprint doctor` — a preflight diagnostic that finds
// broken environments BEFORE checks fail mid-flight. Each check produces a
// (status, detail) pair where status ∈ ok|warn|error and a category ∈
// env|config|info.
//
// Flags:
//
//	--repo=PATH   Repository root (default: current directory).
//	--json        Emit a single JSON object instead of human-readable text.
func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "emit JSON instead of human-readable text")
	repoRoot := fs.String("repo", ".", "repository root (default: current directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	absRoot, err := filepath.Abs(*repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: doctor: invalid repository path %q: %v\n", *repoRoot, err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	checks := runDoctorChecks(ctx, absRoot)

	// Exit code: 3 if any config error; else 2 if any env error; else 0.
	//
	// When BOTH config and env errors exist, config wins (exit 3): an invalid
	// Blueprint config is the more specific, user-fixable problem, and the
	// README exit-code contract lists 3 (invalid Blueprint configuration)
	// ahead of 2 (tool/runtime/environment ERROR). This intentionally lets a
	// missing kern binary coexist with a broken config without masking the
	// config error. Warnings and info are never fatal.
	verdict := 0
	hasConfigErr, hasEnvErr := false, false
	for _, c := range checks {
		if c.Status != statusError {
			continue
		}
		switch c.Category {
		case catConfig:
			hasConfigErr = true
		case catEnv:
			hasEnvErr = true
		}
	}
	switch {
	case hasConfigErr:
		verdict = 3
	case hasEnvErr:
		verdict = 2
	}

	if *jsonOut {
		emitDoctorJSON(verdict, checks)
	} else {
		emitDoctorText(verdict, checks)
	}
	return verdict
}

// doctorStatus is one check outcome: ok, warn, error, or info.
type doctorStatus string

const (
	statusOK    doctorStatus = "ok"
	statusWarn  doctorStatus = "warn"
	statusError doctorStatus = "error"
	statusInfo  doctorStatus = "info"
)

// doctorCategory groups checks by what they report on: environment (tooling, git),
// Blueprint config, or informational items.
type doctorCategory string

const (
	catEnv    doctorCategory = "env"
	catConfig doctorCategory = "config"
	catInfo   doctorCategory = "info"
)

// doctorCheck is one preflight result. Name is the stable JSON key;
// Text optionally overrides the human-readable line (defaults to Detail).
type doctorCheck struct {
	Name     string
	Status   doctorStatus
	Category doctorCategory
	Detail   string
	Text     string
}

// kernDependentGates are the phase-gate registry entries whose tests require a
// reachable kern binary (they resolve kern and skip when it is missing). When
// kern is unavailable these gates silently skip in CI; doctor surfaces the gap
// so an environment cannot ship with a shrunken gate set without knowing it.
var kernDependentGates = []string{"G2", "G3", "G5", "G7", "G12", "G13", "G27"}

// runDoctorChecks runs the preflight checks in order:
//
//  1. kern-binary   (env)    — binary resolvable via NewKernClient
//  2. degraded-mode (env)    — WARN when kern is missing: check runs in
//     degraded mode (architecture guard skipped, audit chain local-only)
//     2b. kern-gates    (env)    — which registry gates require kern and whether
//     they can run here (WARN + gate list when kern is missing)
//  3. kern-contract (env)    — `kern sec --json` contract probe
//  4. boundaries    (config) — .kern/boundaries.json shape
//  5. config        (config) — .blueprint/config.yaml loads
//  6. git           (env)    — repository root is a git repo
//  7. hook          (info)   — pre-commit hook presence (never an error)
//  8. audit-chain   (config) — .blueprint/audit/audit.jsonl hash chain intact
func runDoctorChecks(ctx context.Context, repoRoot string) []doctorCheck {
	checks := make([]doctorCheck, 0, 9)

	// 1. kern binary (env).
	client, err := kern.NewKernClient()
	if err != nil {
		checks = append(checks, doctorCheck{
			Name:     "kern-binary",
			Status:   statusError,
			Category: catEnv,
			Detail:   "kern binary not found: " + err.Error() + " (set KERN_BINARY or add kern to PATH)",
		})
		// Informational: `blueprint check` (without --require-kern) degrades
		// gracefully when kern is missing, so tell the user what that means.
		// WARN only — it must not flip the doctor verdict (the kern-binary
		// env error above already owns the exit code).
		checks = append(checks, doctorCheck{
			Name:     "degraded-mode",
			Status:   statusWarn,
			Category: catEnv,
			Detail:   "blueprint check will run in degraded mode: architecture guard skipped, audit chain local-only. gitleaks/jscpd/sandbox still run. Pass --require-kern to enforce kern.",
		})
	} else {
		checks = append(checks, doctorCheck{
			Name:     "kern-binary",
			Status:   statusOK,
			Category: catEnv,
			Detail:   kernBinaryPath(),
		})
	}

	// 2b. kern-dependent gates (env): which registry gates need kern, and can
	// they run here? WARN (never an error — the kern-binary env error above
	// already owns the exit code) so the skip set is visible to the operator.
	gateList := strings.Join(kernDependentGates, ", ")
	if client == nil {
		checks = append(checks, doctorCheck{
			Name:     "kern-gates",
			Status:   statusWarn,
			Category: catEnv,
			Detail:   fmt.Sprintf("%d gates require kern and will skip: %s", len(kernDependentGates), gateList),
		})
	} else {
		checks = append(checks, doctorCheck{
			Name:     "kern-gates",
			Status:   statusOK,
			Category: catEnv,
			Detail:   fmt.Sprintf("kern available: %d kern-dependent gates can run: %s", len(kernDependentGates), gateList),
		})
	}

	// 3. kern contract (env). Probe with `kern sec --json <repoRoot>` from the
	// repo root — cheap, needs no symbol index. The probe's error already
	// names KERN_BINARY / upgrade guidance (contract mismatch, parse failure,
	// or tool failure), so surface it verbatim.
	if client == nil {
		checks = append(checks, doctorCheck{
			Name:     "kern-contract",
			Status:   statusError,
			Category: catEnv,
			Detail:   "cannot probe contract: kern binary unavailable",
		})
	} else {
		checks = append(checks, probeKernContract(ctx, client, repoRoot))
	}

	// 4. boundaries (config).
	checks = append(checks, checkBoundaries(repoRoot))

	// 5. config (config).
	checks = append(checks, checkConfig(repoRoot))

	// 6. git repo (env).
	checks = append(checks, checkGit(repoRoot))

	// 7. pre-commit hook (info) — never an error.
	checks = append(checks, checkHook(repoRoot))

	// 8. audit chain (config) — the P1.4 hash chain must be unbroken so CI
	// receipts remain verifiable. A tampered record breaks every subsequent
	// hash and is a hard config-class error (exit 3).
	checks = append(checks, checkAuditChain(repoRoot))

	return checks
}

// checkAuditChain verifies the P1.4 hash chain in .blueprint/audit/audit.jsonl
// by recomputing every record's hash and walking the PreviousHash links. A
// missing/empty file is intact (0 records); a broken link or a record whose
// stored hash no longer recomputes is an error naming the record.
func checkAuditChain(repoRoot string) doctorCheck {
	w := audit.NewWriter(filepath.Join(repoRoot, ".blueprint", "audit", "audit.jsonl"))
	lastHash, err := w.VerifyChain()
	if err != nil {
		return doctorCheck{
			Name:     "audit-chain",
			Status:   statusError,
			Category: catConfig,
			Detail:   "audit chain BROKEN: " + err.Error(),
		}
	}
	n := w.RecordCount()
	detail := fmt.Sprintf("audit chain intact (%d records", n)
	if lastHash != "" {
		detail += fmt.Sprintf(", last hash %s…", lastHash[:min(8, len(lastHash))])
	}
	detail += ")"
	return doctorCheck{
		Name:     "audit-chain",
		Status:   statusOK,
		Category: catConfig,
		Detail:   detail,
	}
}

// probeKernContract runs `kern sec --json` against the repo root and reports
// the contract version on success, or the exact tool error on failure.
func probeKernContract(parent context.Context, client *kern.KernClient, repoRoot string) doctorCheck {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	_, stdout, _, err := client.SecScan(ctx, repoRoot, repoRoot)
	if err != nil {
		return doctorCheck{
			Name:     "kern-contract",
			Status:   statusError,
			Category: catEnv,
			Detail:   err.Error(),
		}
	}

	// SecScan already enforces schema_version == KernContractVersion; parse
	// it from stdout so the reported version reflects what kern actually
	// emitted.
	version := kern.KernContractVersion // kern contract schema_version; SecScan errors on any other
	var payload struct {
		SchemaVersion int `json:"schema_version"`
	}
	if json.Unmarshal([]byte(stdout), &payload) == nil && payload.SchemaVersion > 0 {
		version = payload.SchemaVersion
	}
	return doctorCheck{
		Name:     "kern-contract",
		Status:   statusOK,
		Category: catEnv,
		Detail:   fmt.Sprintf("schema_version %d", version),
	}
}

// checkBoundaries validates .kern/boundaries.json. A missing file is only a
// warning — the architecture check skips when no boundaries are declared, so
// "everything permitted" is a state to know about, not an error. A malformed
// file (or a rules array that isn't an array of {from,to,action} objects) is
// a hard config error.
func checkBoundaries(repoRoot string) doctorCheck {
	path := filepath.Join(repoRoot, ".kern", "boundaries.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{
				Name:     "boundaries",
				Status:   statusWarn,
				Category: catConfig,
				Detail:   "no .kern/boundaries.json (everything permitted)",
			}
		}
		return doctorCheck{
			Name:     "boundaries",
			Status:   statusError,
			Category: catConfig,
			Detail:   "invalid .kern/boundaries.json: " + err.Error(),
		}
	}

	var payload struct {
		Rules []struct {
			From   string `json:"from"`
			To     string `json:"to"`
			Action string `json:"action"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return doctorCheck{
			Name:     "boundaries",
			Status:   statusError,
			Category: catConfig,
			Detail:   "invalid .kern/boundaries.json: " + err.Error(),
		}
	}
	if payload.Rules == nil {
		return doctorCheck{
			Name:     "boundaries",
			Status:   statusError,
			Category: catConfig,
			Detail:   "invalid .kern/boundaries.json: missing or invalid rules array",
		}
	}
	for i, r := range payload.Rules {
		if r.From == "" || r.To == "" || r.Action == "" {
			return doctorCheck{
				Name:     "boundaries",
				Status:   statusError,
				Category: catConfig,
				Detail:   fmt.Sprintf("invalid .kern/boundaries.json: rules[%d] must have from, to, and action strings", i),
			}
		}
	}
	return doctorCheck{
		Name:     "boundaries",
		Status:   statusOK,
		Category: catConfig,
		Detail:   fmt.Sprintf("%d rules declared", len(payload.Rules)),
	}
}

// checkConfig loads the Blueprint config. A missing file is OK (conservative
// defaults apply); a parse/validation failure is a hard config error.
func checkConfig(repoRoot string) doctorCheck {
	_, err := policy.Load(repoRoot)
	if err != nil {
		return doctorCheck{
			Name:     "config",
			Status:   statusError,
			Category: catConfig,
			Detail:   err.Error(),
		}
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".blueprint", "config.yaml")); os.IsNotExist(err) {
		return doctorCheck{
			Name:     "config",
			Status:   statusOK,
			Category: catConfig,
			Detail:   "using default configuration (no .blueprint/config.yaml)",
		}
	}
	return doctorCheck{
		Name:     "config",
		Status:   statusOK,
		Category: catConfig,
		Detail:   ".blueprint/config.yaml valid",
	}
}

// checkGit reports whether the repo root is inside a git repository.
func checkGit(repoRoot string) doctorCheck {
	if isGitRepo(repoRoot) {
		return doctorCheck{
			Name:     "git",
			Status:   statusOK,
			Category: catEnv,
			Detail:   repoRoot,
		}
	}
	return doctorCheck{
		Name:     "git",
		Status:   statusError,
		Category: catEnv,
		Detail:   "not a git repository",
	}
}

// checkHook reports the pre-commit hook status. Missing or non-blueprint hooks
// are info, never an error: `git commit --no-verify` bypasses the hook
// anyway, so CI is the organizational boundary.
func checkHook(repoRoot string) doctorCheck {
	hookPath := filepath.Join(repoRoot, ".git", "hooks", "pre-commit")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{
				Name:     "hook",
				Status:   statusInfo,
				Category: catInfo,
				Detail:   "pre-commit hook not installed (git commit --no-verify bypasses it anyway; CI is the organizational boundary)",
				Text:     "pre-commit hook not installed",
			}
		}
		return doctorCheck{
			Name:     "hook",
			Status:   statusInfo,
			Category: catInfo,
			Detail:   fmt.Sprintf("pre-commit hook unreadable: %v", err),
		}
	}
	if strings.Contains(string(data), "blueprint") {
		return doctorCheck{
			Name:     "hook",
			Status:   statusOK,
			Category: catInfo,
			Detail:   "pre-commit hook installed",
		}
	}
	return doctorCheck{
		Name:     "hook",
		Status:   statusInfo,
		Category: catInfo,
		Detail:   "pre-commit hook present but does not reference blueprint",
	}
}

// kernBinaryPath reports the resolved kern binary for display, mirroring
// NewKernClient's resolution order (KERN_BINARY, PATH, ../kern/bin/kern).
// It is best-effort: runDoctor only calls it after NewKernClient succeeded.
func kernBinaryPath() string {
	if p := os.Getenv("KERN_BINARY"); p != "" {
		return p
	}
	if p, err := exec.LookPath("kern"); err == nil {
		return p
	}
	if cwd, err := os.Getwd(); err == nil {
		for _, candidate := range []string{
			filepath.Join(cwd, "bin", "kern"),
			filepath.Join(cwd, "..", "kern", "bin", "kern"),
		} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return "kern"
}

// emitDoctorText prints one line per check plus a verdict line.
func emitDoctorText(verdict int, checks []doctorCheck) {
	for _, c := range checks {
		text := c.Text
		if text == "" {
			text = c.Detail
		}
		fmt.Printf("%s%s: %s\n", renderDoctorStatus(c.Status), strings.ReplaceAll(c.Name, "-", " "), text)
	}

	var warnings, errors int
	for _, c := range checks {
		switch c.Status {
		case statusWarn:
			warnings++
		case statusError:
			errors++
		}
	}
	suffix := "healthy"
	switch {
	case errors > 0:
		suffix = fmt.Sprintf("unhealthy — %d %s", errors, plural(errors, "error"))
		if warnings > 0 {
			suffix += fmt.Sprintf(", %d %s", warnings, plural(warnings, "warning"))
		}
	case warnings > 0:
		suffix = fmt.Sprintf("healthy — %d %s", warnings, plural(warnings, "warning"))
	}
	fmt.Printf("verdict: %d (%s)\n", verdict, suffix)

	// Phase-gate registry summary (the authoritative inventory lives in
	// internal/blueprint/gates; --json lists every gate with its test file).
	fmt.Printf("Gates: %d registered (%s–%s). Run --json for details.\n",
		len(gates.Registry), gates.Registry[0].ID, gates.Registry[len(gates.Registry)-1].ID)
}

// plural returns noun unchanged for 1, noun+"s" otherwise.
func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// renderDoctorStatus renders a status as the fixed-width `[ok]   ` column.
func renderDoctorStatus(s doctorStatus) string {
	pad := 5 - len(s)
	if pad < 1 {
		pad = 1
	}
	return "[" + string(s) + "]" + strings.Repeat(" ", pad)
}

// doctorJSON is the --json output shape with stable check names.
type doctorJSON struct {
	Verdict   int               `json:"verdict"`
	Checks    []doctorJSONCheck `json:"checks"`
	Gates     []doctorJSONGate  `json:"gates"`
	GateCount int               `json:"gate_count"`
}

type doctorJSONCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Category string `json:"category"`
	Detail   string `json:"detail"`
}

// doctorJSONGate is one phase-gate registry entry in the doctor JSON output.
type doctorJSONGate struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Verifies  string `json:"verifies"`
	TestFile  string `json:"test_file"`
	Package   string `json:"package"`
	TestCount int    `json:"test_count"`
}

// emitDoctorJSON prints a single JSON object.
func emitDoctorJSON(verdict int, checks []doctorCheck) {
	out := doctorJSON{Verdict: verdict, Checks: make([]doctorJSONCheck, 0, len(checks))}
	for _, c := range checks {
		out.Checks = append(out.Checks, doctorJSONCheck{
			Name:     c.Name,
			Status:   string(c.Status),
			Category: string(c.Category),
			Detail:   c.Detail,
		})
	}
	// Phase-gate registry: compiled in, so no file I/O and always in sync
	// with the binary. Sorted by construction (Registry is G0..G28 in order).
	out.Gates = make([]doctorJSONGate, 0, len(gates.Registry))
	for _, g := range gates.Registry {
		out.Gates = append(out.Gates, doctorJSONGate{
			ID:        g.ID,
			Name:      g.Name,
			Verifies:  g.Verifies,
			TestFile:  g.TestFile,
			Package:   g.Package,
			TestCount: len(g.TestFuncs),
		})
	}
	out.GateCount = len(gates.Registry)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}
