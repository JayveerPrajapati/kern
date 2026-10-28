package mcp

// slimwire.go keeps the slim-mode wiring in one place: toolpolicy decides WHICH
// tools are advertised, slim.go decides how compactly, and this file joins them.

// slimIfRequested applies the opt-in compact advertisement (see slim.go).
func slimIfRequested(ts []Tool) []Tool {
	if !slimEnabled() {
		return ts
	}
	return slimTools(ts)
}
