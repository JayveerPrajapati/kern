package admin

import "example.com/netcfg/ports"

// Port parses the admin interface port.
func Port(s string) (int, error) {
	return ports.Parse(s)
}
