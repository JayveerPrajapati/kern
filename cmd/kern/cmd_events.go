package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/lock"
	"github.com/JayveerPrajapati/kern/internal/relay"
)

// runEvents surfaces the cross-process event relay:
//
//	kern events serve              own the socket and fan the bus out
//	kern events watch              stream events (optionally filtered)
//	kern events emit <kind>        publish one event from a script
func runEvents(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "kern events: subcommand required (serve|watch|emit)")
		return 1
	}
	switch args[0] {
	case "serve":
		return runEventsServe(args[1:])
	case "watch":
		return runEventsWatch(args[1:])
	case "emit":
		return runEventsEmit(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "kern events: unknown subcommand %q (serve|watch|emit)\n", args[0])
		return 1
	}
}

type eventsFlags struct {
	root    string
	kinds   string
	json    bool
	subject string
	payload []string
}

func parseEventsFlags(args []string) eventsFlags {
	var f eventsFlags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root":
			if i+1 < len(args) {
				i++
				f.root = args[i]
			}
		case "--kind", "--kinds":
			if i+1 < len(args) {
				i++
				f.kinds = args[i]
			}
		case "--json":
			f.json = true
		case "--subject":
			if i+1 < len(args) {
				i++
				f.subject = args[i]
			}
		case "--payload":
			if i+1 < len(args) {
				i++
				f.payload = append(f.payload, args[i])
			}
		}
	}
	if f.root == "" {
		f.root = "."
	}
	return f
}

func runEventsServe(args []string) int {
	f := parseEventsFlags(args)
	bus := eventbus.New()
	srv, err := relay.Start(f.root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern events serve: %v\n", err)
		return 1
	}
	defer srv.Close()
	bus.Subscribe("", srv.Broadcast) // "" = every kind
	srv.SetPublisher(bus.Publish)
	// Surface in-process lock contention too (Acquire calls made by this
	// process); contention in OTHER processes emits via their own CLI path.
	lock.ContentionHook = func(scope string, holderPID int) {
		bus.Publish(eventbus.Event{
			Kind:    eventbus.LockContended,
			Source:  "cli",
			Subject: scope,
			Payload: map[string]any{"scope": scope, "holder_pid": holderPID},
		})
	}
	fmt.Printf("kern events relay listening on %s (ctrl-c to stop)\n", srv.Path())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	return 0
}

func runEventsWatch(args []string) int {
	f := parseEventsFlags(args)
	var filter map[string]bool
	if f.kinds != "" {
		filter = make(map[string]bool)
		for _, k := range strings.Split(f.kinds, ",") {
			if k = strings.TrimSpace(k); k != "" {
				filter[k] = true
			}
		}
	}
	c, err := relay.Dial(f.root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern events watch: no relay at %s (start `kern events serve` or kern-server)\n", relay.SocketPath(f.root))
		return 1
	}
	defer c.Close()
	for {
		e, err := c.Next()
		if err != nil {
			return 0
		}
		if filter != nil && !filter[string(e.Kind)] {
			continue
		}
		if f.json {
			if b, err := json.Marshal(e); err == nil {
				fmt.Println(string(b))
			}
			continue
		}
		fmt.Printf("[%s] %-22s %-8s %s", e.ID, e.Kind, e.Source, e.Subject)
		if p, ok := e.Payload.(map[string]any); ok && len(p) > 0 {
			fmt.Printf("  %s", payloadJSON(p))
		}
		fmt.Println()
	}
}

func runEventsEmit(args []string) int {
	f := parseEventsFlags(args)
	positional := nonFlagArgs(args)
	if len(positional) == 0 {
		fmt.Fprintln(os.Stderr, "kern events emit: kind required (kern events emit <kind> [--subject S] [--payload k=v])")
		return 1
	}
	kind := positional[0]
	payload := make(map[string]any)
	for _, kv := range f.payload {
		if k, v, ok := strings.Cut(kv, "="); ok {
			payload[k] = v
		}
	}
	c, err := relay.Dial(f.root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern events emit: no relay at %s (start `kern events serve` or kern-server)\n", relay.SocketPath(f.root))
		return 1
	}
	defer c.Close()
	if err := c.Emit(eventbus.Event{
		Kind:    eventbus.Kind(kind),
		Source:  "cli",
		Subject: f.subject,
		Payload: payload,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "kern events emit: %v\n", err)
		return 1
	}
	return 0
}

// nonFlagArgs returns positional args (values of --flags excluded). The
// emit parser already consumed flag values; this rewalks them because
// emit accepts exactly one positional (the kind).
func nonFlagArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root", "--kind", "--kinds", "--subject", "--payload":
			i++ // skip value
		case "--json":
		default:
			out = append(out, args[i])
		}
	}
	return out
}

func payloadJSON(p map[string]any) string {
	if len(p) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(p))
	for k, v := range p {
		parts = append(parts, fmt.Sprintf("%q:%q", k, fmt.Sprint(v)))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// emitLockEvent publishes a lock lifecycle event through the relay when
// one is running. Best-effort: without a relay owner the event is
// dropped silently — lock semantics never depend on the relay.
func emitLockEvent(root, kind, scope string, payload map[string]any) {
	c, err := relay.Dial(root)
	if err != nil {
		return
	}
	defer c.Close()
	_ = c.Emit(eventbus.Event{
		Kind:    eventbus.Kind(kind),
		Source:  "cli",
		Subject: scope,
		Payload: payload,
	})
}
