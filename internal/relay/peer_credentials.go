package relay

import (
	"fmt"
	"runtime"
)

// peerCredentialsUnsupportedError builds the error reported when the current
// platform has no peer-credential mechanism for Unix domain sockets (no
// SO_PEERCRED/LOCAL_PEERCRED equivalent or getpeereid — e.g. Windows, the
// BSDs, Plan 9). It exists in a build-tag-free file so the message can be
// pinned by a unit test on any build platform; the peer_other.go
// implementation that returns it only compiles on unsupported GOOSes.
//
// The checkPeerCredentials contract (see the per-platform implementations):
// a platform that cannot verify the peer MUST return ok=false — fail closed,
// never trust an unverifiable peer — together with this non-nil error, so the
// caller rejects the connection loudly instead of silently. An unsupported
// platform disables the relay entirely; that must never be a silent drop.
func peerCredentialsUnsupportedError() error {
	return fmt.Errorf("relay peer credential check unsupported on this platform (GOOS=%s) — connections refused", runtime.GOOS)
}
