// Command kern-mcp runs the kern MCP server over stdio (default) or HTTP
// (--http ADDR). The HTTP transport is Streamable HTTP style: POST JSON-RPC
// messages to /mcp and read the response body. TLS is optional: pass
// --tls-cert/--tls-key (or set KERN_MCP_TLS_CERT/KERN_MCP_TLS_KEY) to serve
// HTTPS instead of plain HTTP; without them the server falls back to plain
// HTTP on loopback.
// SIGINT/SIGTERM trigger a graceful shutdown: in-flight tool calls are
// cancelled (their child processes killed), held locks are released, and the
// server stops reading input. This keeps slow tools from hanging the process
// until the OS force-kills it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	kversion "github.com/JayveerPrajapati/kern/internal/version"
)

// version is the build-stamped release version, initialized from the shared
// internal/version.Version so every kern binary reports the same value.
// It starts as the literal "dev" (not a copy of kversion.Version) because
// the legacy -ldflags "-X main.version=..." only rewrites a variable whose
// initializer is a compile-time constant: a runtime copy from another global
// aliases the read and silently defeats -X. When unstamped, init() adopts
// the shared internal/version.Version (default "dev", or the newer
// "-X github.com/JayveerPrajapati/kern/internal/version.Version=..." form).
var version = "dev"

func init() {
	version = kversion.Adopt(version)
}

func main() {
	httpAddr := flag.String("http", "", "serve MCP over HTTP on this address (e.g. :8080) instead of stdio")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM) for the HTTP transport; env KERN_MCP_TLS_CERT")
	tlsKey := flag.String("tls-key", "", "TLS private key file (PEM) for the HTTP transport; env KERN_MCP_TLS_KEY")
	longVer := flag.Bool("version", false, "print version and exit")
	shortVer := flag.Bool("v", false, "shorthand for -version")
	flag.Parse()
	// Same contract as the other binaries (blueprint-mcp precedent,
	// f778aff): -v/--version/version all print the ldflags-stamped
	// version and exit 0, so doctor's version-parity probe and shell
	// scripts can read it without starting the stdio server.
	if *longVer || *shortVer || (flag.NArg() > 0 && flag.Arg(0) == "version") {
		fmt.Println("kern-mcp " + version)
		return
	}
	mcp.SetServerVersion(version)
	_ = optimize.EnsureRecorder()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *httpAddr != "" {
		tlsCfg := mcp.TLSOptions(*tlsCert, *tlsKey)
		if err := mcp.ServeHTTPContextWithTLS(ctx, *httpAddr, tlsCfg); err != nil {
			os.Exit(1)
		}
		return
	}
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	// ServeStdio owns the SIGINT/SIGTERM drain (cancel in-flight tools,
	// release locks, close stdin, wait up to 5s for in-flight calls). It
	// never calls os.Exit: a clean drain returns nil (exit 0), a drain
	// timeout or serve error returns a non-nil error (exit 1), so deferred
	// cleanup runs on every path.
	if err := mcp.ServeStdio(srv); err != nil {
		os.Exit(1)
	}
}
