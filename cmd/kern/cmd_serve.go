package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/enterprise"
	"github.com/JayveerPrajapati/kern/internal/web"
)

// defaultServeAddr is the listen address used when --addr is not given. It
// matches the SDK default base URL (internal/sdk/client.go:
// DefaultBaseURL = "http://localhost:8090").
const defaultServeAddr = ":8090"

// serveProject is a single NAME=PATH project registration for enterprise mode.
type serveProject struct {
	name string
	root string
}

// serveUsage is the help text for `kern serve`. main() intercepts --help/-h on
// every subcommand before dispatch, but buildServeHandler also honors them so
// direct callers (and tests) get the same behaviour without starting a server.
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

// runServe implements `kern serve` / `kern web`. It builds the handler
// (without ever binding a port until ListenAndServe) and blocks forever
// serving it. A nil handler with no error means help/usage was printed.
func runServe(rest []string) {
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
	log.Fatal(http.ListenAndServe(addr, h))
}

// buildServeHandler parses `kern serve` args and constructs the HTTP handler
// WITHOUT starting a listener, so tests can exercise the parsing + construction
// logic directly. The returned mode is "single-project" or "enterprise". A nil
// handler with a nil error means help/usage was printed and no server should
// start.
func buildServeHandler(args []string) (http.Handler, string, error) {
	f, _, err := parseFlags(args)
	if err != nil {
		return nil, "", err
	}
	if f.help || len(args) == 0 {
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

// resolveServeProjects turns --project NAME=PATH pairs into registrations. With
// no pairs, the default root is registered as a single project named after its
// base directory.
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

// projectNameFromRoot derives a project name from a directory path (its base
// directory). A bare "." resolves to the current directory's name.
func projectNameFromRoot(root string) string {
	name := filepath.Base(root)
	if name == "." || name == string(filepath.Separator) || name == "" {
		if abs, err := filepath.Abs(root); err == nil {
			name = filepath.Base(abs)
		}
	}
	return name
}
