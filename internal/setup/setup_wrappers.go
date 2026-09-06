package setup

import (
	"os/exec"
	"strings"

	kernversion "github.com/JayveerPrajapati/kern/internal/version"
)

// wrapperVersion runs `<bin> version` and returns the bare version string the
// binary reports (everything before the first space-splitting of the output is
// the program name, the second token is the version). A launch failure or
// empty output yields ("", false) so callers can warn without blocking.
func wrapperVersion(bin string) (string, bool) {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "", false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", false
	}
	return fields[1], true
}

// checkWrapperFreshness verifies that leftover liability binaries on PATH
// (blueprint, blueprint-mcp — legacy shims kept only for backward
// compatibility, no longer built or installed by CI/Makefile/setup) report
// the same version as the running kern binary. After a release upgrade, stale
// standalone leftovers can silently downgrade or bypass the pre-commit
// governance gate (A16): an old hook may run `exec blueprint check` and would
// invoke the old binary. The check is advisory — it reports a mismatch with a
// refresh hint, never by itself a failure. A healthy deployment (no leftover
// installed, or matching versions) contributes no Status entries so
// `kern setup --check` stays clean.
func checkWrapperFreshness() []Status {
	own := kernversion.Version
	var out []Status
	for _, bin := range []string{"blueprint", "blueprint-mcp"} {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue // not installed via this PATH; no check applies
		}
		v, ok := wrapperVersion(bin)
		if !ok {
			out = append(out, Status{
				Agent:     bin,
				Installed: true,
				Path:      path,
				Note:      "WARN: cannot read version from " + path + "; refresh it from the same release as kern",
			})
			continue
		}
		if own != "dev" && v != own {
			out = append(out, Status{
				Agent:     bin,
				Installed: true,
				Path:      path,
				Note:      "STALE: " + bin + " " + v + " != kern " + own + " — refresh from the same release (else the pre-commit hook may run an old governance gate)",
			})
			continue
		}
	}
	return out
}