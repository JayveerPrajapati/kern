package proxy

import "example.com/netcfg/ports"

// UpstreamPort parses the port of the upstream the proxy forwards to.
func UpstreamPort(s string) (int, error) {
	return ports.Parse(s, 65535)
}
