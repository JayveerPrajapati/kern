// Command kern-server starts a local, stdlib-only HTTP server exposing the project's digital-twin
// data as JSON and a minimal server-rendered HTML dashboard. No external
// dependencies are required.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/enterprise"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	kversion "github.com/JayveerPrajapati/kern/internal/version"
	"github.com/JayveerPrajapati/kern/internal/web"
	"github.com/JayveerPrajapati/kern/internal/webhook"
)

// version is the build-stamped release version, initialized from the shared
// internal/version.Version so every kern binary reports the same value.
// It starts as the literal "dev" (not a copy of kversion.Version) because
// the legacy -ldflags "-X main.version=..." only rewrites a variable whose
// initializer is a compile-time constant: a runtime copy from another global
// aliases the read and silently defeats -X. When unstamped, init() adopts
// the shared internal/version.Version (default "dev", or the newer
// "-X github.com/JayveerPrajapati/kern/internal/version.Version=..." form;
// see .github/workflows/release.yml and homebrew/kern.rb).
var version = "dev"

func init() {
	version = kversion.Adopt(version)
}

func main() {
	root := flag.String("root", ".", "project root to serve (single-project mode)")
	addr := flag.String("addr", "127.0.0.1:8090", "listen address")
	enterprise := flag.Bool("enterprise", false, "enable multi-project enterprise mode")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("kern-server %s\n", version)
		os.Exit(0)
	}

	if *enterprise {
		runEnterprise(*addr)
		return
	}

	app, err := web.New(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern-server: %v\n", err)
		os.Exit(1)
	}

	// Wire the app's event bus to outbound webhooks configured via
	// KERN_WEBHOOKS="name=url,name2=url2". A bare URL (no "name=") uses the
	// URL's host as the hook name.
	hooks := webhook.New()
	for _, raw := range strings.Split(os.Getenv("KERN_WEBHOOKS"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name, u := "", raw
		if i := strings.Index(raw, "="); i > 0 {
			name, u = strings.TrimSpace(raw[:i]), strings.TrimSpace(raw[i+1:])
		} else if parsed, perr := url.Parse(raw); perr == nil && parsed.Host != "" {
			name = parsed.Host
		} else {
			name = raw
		}
		if err := hooks.Add(name, u); err != nil {
			log.Printf("kern-server: skipping webhook %q: %v", name, err)
		}
	}
	if n := len(hooks.URLs()); n > 0 {
		log.Printf("kern-server: registered %d webhook(s)", n)
	}
	// Deliver webhooks asynchronously in a goroutine so a slow (up to 5s) or
	// unreachable webhook URL never blocks the event bus or the triggering HTTP
	// handler. Publish returns immediately; delivery runs in the background and
	// failures are logged without blocking the caller.
	unsub := app.Bus().Subscribe("", func(ev eventbus.Event) {
		go func() {
			if errs := hooks.Deliver(ev); len(errs) > 0 {
				for u, e := range errs {
					log.Printf("kern-server: webhook %s failed: %v", u, e)
				}
			}
		}()
	})
	defer unsub()

	fmt.Printf("kern-server listening on %s\n", *addr)
	serve(*addr, app)
}

// runEnterprise starts kern-server in multi-project enterprise mode. Projects
// are read from KERN_ENTERPRISE_PROJECTS="name=path,name2=path2". A bare path
// (no "name=") uses the path as the project name.
func runEnterprise(addr string) {
	srv := enterprise.New()
	projects := os.Getenv("KERN_ENTERPRISE_PROJECTS")
	for _, raw := range strings.Split(projects, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name, path := raw, raw
		if i := strings.Index(raw, "="); i > 0 {
			name = strings.TrimSpace(raw[:i])
			path = strings.TrimSpace(raw[i+1:])
		}
		if err := srv.Register(name, path); err != nil {
			log.Printf("kern-server: skipping project %q: %v", name, err)
		}
	}
	if len(srv.Projects()) == 0 {
		fmt.Fprintln(os.Stderr, "kern-server: enterprise mode requires KERN_ENTERPRISE_PROJECTS=name=path,...")
		os.Exit(1)
	}
	// Fail-closed: enterprise mode serves the full digital twin of every project
	// plus the shared org audit log and policies, so it refuses to start without
	// a bearer token configured.
	if os.Getenv("KERN_AUTH_TOKEN") == "" {
		fmt.Fprintln(os.Stderr, "kern-server: KERN_AUTH_TOKEN must be set for enterprise mode")
		os.Exit(1)
	}
	fmt.Printf("kern-server (enterprise) listening on %s with %d project(s)\n", addr, len(srv.Projects()))
	serve(addr, srv)
}

// serve runs an http.Server with graceful shutdown. On SIGINT/SIGTERM it drains
// in-flight requests for up to 10s before returning, so a control-plane server
// (REST + webhooks + long-running /v1/loop) is not hard-killed mid-request.
func serve(addr string, handler http.Handler) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: addr, Handler: handler}
	go func() {
		<-ctx.Done()
		stop()
		log.Printf("kern-server: shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("kern-server: shutdown error: %v", err)
		}
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "kern-server: %v\n", err)
		os.Exit(1)
	}
}
