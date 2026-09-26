package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp"
)

func runVersion(rest []string) {
	// F22: version previously ignored every flag (even `kern version
	// --nope` exited 0). Parse flags so an unknown flag is a usage error
	// (rc=2); known flags (--json, --root, ...) are accepted and ignored,
	// keeping `kern version`, `kern --version` and `kern -v` working.
	if _, _, err := parseFlags(rest); err != nil {
		fatalUsage("flags: %v", err)
	}
	v := version
	if v == "dev" {
		// Unstamped source build (plain `go build`, no Makefile ldflags):
		// report the VCS revision the toolchain embedded at build time, or
		// fall back to the checkout's HEAD so `kern version` never prints a
		// bare "dev" for a binary built from a git clone.
		if rev := buildInfoRevision(); rev != "" {
			// The toolchain embeds the full 40-char revision; keep the
			// conventional 7-char short form the Makefile stamps.
			if len(rev) == 40 {
				rev = rev[:7]
			}
			v = rev + " (dev)"
		} else if h := shortHash(); h != "" {
			v = h + " (dev)"
		}
	}
	fmt.Printf("kern %s\n", v)
}

// buildInfoRevision returns the git revision embedded by the Go toolchain
// (vcs.revision build setting) when the binary was built from a git
// checkout, or "" when absent (e.g. `go install pkg@version` from the module
// cache, or a release tarball build).
func buildInfoRevision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

func runGuide(rest []string) {
	// QA: `kern guide --nonsense` previously ignored every flag (exit 0).
	// guide takes no flags; any token starting with "-" is a usage error
	// (--help/-h is intercepted by the dispatcher before dispatch).
	for _, a := range rest {
		if strings.HasPrefix(a, "-") {
			fatalUsage("unknown flag %q", a)
		}
	}
	fmt.Println(mcp.Guide())
}

// runMeta implements the `kern meta` CLI subcommand — the CLI mirror of the
// kern_meta MCP meta-tool. It takes a natural-language request, classifies it
// (via the same internal classifier the MCP server uses), and dispatches to
// the appropriate CLI subcommand. This lets shell users get the same
// "describe what you want, kern picks the tool" experience as agents.
// Usage: kern meta "<request>" [--root ROOT]
// Example: kern meta "show me the architecture"
// kern meta "how does dispatch work"
// kern meta "find the NewServer function"
func runMeta(rest []string) {
	// --pipeline routes to the deterministic multi-tool compose engine
	// (surface consolidation T2b): `kern compose` is now a thin wrapper over
	// `kern meta --pipeline`, and both spellings execute the JSON pipeline
	// instead of NL routing. The whole remaining arg vector is passed
	// through so every input form the compose command historically accepted
	// (--pipeline flag, positional JSON, stdin) keeps working.
	for _, a := range rest {
		if a == "--pipeline" || strings.HasPrefix(a, "--pipeline=") {
			runComposeCore(rest)
			return
		}
	}
	var request string
	root := "."
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--root":
			i++
			if i < len(rest) {
				root = rest[i]
			}
		case "--help", "-h":
			fmt.Println(`kern meta "<request>" [--root ROOT]

Single entry point: describe what you need in natural language and kern
classifies the request and runs the right tool internally. Examples:
  kern meta "show me the architecture"
  kern meta "how does dispatch work"
  kern meta "find the NewServer function"
  kern meta "what breaks if I change dispatch"
  kern meta "compress this log: ERROR: dispatchCommand failed"`)
			return
		default:
			// QA: an unknown flag AFTER the free-form request was previously
			// silently appended to it (exit 0). Reject it as a usage error.
			// A leading "-..." token is still the request when nothing has
			// been captured yet (a quoted single-argument request like
			// `kern meta "-x flag"` arrives as ONE arg and must keep
			// working) — mirroring how parseFlags-based commands reject
			// unknown flags without touching positionals.
			if strings.HasPrefix(rest[i], "-") && request != "" {
				fatalUsage("unknown flag %q", rest[i])
			}
			if request == "" {
				request = rest[i]
			} else {
				request += " " + rest[i]
			}
		}
	}
	if strings.TrimSpace(request) == "" {
		fatalUsage(`usage: kern meta "<request>" [--root ROOT]
describe what you need and kern picks the right tool. Example:
  kern meta "show me the architecture"`)
	}

	// Re-dispatch via the MCP server's internal classifier. We build a
	// minimal args map and call the same handleMeta the MCP server uses, so
	// CLI and MCP stay perfectly in sync.
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	args := map[string]any{"request": request, "root": root}
	out, err := srv.HandleMeta(context.Background(), args)
	if err != nil {
		fatal("meta: %v", err)
	}
	fmt.Println(out)
}
