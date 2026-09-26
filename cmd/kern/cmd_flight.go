package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/flight"
)

// runFlight surfaces the AI flight recorder (Workflow E observability):
//
//	kern flight list [--root ROOT] [--agent X] [--task T] [--status S] [--json]
//	kern flight show <task-id> [--root ROOT]
//	kern flight tasks [--root ROOT] [--json]
//	kern flight gc [--root ROOT] [--keep-tasks N] [--older-than DURATION]
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
	f, pos, err := parseFlags(args)
	if err != nil {
		return 2
	}
	root := f.root
	agent := f.agent
	task := f.task
	status := f.statusFilter
	if status == "" && len(pos) > 0 {
		// --status is the dual-form bool/string flag: `--status ok` lands the
		// value in the first positional (the next token is never consumed, so
		// `kern index --status <root>` keeps its root); the inline
		// `--status=ok` form lands in statusFilter directly.
		status = pos[0]
	}
	asJSON := f.json
	recs := flight.New(root).Filter(agent, task, status)
	if len(recs) == 0 {
		if asJSON {
			fmt.Println("[]")
		} else {
			fmt.Println("no flight records (root: " + root + ")")
		}
		return 0
	}
	for _, rec := range recs {
		if asJSON {
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
	f, pos, err := parseFlags(args)
	if err != nil {
		return 2
	}
	root := f.root
	taskID := ""
	if len(pos) > 0 {
		taskID = pos[0]
	}
	if taskID == "" {
		fmt.Fprintln(os.Stderr, "usage: kern flight show <task-id> [--root ROOT]")
		return 2
	}
	fmt.Print(flight.New(root).TrailText(taskID))
	return 0
}

// runFlightTasks lists every task with a flight trail, most recently active
// first — the task-id to trail linkage view.
func runFlightTasks(args []string) int {
	f, _, err := parseFlags(args)
	if err != nil {
		return 2
	}
	root := f.root
	asJSON := f.json
	sums, err := flight.New(root).Tasks()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern flight tasks: %v\n", err)
		return 1
	}
	for _, s := range sums {
		if asJSON {
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
	f, _, err := parseFlags(args)
	if err != nil {
		return 2
	}
	root := f.root
	keepTasks := f.keepTasks
	olderThan := f.olderThan
	var cutoff time.Duration
	if olderThan != "" {
		d, err := parseDurationDays(olderThan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kern flight gc: --older-than %q: %v\n", olderThan, err)
			return 2
		}
		cutoff = d
	}
	rec := flight.New(root)
	deleted, err := rec.GC(keepTasks, cutoff)
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
		deleted, len(after), keepTasks, olderThan)
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
