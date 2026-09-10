package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/flight"
)

// runFlight surfaces the AI flight recorder (Workflow E observability):
//
//	kern flight list [--root DIR] [--agent X] [--task T] [--status S] [--json]
//	kern flight show <task-id> [--root DIR]
//	kern flight tasks [--root DIR] [--json]
//	kern flight gc [--root DIR] [--keep-tasks N] [--older-than DURATION]
//
// Records are written by the autonomous loop / TaskService into <root>/.kern/flight.
func runFlight(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "kern flight: subcommand required (list|show|tasks|gc)")
		return 1
	}
	switch args[0] {
	case "list":
		return runFlightList(args[1:])
	case "show":
		return runFlightShow(args[1:])
	case "tasks":
		return runFlightTasks(args[1:])
	case "gc":
		return runFlightGC(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "kern flight: unknown subcommand %q (list|show|tasks|gc)\n", args[0])
		return 1
	}
}

func runFlightList(args []string) int {
	fs := flag.NewFlagSet("flight list", flag.ContinueOnError)
	root := fs.String("root", ".", "project root holding .kern/flight")
	agent := fs.String("agent", "", "filter by agent id")
	task := fs.String("task", "", "filter by task id")
	status := fs.String("status", "", "filter by record status (ok|error|blocked|denied)")
	asJSON := fs.Bool("json", false, "emit JSON lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	recs := flight.New(*root).Filter(*agent, *task, *status)
	for _, rec := range recs {
		if *asJSON {
			data, err := json.Marshal(rec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "kern flight list: %v\n", err)
				return 1
			}
			fmt.Println(string(data))
			continue
		}
		fmt.Printf("%s %-8s %-12s %-12s %s\n",
			rec.Timestamp.Format("2006-01-02T15:04:05Z07:00"), rec.Status, rec.AgentID, rec.TaskID, rec.Action)
	}
	return 0
}

func runFlightShow(args []string) int {
	fs := flag.NewFlagSet("flight show", flag.ContinueOnError)
	root := fs.String("root", ".", "project root holding .kern/flight")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	taskID := fs.Arg(0)
	if taskID == "" {
		fmt.Fprintln(os.Stderr, "usage: kern flight show <task-id> [--root DIR]")
		return 2
	}
	fmt.Print(flight.New(*root).TrailText(taskID))
	return 0
}

// runFlightTasks lists every task with a flight trail, most recently active
// first — the task-id to trail linkage view.
func runFlightTasks(args []string) int {
	fs := flag.NewFlagSet("flight tasks", flag.ContinueOnError)
	root := fs.String("root", ".", "project root holding .kern/flight")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	sums, err := flight.New(*root).Tasks()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern flight tasks: %v\n", err)
		return 1
	}
	for _, s := range sums {
		if *asJSON {
			data, err := json.Marshal(s)
			if err != nil {
				fmt.Fprintf(os.Stderr, "kern flight tasks: %v\n", err)
				return 1
			}
			fmt.Println(string(data))
			continue
		}
		name := s.TaskID
		if name == "" {
			name = "(untracked)"
		}
		fmt.Printf("%-14s %-24s %-24s %5d recs  %s\n",
			name, s.First.Format("2006-01-02T15:04:05Z07:00"), s.Last.Format("2006-01-02T15:04:05Z07:00"),
			s.Count, strings.Join(s.Statuses, ","))
	}
	return 0
}

// runFlightGC enforces flight-store retention. Retention is per task trail
// (never partially truncated), keeping the keepTasks most recently active
// tasks plus any task active within olderThan.
func runFlightGC(args []string) int {
	fs := flag.NewFlagSet("flight gc", flag.ContinueOnError)
	root := fs.String("root", ".", "project root holding .kern/flight")
	keepTasks := fs.Int("keep-tasks", 20, "retain the N most recently active task trails (0 disables)")
	olderThan := fs.String("older-than", "", "retain tasks active within this duration (e.g. 30d, 720h)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	var cutoff time.Duration
	if *olderThan != "" {
		d, err := parseDurationDays(*olderThan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kern flight gc: --older-than %q: %v\n", *olderThan, err)
			return 2
		}
		cutoff = d
	}
	rec := flight.New(*root)
	deleted, err := rec.GC(*keepTasks, cutoff)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern flight gc: %v\n", err)
		return 1
	}
	after, err := rec.Tasks()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern flight gc: %v\n", err)
		return 1
	}
	fmt.Printf("kern flight gc: deleted %d record(s); %d task trail(s) remain (keep-tasks=%d, older-than=%s)\n",
		deleted, len(after), *keepTasks, *olderThan)
	return 0
}

// parseDurationDays parses a Go duration, additionally accepting a bare
// "Nd" suffix (e.g. "30d" -> 720h) so retention cutoffs read naturally.
func parseDurationDays(s string) (time.Duration, error) {
	if len(s) > 2 && s[len(s)-1] == 'd' {
		if n, err := strconv.Atoi(s[:len(s)-1]); err == nil {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	return time.ParseDuration(s)
}
