//go:build darwin

package relay

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// checkPeerCredentials verifies that the peer on the other end of a Unix
// domain socket is owned by the same user running this process. macOS has no
// SO_PEERCRED; the equivalent is the LOCAL_PEERCRED socket option, which
// returns a struct xucred containing the peer's effective UID. macOS always
// has the mechanism, so err is always nil here.
//
// Contract: ok==true only when the peer is verified to belong to the current
// user (fail closed otherwise — an unverifiable peer is rejected); err!=nil
// reports that the platform cannot perform the check at all, which callers
// MUST log loudly while still rejecting the connection.
func checkPeerCredentials(conn net.Conn) (bool, error) {
	uconn, ok := conn.(*net.UnixConn)
	if !ok {
		return false, nil
	}
	raw, err := uconn.SyscallConn()
	if err != nil {
		return false, nil
	}
	var cred *unix.Xucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if err != nil || credErr != nil || cred == nil {
		return false, nil
	}
	return cred.Uid == uint32(os.Getuid()), nil
}
