package relay

import (
	"runtime"
	"strings"
	"testing"
)

// TestPeerCredentialsUnsupportedError pins the fail-closed contract for
// platforms without a peer-credential mechanism. The peer_other.go
// implementation only compiles on unsupported GOOSes (not on linux/darwin dev
// machines), so this pins the shared message helper the unsupported path
// reports: fail closed AND loud — the error must name the platform and state
// that connections are refused, never a bare silent false.
func TestPeerCredentialsUnsupportedError(t *testing.T) {
	err := peerCredentialsUnsupportedError()
	if err == nil {
		t.Fatal("peerCredentialsUnsupportedError() = nil, want a non-nil error")
	}
	msg := err.Error()
	for _, want := range []string{
		"peer credential check unsupported on this platform",
		"connections refused",
		runtime.GOOS,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
}
