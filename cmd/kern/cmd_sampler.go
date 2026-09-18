package main

import (
	"fmt"
	"strconv"
)

// runRegisterHostSampler implements `kern register-host-sampler`:
// registers (or, with an empty command, unregisters) a host sampler command
// for LLM delegation — the auto chain's host leg runs the command per
// generation (stdin = user prompt, $KERN_SYSTEM_PROMPT = system prompt,
// stdout = reply). key namespaces the registration so several
// sessions/agents/repos coexist. Intended for hosts that do not announce MCP
// sampling (e.g. opencode): the plugin calls this with `opencode run` when
// KERN_HOST_SAMPLER_CMD is set.
func runRegisterHostSampler(rest []string) int {
	f, pos, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	key := f.key
	timeout := ""
	if f.timeoutSet {
		timeout = strconv.Itoa(f.timeout)
	}
	model := f.model
	command := ""
	if len(pos) > 0 {
		command = pos[0]
		// The documented usage is `kern register-host-sampler [command] [--key K]`;
		// the unified parser consumes flags before AND after the command, so
		// both orders work without a re-parse.
	}

	out, err := callTool("kern_register_host_sampler", map[string]any{
		"command": command,
		"key":     key,
		"timeout": timeout,
		"model":   model,
	})
	if err != nil {
		fatal("register-host-sampler: %v", err)
	}
	fmt.Printf("%s\n", out)
	return 0
}
