package server

import "example.com/netcfg/ports"

// ListenPort parses the port the server listens on.
func ListenPort(s string) (int, error) {
	return ports.Parse(s)
}
