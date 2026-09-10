package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// runRuntime surfaces the production-intelligence layer:
//
//	kern runtime status [--root DIR] [--json]
//	kern runtime drift [--root DIR] [--json]
//
// status is also the adapter discovery wizard: it reports which runtime
// source is wired (live adapter via env/config, or the local snapshot) and,
// when none is, lists every available adapter with the exact variable that
// enables it. drift compares the routes observed at runtime against the
// routes declared in code (framework entry points in the index).
func runRuntime(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "kern runtime: subcommand required (status|drift)")
		return 1
	}
	switch args[0] {
	case "status":
		return runRuntimeStatus(args[1:])
	case "drift":
		return runRuntimeDrift(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "kern runtime: unknown subcommand %q (status|drift)\n", args[0])
		return 1
	}
}

// runRuntimeDrift compares runtime routes (metric route/path/endpoint
// labels) against code-declared routes (index framework entry points),
// template-aware: "/users/:id" matches "/users/42".
func runRuntimeDrift(args []string) int {
	fs := flag.NewFlagSet("runtime drift", flag.ContinueOnError)
	root := fs.String("root", ".", "project root")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	src := runtime.LoadSource(*root)
	ix, err := loadOrBuild(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern runtime drift: %v\n", err)
		return 1
	}
	var codeRoutes []string
	for _, s := range ix.Symbols {
		// Route presence is the signal, not Kind: Go entry points persist as
		// kind "entry" while foreign-language framework handlers persist as
		// "func"/"method" with a Route field.
		if s.Route != "" {
			codeRoutes = append(codeRoutes, s.Route)
		}
	}
	rep := runtime.Drift(runtime.Routes(src), codeRoutes)
	if *asJSON {
		printJSON(rep)
		return 0
	}
	fmt.Printf("runtime drift: %d code routes matched · %d runtime-only · %d code-only\n",
		rep.Matched, len(rep.ProdOnly), len(rep.CodeOnly))
	for _, r := range rep.ProdOnly {
		fmt.Printf("  PROD-ONLY  %-40s %s (%d events, %.1f%% err)\n", r.Route, r.Service, r.Events, r.ErrorRate)
	}
	for _, c := range rep.CodeOnly {
		fmt.Printf("  CODE-ONLY  %s\n", c)
	}
	return 0
}

func runRuntimeStatus(args []string) int {
	fs := flag.NewFlagSet("runtime status", flag.ContinueOnError)
	root := fs.String("root", ".", "project root")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	src := runtime.LoadSource(*root)
	if *asJSON {
		printJSON(runtime.StatusSnapshot(src))
		return 0
	}
	if src == nil {
		fmt.Println("runtime: no source configured")
		fmt.Println()
		fmt.Println("enable one of:")
		fmt.Println("  KERN_PROMETHEUS_URL=<query url>          live Prometheus poller (e.g. http://prometheus:9090/api/v1/query?query=up)")
		fmt.Println("  KERN_OTEL_URL=<otlp http url>            live OpenTelemetry poller")
		fmt.Println("  KERN_K8S_API=<api server url>            live Kubernetes poller (KERN_K8S_TOKEN, KERN_K8S_NAMESPACE)")
		fmt.Println("  .kern/runtime.json                       local offline snapshot")
		fmt.Printf("\npoll interval: KERN_POLL_INTERVAL (default 30s)\n")
		fmt.Printf("runtime-aware review: kern review --runtime\n")
		return 0
	}
	profiles := runtime.ServiceProfiles(src)
	events, errors := 0, 0
	for _, p := range profiles {
		events += p.Events
		errors += p.Errors
	}
	fmt.Printf("runtime source: %s (poll %s)\n", src.Name(), runtime.PollInterval())
	fmt.Printf("events: %d · errors: %d · deployments: %d · commits: %d\n",
		events, errors, len(src.Deployments("")), len(src.Commits()))
	if len(profiles) == 0 {
		fmt.Println("services: (none — the source has no telemetry yet)")
		return 0
	}
	lines := make([]string, 0, len(profiles))
	for _, p := range profiles {
		name := p.Name
		if name == "" {
			name = "(untagged)"
		}
		lines = append(lines, fmt.Sprintf("%s (%d events, %.1f%% err)", name, p.Events, p.ErrorRate))
	}
	sort.Strings(lines)
	fmt.Println("services: " + strings.Join(lines, " · "))
	return 0
}
