//go:build linux

package relay

import (
	"net"
	"os"
	"syscall"
)

func checkPeerCredentials(conn net.Conn) bool {
	uconn, ok := conn.(*net.UnixConn)
	if !ok {
		return true
	}
	raw, err := uconn.SyscallConn()
	if err != nil {
		return false
	}
	var cred *syscall.Ucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil || credErr != nil || cred == nil {
		return false
	}
	return cred.Uid == uint32(os.Getuid())
}
