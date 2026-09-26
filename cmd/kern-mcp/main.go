// Command kern-mcp runs the kern MCP server over stdio (default) or HTTP
// (--http ADDR). The HTTP transport is Streamable HTTP style: POST JSON-RPC
// messages to /mcp and read the response body. An explicit ADDR binds
// loopback TCP (e.g. :8080) or a unix socket (unix:/path); the sentinel
// "auto" (the recommended default) serves on a 0600 unix socket in a fresh
// 0700 temp dir, announced on stderr — loopback TCP on 127.0.0.1:8080 is
// used only when unix sockets are unavailable (Windows) or the legacy
// behavior is forced with KERN_MCP_TRANSPORT=tcp. TLS is optional: pass
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
	"strings"
	"syscall"

	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/mcp/transport"
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

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringList) Set(val string) error {
	*s = append(*s, val)
	return nil
}

func main() {
	var projects stringList
	flag.Var(&projects, "project", "register a project (NAME=PATH, repeatable)")
	httpAddr := flag.String("http", "", "serve MCP over HTTP instead of stdio: an address (:8080, unix:/path) or \"auto\" for a 0600 unix socket (default transport; force loopback TCP with KERN_MCP_TRANSPORT=tcp)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM) for the HTTP transport; env KERN_MCP_TLS_CERT")
	tlsKey := flag.String("tls-key", "", "TLS private key file (PEM) for the HTTP transport; env KERN_MCP_TLS_KEY")
	longVer := flag.Bool("version", false, "print version and exit")
	shortVer := flag.Bool("v", false, "shorthand for -version")
	flag.Parse()
	if len(projects) > 0 {
		var roots []string
		for _, p := range projects {
			_, root, ok := strings.Cut(p, "=")
			if !ok || root == "" {
				root = p
			}
			roots = append(roots, root)
		}
		existing := os.Getenv("KERN_MCP_ROOTS")
		if existing != "" {
			roots = append(roots, strings.Split(existing, ",")...)
		}
		_ = os.Setenv("KERN_MCP_ROOTS", strings.Join(roots, ","))
		_ = os.Setenv("KERN_ROOTS", strings.Join(roots, ","))
	}
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
		// Transport auto-selection (see mcp.ResolveHTTPAddr): "auto"/"uds"
		// serve on a 0600 unix socket in a fresh 0700 temp dir — the secure
		// default, since a loopback TCP port is reachable by ANY local
		// process on a multi-user host. The socket path is announced on
		// stderr so clients can connect; the temp dir is removed at exit
		// (the socket itself is unlinked by the listener on shutdown).
		// Explicit addresses (":8080", "unix:/path") pass through unchanged.
		isAuto := *httpAddr == "auto" || *httpAddr == "uds"
		listenAddr, cleanup := mcp.ResolveHTTPAddr(*httpAddr)
		if cleanup != nil {
			defer cleanup()
		}
		if isAuto {
			fmt.Fprintf(os.Stderr, "kern-mcp: listening on %s\n", listenAddr)
		}
		tlsCfg := transport.TLSOptions(*tlsCert, *tlsKey)
		if err := mcp.ServeHTTPContextWithTLS(ctx, listenAddr, tlsCfg); err != nil {
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
