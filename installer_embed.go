// Package kern is the module-root package. Its single job is embedding the
// repo-root installer scripts (install.sh, install.ps1) so `kern uninstall`
// can run them fully OFFLINE: go:embed can only reach files inside the
// embedding package's own directory subtree, and the canonical scripts live
// at the repo root — this package IS that directory, so the root scripts
// stay the single source of truth with no synced copies to drift.
//
// Why it matters: uninstall must never depend on network availability —
// fetching the script from the release host meant any server error (e.g. a
// 502) bricked phase 2 of `kern uninstall` on a perfectly healthy machine.
// `kern update` still fetches the remote script deliberately: it downloads
// releases anyway and wants the release-matched script version.
//
// Architecture note: this package intentionally lives at the module root,
// outside the internal/ ledger trees — the parity gate governs packages
// under internal/, cmd/ and evaluate/, and imports of the module root
// itself are not internal-subsystem imports. Nothing else should live
// here; a root package is reserved for exactly this kind of repo-root
// asset embedding.
package kern

import _ "embed"

//go:embed install.sh
var installSH []byte

//go:embed install.ps1
var installPS1 []byte

// InstallerScript returns the installer script matching goos ("windows"
// gets install.ps1, everything else install.sh) plus its file name — used
// for the temp-file suffix and diagnostics.
func InstallerScript(goos string) (name string, content []byte) {
	if goos == "windows" {
		return "install.ps1", installPS1
	}
	return "install.sh", installSH
}
