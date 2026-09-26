package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"github.com/JayveerPrajapati/kern/internal/mcp/transport"
)

func runMCP(rest []string) {
	// `kern mcp tools` lists the catalog instead of starting a server —
	// the discoverability front door for the long tail the default
	// 11-tool MCP advertisement hides behind kern_meta (KERN_MCP_FULL=1
	// exposes all; the meta router reaches everything).
	if len(rest) > 0 && rest[0] == "tools" {
		runMCPToolsList(rest[1:])
		return
	}
	f, args := parseFlagsOrDie(rest)
	httpAddr := mcpHTTPAddr(args, f)
	if len(f.projects) > 0 {
		var roots []string
		for _, p := range f.projects {
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
	wireRecorder()
	mcp.SetServerVersion(version)
	if httpAddr != "" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		// Transport auto-selection (see mcp.ResolveHTTPAddr): "auto"/"uds"
		// serve on a 0600 unix socket in a fresh 0700 temp dir — the secure
		// default, since a loopback TCP port is reachable by ANY local
		// process on a multi-user host. The socket path is announced on
		// stderr so clients can connect; the temp dir is removed at exit
		// (the socket itself is unlinked by the listener on shutdown).
		// Explicit addresses (":8080", "unix:/path") pass through unchanged.
		isAuto := httpAddr == "auto" || httpAddr == "uds"
		listenAddr, cleanup := mcp.ResolveHTTPAddr(httpAddr)
		if cleanup != nil {
			defer cleanup()
		}
		if isAuto {
			fmt.Fprintf(os.Stderr, "kern: mcp: listening on %s\n", listenAddr)
		}
		// TLS is optional: --tls-cert/--tls-key flags win, KERN_MCP_TLS_CERT /
		// KERN_MCP_TLS_KEY env vars fill in what the flags leave empty.
		tlsCfg := transport.TLSOptions(f.tlsCert, f.tlsKey)
		if err := mcp.ServeHTTPContextWithTLS(ctx, listenAddr, tlsCfg); err != nil {
			// Route through fatal so main() persists metrics before the
			// real exit (same exit code 1 as before).
			fatal("mcp: %v", err)
		}
		return
	}
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	// ServeStdio owns the SIGINT/SIGTERM drain (cancel in-flight tools,
	// release locks, close stdin, wait up to 5s for in-flight calls). It
	// never calls os.Exit: a clean drain returns nil (exit 0), a drain
	// timeout or serve error returns a non-nil error (exit 1) routed through
	// fatal so main() persists metrics before the real exit.
	if err := mcp.ServeStdio(srv); err != nil {
		fatal("mcp: %v", err)
	}
}

// runMCPToolsList prints the full MCP tool catalog, grouped by category.
// It is the discoverability front door for the ~145-tool surface: the
// default MCP advertisement exposes 11 starter tools and hides the rest
// behind the kern_meta router, so a user (or agent) otherwise has no way
// to enumerate every capability, its phase and its risk level.
func runMCPToolsList(rest []string) {
	fs := flag.NewFlagSet("kern mcp tools", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "emit JSON output")
	category := fs.String("category", "", "only list one category (e.g. graph, governance)")
	if err := fs.Parse(rest); err != nil {
		fatalUsage("kern mcp tools: %v", err)
	}
	// A bare positional arg filters by category: `kern mcp tools graph`.
	if fs.NArg() > 0 && *category == "" {
		*category = fs.Arg(0)
	}

	tools := catalog.All
	if *category != "" {
		var filtered []catalog.Tool
		for _, t := range tools {
			if t.Category == *category {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == 0 {
			fatal("kern mcp tools: no tools in category %q", *category)
		}
		tools = filtered
	}

	if *jsonOut {
		type toolInfo struct {
			Name        string `json:"name"`
			Category    string `json:"category"`
			Phase       string `json:"phase,omitempty"`
			RiskLevel   string `json:"riskLevel,omitempty"`
			Description string `json:"description"`
		}
		out := make([]toolInfo, 0, len(tools))
		for _, t := range tools {
			out = append(out, toolInfo{t.Name, t.Category, t.Phase, t.RiskLevel, t.Description})
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fatal("kern mcp tools: %v", err)
		}
		fmt.Println(string(b))
		return
	}

	fmt.Printf("kern MCP catalog: %d tools (default MCP surface advertises 11; KERN_MCP_FULL=1 exposes all; kern_meta routes to everything)\n\n", len(tools))
	byCat := map[string][]catalog.Tool{}
	for _, t := range tools {
		byCat[t.Category] = append(byCat[t.Category], t)
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		fmt.Printf("%s (%d):\n", c, len(byCat[c]))
		for _, t := range byCat[c] {
			fmt.Printf("  %-34s %-8s %s\n", t.Name, t.RiskLevel, clipDesc(t.Description))
		}
		fmt.Println()
	}
}

// clipDesc truncates a tool description to one compact listing line.
func clipDesc(s string) string {
	if len(s) > 100 {
		return s[:97] + "..."
	}
	return s
}
