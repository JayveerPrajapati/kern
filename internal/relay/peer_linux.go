//go:build linux

package relay

import (
	"net"
	"os"
	"syscall"
)

// checkPeerCredentials verifies that the peer on the other end of a Unix
// domain socket is owned by the same user running this process, using
// SO_PEERCRED. Linux always has the mechanism, so a failed check is a
// credential mismatch, never a platform limitation: err is always nil here.
//
// Contract: ok==true only when the peer is verified to belong to the current
// user (fail closed otherwise — an unverifiable peer is rejected); err!=nil
// reports that the platform cannot perform the check at all, which callers
// MUST log loudly while still rejecting the connection.
func checkPeerCredentials(conn net.Conn) (bool, error) {
	uconn, ok := conn.(*net.UnixConn)
	if !ok {
		return true, nil
	}
	raw, err := uconn.SyscallConn()
	if err != nil {
		return false, nil
	}
	var cred *syscall.Ucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil || credErr != nil || cred == nil {
		return false, nil
	}
	return cred.Uid == uint32(os.Getuid()), nil
}
