package mcp

import (
	"io"
	"strings"
)

func newTestServer() *Server {
	return NewServer(strings.NewReader(""), io.Discard)
}
