//go:build !linux

package relay

import "net"

func checkPeerCredentials(conn net.Conn) bool {
	return true
}
