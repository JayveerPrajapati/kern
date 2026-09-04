package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/gitleaks"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/jscpd"
	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/blueprint/policy"
	"github.com/JayveerPrajapati/kern/internal/blueprint/service"
	"github.com/JayveerPrajapati/kern/internal/blueprint/watcher"
)

// runWatch implements `blueprint watch` — the continuous watcher daemon.
// It polls the repo for file changes and emits debounced event batches to
// stdout, validating each batch against the selected policy checks and
// printing advisory findings to stderr. This is advisory post-write feedback
// (spec Rule 2 / Phase 10 lines 1311-1314), NOT a pre-write firewall.
func runWatch(args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoRoot := fs.String("repo", "", "repository root (default: current directory)")
	interval := fs.Duration("interval", 500*time.Millisecond, "polling interval")
	debounce := fs.Duration("debounce", 1*time.Second, "quiet period before emitting events")
	strict := fs.Bool("strict", false, "strict mode: exit non-zero on any violation")
	policyFlag := fs.String("policy", "", "comma-separated policies to run (architecture,secrets)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0 // -h/--help: usage already printed; exit clean
		}
		return 2
	}

	root := *repoRoot
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: cannot determine working directory: %v\n", err)
			return 2
		}
		root = cwd
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: invalid repository path %q: %v\n", root, err)
		return 2
	}

	// Build the check list from --policy. Unknown policy names are a usage
	// error (exit 2), matching the tool/runtime error contract.
	policyArg := *policyFlag
	client, err := kern.NewKernClient()
	if err != nil {
		// M6: watch is an advisory tool — a missing kern binary degrades to a
		// local-only watcher instead of a hard failure. Kern-backed policies
		// (architecture, secrets) are skipped; the warning makes the reduced
		// capability visible, never silent.
		fmt.Fprintln(os.Stderr, "blueprint: warning: kern binary not found; architecture checks will be skipped")
		client = nil
		policyArg = stripKernPolicies(policyArg)
	}
	var checks []service.Check
	if client == nil && policyArg == "" {
		// Degraded mode with nothing kern-backed left to run: keep watching
		// files but validate nothing (advisory-only, no architecture checks).
		checks = []service.Check{}
	} else {
		checks, err = buildWatchChecks(policyArg, client)
		if err != nil {
			fmt.Fprintf(os.Stderr, "blueprint: %v\n", err)
			return 2
		}
	}

	// Load the policy engine (suppressions, mode warn/off, owner routing,
	// source overrides) exactly like `blueprint check`. A missing config file
	// falls back to the default policy (policy.Load returns DefaultConfig with
	// no error); a malformed config is a hard error, same as the CLI.
	policyCfg, err := policy.Load(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: invalid configuration: %v\n", err)
		return 2
	}
	policyEngine := policy.NewEngine(policyCfg.Policy)

	cfg := watcher.DefaultConfig(absRoot)
	cfg.Interval = *interval
	cfg.Debounce = *debounce

	w := watcher.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl-C / SIGTERM for clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nblueprint: shutting down watcher...")
		cancel()
	}()

	if err := w.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "blueprint: watcher start failed: %v\n", err)
		return 2
	}

	fmt.Fprintf(os.Stderr, "blueprint: watching %s (poll=%s debounce=%s)\n", absRoot, cfg.Interval, cfg.Debounce)
	fmt.Fprintln(os.Stderr, "Press Ctrl-C to stop. This is advisory feedback, not a pre-write firewall.")

	sessionHadBlock := false
	for {
		select {
		case <-ctx.Done():
			w.Stop()
			if *strict && sessionHadBlock {
				fmt.Fprintln(os.Stderr, "blueprint: strict mode — violations detected during watch session")
				return 1
			}
			return 0
		case events, ok := <-w.Events():
			if !ok {
				return 0
			}
			for _, e := range events {
				fmt.Printf("[%s] %s\n", e.Time.Format("15:04:05"), e)
			}
			// Validate the batch and surface findings as advisory output.
			hadBlock, findings := validateBatch(ctx, absRoot, checks, policyEngine, events)
			emitWatchFindings(findings)
			sessionHadBlock = sessionHadBlock || hadBlock
		}
	}
}

// stripKernPolicies removes the kern-backed policies (architecture, secrets)
// from a comma-separated --policy value, so watch can degrade gracefully when
// the kern binary is missing (M6: advisory tool, not a hard dependency). The
// default (empty) value maps to the default set minus kern-backed policies;
// the returned value is always safe to pass to buildWatchChecks with a nil
// client (it never re-triggers the default architecture,secrets expansion).
func stripKernPolicies(policyValue string) string {
	names := strings.Split(policyValue, ",")
	kept := make([]string, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || name == "architecture" || name == "secrets" {
			continue
		}
		kept = append(kept, name)
	}
	return strings.Join(kept, ",")
}

// buildWatchChecks maps a comma-separated --policy value to the check list
// that `blueprint check` runs for those policies. An empty policy means the
// default set (architecture,secrets). Recognized policy names:
//
//	architecture  → kern.NewArchitectureCheck (requires a kern client)
//	secrets       → gitleaks.NewCheck (requires a kern client; falls back to
//	                 kern.NewSecretCheck when gitleaks is absent)
//	duplication   → jscpd.NewCheck (falls back to duplication.NewCheck when
//	                 jscpd is absent; a nil client constructs fine and Run
//	                 degrades to a WARN fallback finding)
//
// The client may be nil only when no kern-backed policy is selected (so the
// helper is unit-testable without a running daemon).
func buildWatchChecks(policy string, client *kern.KernClient) ([]service.Check, error) {
	names := strings.Split(policy, ",")
	if len(names) == 1 && strings.TrimSpace(names[0]) == "" {
		names = []string{"architecture", "secrets"}
	}
	checks := make([]service.Check, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		switch name {
		case "architecture":
			if client == nil {
				return nil, fmt.Errorf("policy %q requires a kern client", name)
			}
			checks = append(checks, kern.NewArchitectureCheck(client))
		case "secrets":
			if client == nil {
				return nil, fmt.Errorf("policy %q requires a kern client", name)
			}
			checks = append(checks, gitleaks.NewCheck(client))
		case "duplication":
			checks = append(checks, jscpd.NewCheck(client))
		default:
			return nil, fmt.Errorf("unknown policy %q (recognized: architecture,secrets,duplication)", name)
		}
	}
	return checks, nil
}

// validateBatch runs the canonical validation pipeline (spec Rule 1) over one
// debounced event batch and reports whether any finding has SeverityBlock,
// plus all findings. It prints nothing; the caller emits the output.
//
// The ChangeRequest is built with one FileChange per distinct event path
// (Op=OpEdit), sourced as SourceWatch with Operation=OpCommit, and validated
// through service.New(checks, WithPolicy(engine)): suppressions, mode
// warn/off, owner stamping, and source overrides are applied exactly as in
// `blueprint check`. The verdict is advisory.
func validateBatch(ctx context.Context, root string, checks []service.Check, engine *policy.Engine, events []watcher.Event) (hadBlock bool, findings []domain.Finding) {
	if len(events) == 0 {
		return false, nil
	}

	seen := make(map[string]bool, len(events))
	files := make([]domain.FileChange, 0, len(events))
	for _, e := range events {
		if e.Path == "" || seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		files = append(files, domain.FileChange{Path: e.Path, Op: domain.OpEdit})
	}

	req := domain.ChangeRequest{
		RepositoryRoot: root,
		Source:         domain.SourceWatch,
		Operation:      domain.OpCommit,
		Files:          files,
	}

	svc := service.New(checks, service.WithPolicy(engine))
	result := svc.Validate(ctx, req)

	for _, f := range result.Findings {
		if f.Severity == domain.SeverityBlock {
			hadBlock = true
			break
		}
	}
	return hadBlock, result.Findings
}

// emitWatchFindings prints findings to stderr in a compact terminal style,
// mirroring the per-finding line shape of check.go's emitText:
//
//   - BLOCK [rule-id] file:line — message
func emitWatchFindings(findings []domain.Finding) {
	for _, f := range findings {
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		fmt.Fprintf(os.Stderr, "• %s [%s] %s — %s\n", strings.ToUpper(string(f.Severity)), f.RuleID, loc, f.Message)
	}
}
