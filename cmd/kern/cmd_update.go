package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	kversion "github.com/JayveerPrajapati/kern/internal/version"
)

// installScriptURL is the canonical install.sh location `kern update`
// delegates to (the same script the README's curl|sh one-liner runs).
// installScriptURLFor resolves it; KERN_INSTALL_SCRIPT_URL overrides it for
// mirrors and offline e2e fixtures — the same override pattern KERN_BASE_URL
// uses for release downloads in install.sh.
const installScriptURL = "https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh"

func installScriptURLFor() string {
	if u := os.Getenv("KERN_INSTALL_SCRIPT_URL"); u != "" {
		return u
	}
	return installScriptURL
}

// updateChildEnv returns the policy env forwarded to the installer child on
// top of the inherited environment: KERN_FORCE=1 on --force,
// KERN_VERSION=<pin>+KERN_PIN=1 on --pin, and KERN_CHANNEL=<channel> on
// --channel (Stage C: stable | latest | regex). --pin overrides the channel
// entirely — an explicit pinned tag short-circuits install.sh's get_version
// before any channel logic. A pre-existing inherited KERN_CHANNEL is
// dropped before the forwarded value is appended: on Unix a duplicated
// variable in the execve array resolves to the FIRST occurrence (libc
// getenv), so appending behind an inherited value would silently shadow the
// flag (the e2e fixture's envFor documents the same hazard). Without
// --channel the inherited environment passes through untouched.
func updateChildEnv(f flags) []string {
	env := os.Environ()
	if f.force {
		env = append(env, "KERN_FORCE=1")
	}
	if f.pin != "" {
		env = append(env, "KERN_VERSION="+f.pin, "KERN_PIN=1")
	} else if f.channel != "" {
		env = dropEnvKey(env, "KERN_CHANNEL")
		env = append(env, "KERN_CHANNEL="+f.channel)
	}
	return env
}

// dropEnvKey returns env without the first entry whose key equals key (the
// part before '='). See updateChildEnv for why a forwarded value must not
// be appended behind an inherited one of the same name.
func dropEnvKey(env []string, key string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			out := make([]string, 0, len(env)-1)
			out = append(out, env[:i]...)
			return append(out, env[i+1:]...)
		}
	}
	return env
}

// runUpdate updates the installed kern binaries by delegating to the
// canonical install.sh's `upgrade` operation: it compares the installed
// version against the latest release and swaps the binaries (with backup)
// when older. --dry-run runs the script's `status` operation instead — a
// check-only report, nothing swapped. This is the one kern command that
// intentionally touches the network (Persona 12: updating required
// remembering a curl|sh incantation; now the product owns it).
//
// Stage A release-channel policy (issue #561): BEFORE the network is
// touched, the installed version is classified and, when a target is known
// (--pin), the full UpdateDecision runs. A local-build stamp (Makefile
// short-hash, "dev") or an unverifiable version refuses with rebuild
// guidance — the old flow fell through install.sh's `v[0-9]` regex as "not
// installed" and silently overwrote the local binaries with a fresh
// install. --force is the ONLY override; it is forwarded to install.sh as
// KERN_FORCE=1, which install.sh's own preflight gate honors. --pin <tag>
// pins the target release (forwarded as KERN_VERSION, which install.sh's
// get_version honors) and consents to the downgrade it names.
//
// Stage C release channels: --channel <name> selects which release `latest`
// resolves to (stable = newest 3-component tag, hotfixes excluded; any
// other value = a regex over tag names). It is forwarded as KERN_CHANNEL
// and resolved by install.sh's get_version; an explicit --pin always
// overrides it. A channel resolving to an OLDER tag than installed still
// hits the same downgrade gate — --pin is the only consent.
func runUpdate(rest []string) {
	f, args := parseFlagsOrDie(rest)
	if len(args) > 0 {
		fatalUsage("update: unexpected argument %q — usage: kern update [--dry-run] [--force] [--pin <tag>] [--channel <name>]", args[0])
	}
	// Hidden --preflight <tag> mode: decide (installed, tag) and exit
	// 0=allow/no-op, 3=deny+reason, 2=usage. install.sh's cmd_upgrade calls
	// this before downloading anything; it never touches the network.
	if f.preflightSet {
		runUpdatePreflight(f.preflight)
		return
	}
	installed := version // package var, already Adopt()ed in init()

	// Local, network-free decision preview for --dry-run: rendered from the
	// SAME pure guards the real update runs (kversion.UpdateDecision with a
	// pin, updateGuardDenial without) — printed before the installer's
	// status op, which still runs unchanged and still changes nothing.
	if f.dryRun {
		fmt.Print(updateDecisionPreview(installed, f))
	}

	// Fail-closed local guard, no network. With --pin the target is known,
	// so the full decision runs; a --pin downgrade is the deliberate
	// downgrade the deny reason names, so it is allowed here (install.sh's
	// preflight re-check sees KERN_PIN=1 and allows it too). Every other
	// deny — local build, unverifiable installed version — refuses unless
	// --force.
	if msg := updateGuardDenial(installed, f); msg != "" {
		fatalPolicy("update: %s", msg)
	}

	curl, err := exec.LookPath("curl")
	if err != nil {
		fatal("update: curl is required to fetch the installer (%s) — install curl or run the script directly", installScriptURL)
	}
	op := "upgrade"
	if f.dryRun {
		op = "status"
		fmt.Printf("kern %s: checking for updates (dry-run, nothing will change)\n", version)
	} else {
		fmt.Printf("kern %s: updating via install.sh\n", version)
	}

	scriptURL := installScriptURLFor()
	fetch := exec.Command(curl, "-fsSL", scriptURL)
	install := exec.Command("sh", "-s", "--", op)
	install.Stdin, err = fetch.StdoutPipe()
	if err != nil {
		fatal("update: %v", err)
	}
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	fetch.Stderr = os.Stderr
	// Policy env forwarded to the installer child (updateChildEnv): KERN_FORCE=1
	// when the user passed --force (install.sh's preflight gate honors it),
	// KERN_VERSION=<pin> plus KERN_PIN=1 when --pin names the target
	// (get_version resolves the pin; the preflight re-check sees the
	// deliberate-downgrade consent), and KERN_CHANNEL=<channel> when
	// --channel selects a release channel (get_version resolves it; --pin
	// overrides it entirely).
	install.Env = updateChildEnv(f)
	if err := fetch.Start(); err != nil {
		fatal("update: could not start curl: %v", err)
	}
	if err := install.Start(); err != nil {
		fatal("update: could not start installer: %v", err)
	}
	fetchErr := fetch.Wait()
	installErr := install.Wait()
	if fetchErr != nil {
		fatal("update: could not fetch %s: %v", scriptURL, fetchErr)
	}
	if installErr != nil {
		fatal("update: installer failed: %v", installErr)
	}
}

// updateGuardDenial is the pure, network-free half of the Stage A policy:
// it returns the refusal message when the installed version must block
// `kern update` before install.sh is fetched, or "" when the update may
// proceed. It is a separate function so the guard is unit-testable without
// exec'ing anything — a test that reaches the real curl/install.sh path
// would be a live-update hazard. With --pin the full UpdateDecision runs
// and every deny but the deliberate pin downgrade refuses unless --force;
// without a pin, any Local/Unknown-provenance installed version refuses
// unless --force. Release-provenance installed versions always pass (zero
// behavior change for the normal update path).
func updateGuardDenial(installed string, f flags) string {
	if f.pin != "" {
		d := kversion.UpdateDecision(installed, f.pin)
		if d.Deny && d.Kind != kversion.DenyDowngrade && !f.force {
			return d.Reason
		}
		return ""
	}
	switch kversion.Provenance(installed) {
	case kversion.ProvenanceLocal:
		if !f.force {
			return fmt.Sprintf("refusing to overwrite a local build (%s) — rebuild via 'make build && make install', or re-run with --force", installed)
		}
	case kversion.ProvenanceUnknown:
		if !f.force {
			return fmt.Sprintf("cannot verify installed version %q — rebuild via 'make build && make install', or re-run with --force", installed)
		}
	}
	return ""
}

// updateDecisionPreview renders the local, network-free decision preview
// `kern update --dry-run` prints before the installer's status op: the
// installed version with its Provenance classification, the target (--pin
// when given, else the channel when one is set, else the installer-resolved
// "latest"), and the concrete decision computed by the SAME pure helpers the
// real update uses — kversion.UpdateDecision when a pin names the target,
// updateGuardDenial otherwise. No new policy logic: this is purely a
// rendering of the existing gates, so a deny shown here is exactly the
// refusal the real run would (also) produce. --force and pin-consent
// annotations reflect the overrides those same gates honor.
func updateDecisionPreview(installed string, f flags) string {
	prov := kversion.Provenance(installed)
	target := "latest (resolved by installer)"
	switch {
	case f.pin != "":
		target = f.pin
	case f.channel != "":
		target = fmt.Sprintf("%s channel (resolved by installer)", f.channel)
	}
	verdict := "allow"
	switch {
	case f.pin != "":
		d := kversion.UpdateDecision(installed, f.pin)
		switch {
		case d.Allow:
			verdict = "allow"
		case d.Noop:
			verdict = fmt.Sprintf("no-op — already at %s", f.pin)
		case d.Deny:
			verdict = "deny: " + d.Reason
			if f.force {
				verdict += " (overridden by --force)"
			}
		}
	default:
		if msg := updateGuardDenial(installed, f); msg != "" {
			verdict = "deny: " + msg
		} else if f.force {
			verdict = "allow (--force override)"
		}
	}
	return fmt.Sprintf("  installed: %s (%s)\n  target:     %s\n  decision:   %s\n", installed, prov, target, verdict)
}

// runUpdatePreflight prints the UpdateDecision verdict for (installed, tag)
// and exits: 0 allow/no-op, 3 deny (reason on stderr), 2 usage. install.sh
// calls this as `kern update --preflight <tag>` before any download; it
// never touches the network. A deliberate pin downgrade (KERN_PIN=1, set by
// `kern update --pin <tag>`) is allowed; every other deny is a hard refusal.
func runUpdatePreflight(tag string) {
	if tag == "" {
		fatalUsage("update --preflight: missing target tag — usage: kern update --preflight <tag>")
	}
	installed := version // package var, already Adopt()ed in init()
	d := kversion.UpdateDecision(installed, tag)
	switch {
	case d.Allow:
		fmt.Printf("preflight: allow — %s -> %s\n", installed, tag)
		return
	case d.Noop:
		fmt.Printf("preflight: no-op — already at %s\n", tag)
		return
	case d.Deny:
		if d.Kind == kversion.DenyDowngrade && os.Getenv("KERN_PIN") == "1" {
			fmt.Printf("preflight: allow — deliberate pin downgrade %s -> %s\n", installed, tag)
			return
		}
		fatalPolicy("update preflight: %s", d.Reason)
	}
}
