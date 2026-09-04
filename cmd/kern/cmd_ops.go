package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cockpit"
	"github.com/JayveerPrajapati/kern/internal/loop"
)

// runOps executes the KernOps governed autonomous engineering cockpit.
func runOps(args []string) int {
	if len(args) > 0 && args[0] == "triage" {
		return runOpsTriage(args[1:])
	}

	fs := flag.NewFlagSet("ops", flag.ContinueOnError)

	repoFlag := fs.String("repo", ".", "Repository root path")
	levelFlag := fs.String("level", "L3", "Autonomy level: L0, L1, L2, L3, L4, L5")
	nonInteractive := fs.Bool("non-interactive", false, "Run in headless / CI mode without interactive TUI")
	autoApprove := fs.Bool("auto-approve", false, "Automatically grant approvals for CI runs")
	jsonOut := fs.Bool("json", false, "Emit final execution state as JSON")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: kern ops [flags] <task-intent>\n")
		fmt.Fprintf(os.Stderr, "Example: kern ops \"Implement JWT rotation in auth middleware\"\n\n")
		fs.PrintDefaults()
		return 2
	}

	intent := strings.Join(rest, " ")
	absRepo, err := filepath.Abs(*repoFlag)
	if err != nil {
		absRepo = *repoFlag
	}

	autonomyLevel, err := loop.ParseLevel(*levelFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern: invalid autonomy level %q: %v\n", *levelFlag, err)
		return 2
	}

	cfg := cockpit.RunnerConfig{
		RepoRoot:       absRepo,
		TaskPrompt:     intent,
		AutonomyLevel:  autonomyLevel,
		NonInteractive: *nonInteractive || *jsonOut,
		AutoApprove:    *autoApprove,
	}

	if *jsonOut {
		cfg.Output = os.Stderr
	}

	runner := cockpit.NewRunner(cfg)
	state, runErr := runner.Run(context.Background())

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(state)
	}

	if runErr != nil || !state.Success {
		return 1
	}
	return 0
}

func runOpsTriage(args []string) int {
	fs := flag.NewFlagSet("ops triage", flag.ContinueOnError)

	logFlag := fs.String("log", "", "Path to raw log / stack trace file, or '-' for stdin")
	repoFlag := fs.String("repo", ".", "Repository root path")
	nonInteractive := fs.Bool("non-interactive", false, "Run in headless mode")
	autoApprove := fs.Bool("auto-approve", false, "Automatically grant approvals for CI runs")
	jsonOut := fs.Bool("json", false, "Emit triage report as JSON")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	var rawLog string
	if *logFlag == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kern ops triage: cannot read stdin: %v\n", err)
			return 2
		}
		rawLog = string(b)
	} else if *logFlag != "" {
		b, err := os.ReadFile(*logFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kern ops triage: cannot read log file %q: %v\n", *logFlag, err)
			return 2
		}
		rawLog = string(b)
	} else if len(fs.Args()) > 0 {
		rawLog = strings.Join(fs.Args(), " ")
	} else {
		// Check if piped stdin has data
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			b, _ := io.ReadAll(os.Stdin)
			rawLog = string(b)
		}
	}

	if strings.TrimSpace(rawLog) == "" {
		fmt.Fprintf(os.Stderr, "Usage: kern ops triage --log <path-or-stdin> [flags]\n")
		fs.PrintDefaults()
		return 2
	}

	absRepo, err := filepath.Abs(*repoFlag)
	if err != nil {
		absRepo = *repoFlag
	}

	cfg := cockpit.TriageConfig{
		RepoRoot:       absRepo,
		RawLog:         rawLog,
		NonInteractive: *nonInteractive || *jsonOut,
		AutoApprove:    *autoApprove,
	}
	if *jsonOut {
		cfg.Output = os.Stderr
	}

	report, runErr := cockpit.RunTriage(context.Background(), cfg)
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	}

	if runErr != nil || !report.Success {
		return 1
	}
	return 0
}

