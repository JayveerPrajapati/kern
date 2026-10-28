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

	"github.com/JayveerPrajapati/kern/internal/index"
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

// preindexRoots synchronously builds and persists each listed root's symbol
// index (bare path or NAME=PATH) so a later tool call loads it instead of
// blocking on a cold build (MCP audit 2026-10-25, R6). It reuses
// index.LoadOrBuild, so a fresh root gets its one-time build announced on
// stderr and persisted to .kern/ for the server and any later kern CLI run.
func preindexRoots(entries []string) error {
	for _, e := range entries {
		_, root, ok := strings.Cut(e, "=")
		if !ok || root == "" {
			root = e
		}
		if _, err := index.LoadOrBuild(root); err != nil {
			return fmt.Errorf("preindex %s: %w", root, err)
		}
	}
	return nil
}

func main() {
	var projects stringList
	flag.Var(&projects, "project", "register a project (NAME=PATH, repeatable)")
	var preindex stringList
	flag.Var(&preindex, "preindex", "warm and persist the index for a project root before serving (PATH or NAME=PATH, repeatable)")
	httpAddr := flag.String("http", "", "serve MCP over HTTP instead of stdio: an address (:8080, unix:/path) or \"auto\" for a 0600 unix socket (default transport; force loopback TCP with KERN_MCP_TRANSPORT=tcp)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM) for the HTTP transport; env KERN_MCP_TLS_CERT")
	tlsKey := flag.String("tls-key", "", "TLS private key file (PEM) for the HTTP transport; env KERN_MCP_TLS_KEY")
	longVer := flag.Bool("version", false, "print version and exit")
	shortVer := flag.Bool("v", false, "shorthand for -version")
	// Augment the default usage with the binary's purpose and the
	// environment surface: an operator (or agent) bootstrapping from --help
	// alone otherwise never learns what the server is or that the governance
	// knobs exist (envelope audit F-5 2026-10-25; W3, 2026-10-26 black-box
	// audit: the bare flag dump carried no purpose line).
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `%[1]s — the kern MCP server.

Serves kern's local code-intelligence tools (kern_explore, kern_search,
kern_verify, ...) to MCP clients over stdio (the default; speak JSON-RPC on
stdin/stdout) or HTTP (-http). Every call is answered from a prebuilt
per-project symbol index — nothing leaves the machine.

Usage: %[1]s [flags]

Flags:
`, os.Args[0])
		flag.PrintDefaults()
		fmt.Fprint(os.Stderr, `
Governance and tuning (environment): KERN_MCP_ROOTS, KERN_MCP_PHASE,
KERN_MCP_FULL, KERN_PRELOAD, KERN_MCP_WATCH, KERN_MCP_TRANSPORT,
KERN_ALLOW_EXEC — full reference: docs/mcp/protocol.md
`)
	}
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
	// Fail loudly at startup on a misspelled KERN_MCP_CATEGORY: a no-op
	// filter is a silent capability loss (I8). Same gate as `kern mcp`.
	if err := mcp.ValidateMCPSurfaceEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "kern-mcp:", err)
		os.Exit(2)
	}
	mcp.SetServerVersion(version)
	_ = optimize.EnsureRecorder()
	// R6 (MCP audit 2026-10-25): --preindex warms project roots
	// synchronously before serving, so a deploy script can warm the roots it
	// will serve in one process and the first tool call finds a persisted
	// index instead of blocking on a multi-minute build.
	if len(preindex) > 0 {
		if err := preindexRoots(preindex); err != nil {
			fmt.Fprintln(os.Stderr, "kern-mcp:", err)
			os.Exit(1)
		}
	}
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
			// os.Exit skips deferred functions — run the auto-selected
			// socket-dir cleanup before exiting, or the temp dir leaks
			// (W18, 2026-10-26 black-box audit; the graceful SIGTERM path
			// returns nil and the deferred cleanup runs normally).
			if cleanup != nil {
				cleanup()
			}
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
