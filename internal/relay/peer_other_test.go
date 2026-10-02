//go:build !linux && !darwin

package relay

import (
	"net"
	"strings"
	"testing"
)

// TestCheckPeerCredentialsFailsClosed verifies the fail-closed contract on
// platforms where the peer-credential check is unsupported: every connection
// is rejected (ok=false) and the rejection carries a loud, actionable error
// instead of a bare false — an unsupported platform must never silently drop
// connections or trust an unverifiable peer.
func TestCheckPeerCredentialsFailsClosed(t *testing.T) {
	ok, err := checkPeerCredentials(&net.UnixConn{})
	if ok {
		t.Fatal("checkPeerCredentials = (true, _) on an unsupported platform; want fail-closed false")
	}
	if err == nil {
		t.Fatal("checkPeerCredentials = (false, nil) on an unsupported platform; want a loud error")
	}
	if !strings.Contains(err.Error(), "connections refused") {
		t.Errorf("error %q should state that connections are refused", err.Error())
	}
}
