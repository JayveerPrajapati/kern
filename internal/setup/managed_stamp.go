package setup

import (
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/version"
)

// The managed-version stamp (deep-dive C7, 2026-10-03): every kern-managed
// global section carries the version of the binary that wrote it, and a
// rewrite is refused while the existing section is stamped NEWER than the
// running binary — so a stale release binary (e.g. an install.sh
// post-install step running the RELEASE binary's setup) can never silently
// clobber newer local wiring. Backups (`backupFile`) provide the rollback
// path for everything else.

// kernVersionStampRe matches the stamp line written inside every
// kern-managed section: "<!-- kern-version: <v> -->".
var kernVersionStampRe = regexp.MustCompile(`<!--\s*kern-version:\s*([^>\s]+)\s*-->`)

// stampKernVersion returns the stamp line for the running binary's version.
func stampKernVersion() string {
	return "<!-- kern-version: " + version.Version + " -->"
}

// insertManagedStamp stamps s with the running version: any older stamp
// lines are dropped first (idempotent re-runs), then the fresh stamp is
// inserted directly under the managed marker line (marker blocks) or the
// "# kern usage rules" heading (unmarked sections). Content without either
// managed anchor is returned unchanged.
func insertManagedStamp(s string) string {
	s = kernVersionStampRe.ReplaceAllString(s, "")
	if i := strings.Index(s, globalRulesMarkerOpen); i >= 0 {
		cut := i + len(globalRulesMarkerOpen)
		return s[:cut] + "\n" + stampKernVersion() + s[cut:]
	}
	const heading = "# kern usage rules"
	if i := strings.Index(s, heading); i >= 0 {
		cut := i + len(heading)
		return s[:cut] + "\n" + stampKernVersion() + s[cut:]
	}
	return s
}

// managedStamp returns the first kern-version stamp found anywhere in the
// content ("" when the file predates stamping).
func managedStamp(content string) string {
	if m := kernVersionStampRe.FindStringSubmatch(content); m != nil {
		return m[1]
	}
	return ""
}

// managedNewerThanRunning reports whether a stamped managed-section version
// v is strictly newer than the running binary, per the release-channel
// policy (internal/version):
//   - Release vs Release: numeric tuple order (version.Compare > 0).
//   - Local stamp (dev / git hash) vs a Release binary: newer — a local
//     build's global wiring must never be clobbered by a release binary
//     (the install.sh post-install footgun).
//   - Release stamp vs a Local ("dev") binary: not newer — the local
//     iteration flow (rebuild + re-run setup) keeps working.
//   - Unknown or missing stamps: not newer (best-effort rewrite).
func managedNewerThanRunning(v string) bool {
	running := version.Provenance(version.Version)
	switch version.Provenance(v) {
	case version.ProvenanceRelease:
		if running != version.ProvenanceRelease {
			return false
		}
		a, errA := version.Parse(v)
		b, errB := version.Parse(version.Version)
		return errA == nil && errB == nil && version.Compare(a, b) > 0
	case version.ProvenanceLocal:
		return running == version.ProvenanceRelease
	default:
		return false
	}
}
