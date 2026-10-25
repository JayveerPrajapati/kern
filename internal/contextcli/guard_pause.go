package contextcli

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cliflags"
	"github.com/JayveerPrajapati/kern/internal/guardpause"
)

type pauseFlags struct {
	scope    string
	duration string
	reason   string
}

func parsePauseFlags(args []string) (pauseFlags, []string, error) {
	var f pauseFlags
	f.scope = "repo"
	p := cliflags.NewParser(args)
	for {
		name, inline, hasInline, ok := p.Next()
		if !ok {
			break
		}
		switch name {
		case "--scope":
			p.SetStr(&f.scope, inline, hasInline)
		case "--duration":
			p.SetStr(&f.duration, inline, hasInline)
		case "--reason":
			p.SetStr(&f.reason, inline, hasInline)
		case "--help", "-h":
		default:
			p.Unknown(name)
		}
	}
	return f, p.Rest, p.Err()
}

func pauseRoot(args []string, usage string) string {
	if len(args) > 1 {
		fatalUsage("%s", usage)
	}
	if len(args) == 0 || args[0] == "" {
		return "."
	}
	return args[0]
}

func runGuardPause(args []string) {
	f, rest, err := parsePauseFlags(args)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := pauseRoot(rest, "usage: kern guard pause [root] [--scope repo|bash-steering] [--duration 30m] --reason TEXT")
	if !guardpause.ValidScope(f.scope) {
		fatalUsage("guard pause: --scope must be repo or bash-steering")
	}
	if strings.TrimSpace(f.reason) == "" {
		fatalUsage("guard pause: --reason is required")
	}
	p, err := guardpause.Create(root, f.duration, f.reason, f.scope)
	if err != nil {
		fatal("guard pause: %v", err)
	}
	fmt.Printf("%s guard steering paused until %s (exec governance and the ask-protocol remain enforced)\n", f.scope, p.Expires)
}

func runGuardPaused(args []string) {
	scope := guardpause.ScopeRepo
	if len(args) > 1 && guardpause.ValidScope(args[1]) {
		scope = args[1]
	}
	root := pauseRoot(args, "usage: kern guard paused [root] [repo|bash-steering]")
	_, ok, err := guardpause.ActiveScoped(root, scope)
	if err != nil {
		fatal("guard paused: %v", err)
	}
	if !ok {
		fmt.Println("inactive")
		return
	}
	fmt.Println("active")
}

func runGuardResume(args []string) {
	root := pauseRoot(args, "usage: kern guard resume [root]")
	removed, err := guardpause.Resume(root)
	if err != nil {
		fatal("guard resume: %v", err)
	}
	if !removed {
		fmt.Println("no active repo pause")
		return
	}
	fmt.Println("repo guard steering resumed")
}
