package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/enterprise"
	"github.com/JayveerPrajapati/kern/internal/web"
)

// defaultServeAddr is the default listen address for the serve command.
const defaultServeAddr = ":8090"

// serveProject is a single NAME=PATH project registration for enterprise mode.
type serveProject struct {
	name string
	root string
}

// serveUsage is the help text for kern serve.
const serveUsage = `usage: kern serve [--root PATH] [--addr ADDR] [--enterprise] [--project NAME=PATH]...

Start the kern REST API + HTML dashboard server.

Single-project mode (default) serves one project's digital twin — every /api
and /v1 endpoint plus the dashboard — on the given address. --enterprise
switches to multi-project mode: several projects behind one listener with a
shared org-level audit log, event bus, memory store and policy set. Enterprise
mode is fail-closed: every request requires "Authorization: Bearer $KERN_AUTH_TOKEN".

Flags:
  --root PATH          project root for single-project mode (default: .)
  --addr ADDR          listen address (default :8090)
  --enterprise         multi-project enterprise mode
  --project NAME=PATH  register a project (repeatable; --enterprise only).
                       With no --project flags, --root is registered as a single
                       project named after its base directory.

Examples:
  kern serve                                serve the current directory on :8090
  kern serve --root ./api --addr :8080
  kern serve --enterprise --project api=./api --project web=./web
`

// runServe starts the REST API and dashboard server.
func runServe(rest []string) {
	// serve/web take flags only; a stray positional (e.g. `kern web static`)
	// must fail loudly instead of silently starting a server on the default
	// address. Positionals are detected before any handler is constructed.
	if _, positionals, err := parseFlags(rest); err == nil && len(positionals) > 0 {
		fatalUsage("serve: unexpected argument: %s", positionals[0])
	}
	h, mode, err := buildServeHandler(rest)
	if err != nil {
		fatalUsage("serve: %v", err)
	}
	if h == nil {
		return // help/usage printed
	}
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("serve: %v", err)
	}
	addr := f.addr
	if addr == "" {
		addr = os.Getenv("KERN_ADDR")
	}
	if addr == "" {
		addr = defaultServeAddr
	}
	if mode == "enterprise" {
		projects := 1
		if len(f.projects) > 0 {
			projects = len(f.projects)
		}
		if os.Getenv("KERN_AUTH_TOKEN") == "" {
			log.Printf("kern serve: WARNING: KERN_AUTH_TOKEN is unset — enterprise mode will refuse every request with 503 until it is set and requests carry 'Authorization: Bearer <token>'")
		}
		log.Printf("kern serve: enterprise mode on %s (%d project(s))", addr, projects)
	} else {
		root := f.root
		if root == "" {
			root = "."
		}
		log.Printf("kern serve: single-project mode on %s (root: %s)", addr, root)
	}
	// Graceful shutdown: on SIGINT/SIGTERM drain in-flight requests for up to
	// 10s before returning (exit 0), so the REST API + dashboard is not
	// hard-killed mid-request. Mirrors cmd/kern-server/main.go serve(). The
	// goroutine owns stop() (called only after a real signal), so a startup
	// failure path — where fatal() panics out of runServe — never logs a
	// spurious "shutting down..." for a server that did not come up.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	srv := &http.Server{Addr: addr, Handler: h}
	go func() {
		<-ctx.Done()
		stop()
		log.Printf("kern serve: shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("kern serve: shutdown error: %v", err)
		}
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal("serve failed: %v — is the port in use? (change with --addr)", err)
	}
}

// buildServeHandler constructs the HTTP handler from serve args. Only an
// explicit help request prints usage and returns nil; a bare `kern serve`
// (zero args) starts the server in single-project mode on the default
// address (or KERN_ADDR when set).
func buildServeHandler(args []string) (http.Handler, string, error) {
	f, _, err := parseFlags(args)
	if err != nil {
		return nil, "", err
	}
	if f.help {
		fmt.Fprint(os.Stderr, serveUsage)
		return nil, "", nil
	}
	root := f.root
	if root == "" {
		root = "."
	}
	if f.enterprise {
		srv := enterprise.New()
		projects, err := resolveServeProjects(f.projects, root)
		if err != nil {
			return nil, "", err
		}
		for _, p := range projects {
			if err := srv.Register(p.name, p.root); err != nil {
				return nil, "", err
			}
		}
		return srv, "enterprise", nil
	}
	app, err := web.New(root)
	if err != nil {
		return nil, "", err
	}
	return app, "single-project", nil
}

// resolveServeProjects parses --project NAME=PATH pairs into registrations.
func resolveServeProjects(pairs []string, defaultRoot string) ([]serveProject, error) {
	if len(pairs) == 0 {
		return []serveProject{{name: projectNameFromRoot(defaultRoot), root: defaultRoot}}, nil
	}
	projects := make([]serveProject, 0, len(pairs))
	for _, pair := range pairs {
		name, root, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("--project must be NAME=PATH, got %q", pair)
		}
		projects = append(projects, serveProject{name: name, root: root})
	}
	return projects, nil
}

// projectNameFromRoot derives a project name from a directory path.
func projectNameFromRoot(root string) string {
	name := filepath.Base(root)
	if name == "." || name == string(filepath.Separator) || name == "" {
		if abs, err := filepath.Abs(root); err == nil {
			name = filepath.Base(abs)
		}
	}
	return name
}
