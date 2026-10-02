//go:build !linux && !darwin

package relay

import "net"

// checkPeerCredentials on platforms other than Linux and macOS (e.g. Windows,
// the BSDs, Plan 9) has no portable equivalent of SO_PEERCRED/LOCAL_PEERCRED
// or getpeereid for Unix domain sockets. Windows notably lacks a clean way to
// recover the peer's identity on an AF_UNIX connection.
//
// This is a documented limitation: rather than trusting any local process that
// can reach the socket (the previous fail-open behavior), we fail closed and
// reject every connection. This disables the relay on these platforms until a
// platform-specific credential check is implemented.
//
// Contract: ok==true only when the peer is verified to belong to the current
// user (fail closed otherwise — an unverifiable peer is rejected); err!=nil
// reports that the platform cannot perform the check at all, which callers
// MUST log loudly while still rejecting the connection.
func checkPeerCredentials(conn net.Conn) (bool, error) {
	return false, peerCredentialsUnsupportedError()
}
