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
func checkPeerCredentials(conn net.Conn) bool {
	return false
}
