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
// returns a struct xucred containing the peer's effective UID.
func checkPeerCredentials(conn net.Conn) bool {
	uconn, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := uconn.SyscallConn()
	if err != nil {
		return false
	}
	var cred *unix.Xucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if err != nil || credErr != nil || cred == nil {
		return false
	}
	return cred.Uid == uint32(os.Getuid())
}
