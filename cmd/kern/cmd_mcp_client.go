package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcpclient"
)

// runMcpClient implements `kern mcp-client`: manage external MCP servers
// and call their tools. Subcommands: add, list, rm, call. Config lives at
// .kern/mcp-servers.json (gitignored via .kern). Distinct from `kern mcp`
// (the MCP server runner).
func runMcpClient(rest []string) {
	if len(rest) == 0 {
		mcpClientHelp()
		return
	}
	switch rest[0] {
	case "add":
		mcpClientAdd(rest[1:])
	case "list":
		mcpClientList(rest[1:])
	case "rm", "remove":
		mcpClientRemove(rest[1:])
	case "call":
		mcpClientCall(rest[1:])
	case "help", "-h", "--help":
		mcpClientHelp()
	default:
		fatalUsage("unknown mcp-client subcommand %q (try: add, list, rm, call)", rest[0])
	}
}

func mcpRoot(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" || args[i] == "-r" {
			if i+1 < len(args) {
				return args[i+1], append(args[:i], args[i+2:]...)
			}
		}
	}
	return ".", args
}

func mcpClientAdd(args []string) {
	root, args := mcpRoot(args)
	if len(args) < 1 {
		fatalUsage("usage: kern mcp-client add <name> --transport stdio|streamable-http [--command C [--arg A]...] [--url U] [--header K=V]... [--env K=V]...")
	}
	name := args[0]
	rest := args[1:]
	var s mcpclient.Server
	s.Name = name
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--transport":
			if i+1 < len(rest) {
				s.Transport = rest[i+1]
				i++
			}
		case "--command":
			if i+1 < len(rest) {
				s.Command = rest[i+1]
				i++
			}
		case "--arg":
			if i+1 < len(rest) {
				s.Args = append(s.Args, rest[i+1])
				i++
			}
		case "--url":
			if i+1 < len(rest) {
				s.URL = rest[i+1]
				i++
			}
		case "--header":
			if i+1 < len(rest) {
				if s.Headers == nil {
					s.Headers = map[string]string{}
				}
				kv := strings.SplitN(rest[i+1], "=", 2)
				if len(kv) == 2 {
					s.Headers[kv[0]] = kv[1]
				}
				i++
			}
		case "--env":
			if i+1 < len(rest) {
				if s.Env == nil {
					s.Env = map[string]string{}
				}
				kv := strings.SplitN(rest[i+1], "=", 2)
				if len(kv) == 2 {
					s.Env[kv[0]] = kv[1]
				}
				i++
			}
		default:
			fatalUsage("unknown mcp-client add flag %q", rest[i])
		}
	}
	if err := s.Validate(); err != nil {
		fatalUsage("%v", err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatalUsage("%v", err)
	}
	servers, err := mcpclient.LoadConfig(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	if _, exists := mcpclient.FindServer(servers, name); exists {
		fatalUsage("server %q already configured", name)
	}
	servers = append(servers, s)
	if err := mcpclient.SaveConfig(absRoot, servers); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	fmt.Printf("added MCP server %q (%s)\n", name, s.Transport)
}

func mcpClientList(args []string) {
	root, _ := mcpRoot(args)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatalUsage("%v", err)
	}
	servers, err := mcpclient.LoadConfig(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	if len(servers) == 0 {
		fmt.Println("no MCP servers configured (use: kern mcp-client add)")
		return
	}
	fmt.Println("configured MCP servers:")
	for _, s := range servers {
		target := s.Command
		if s.Transport == "streamable-http" {
			target = s.URL
		}
		fmt.Printf("  %-16s %-16s %s\n", s.Name, s.Transport, target)
	}
}

func mcpClientRemove(args []string) {
	root, args := mcpRoot(args)
	if len(args) < 1 {
		fatalUsage("usage: kern mcp-client rm <name>")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatalUsage("%v", err)
	}
	servers, err := mcpclient.LoadConfig(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	kept := servers[:0]
	found := false
	for _, s := range servers {
		if s.Name == args[0] {
			found = true
			continue
		}
		kept = append(kept, s)
	}
	if !found {
		fatalUsage("no configured server named %q", args[0])
	}
	if err := mcpclient.SaveConfig(absRoot, kept); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	fmt.Printf("removed MCP server %q\n", args[0])
}

func mcpClientCall(args []string) {
	root, args := mcpRoot(args)
	if len(args) < 2 {
		fatalUsage("usage: kern mcp-client call <server> <tool> '<json-args>'")
	}
	serverName, tool := args[0], args[1]
	var toolArgs map[string]any
	if len(args) > 2 {
		if err := json.Unmarshal([]byte(args[2]), &toolArgs); err != nil {
			fatalUsage("invalid json args: %v", err)
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatalUsage("%v", err)
	}
	servers, err := mcpclient.LoadConfig(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	server, ok := mcpclient.FindServer(servers, serverName)
	if !ok {
		fatalUsage("no configured server named %q (see: kern mcp-client list)", serverName)
	}
	c, err := mcpclient.Dial(context.Background(), &server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	defer c.Close()
	res, err := c.CallTool(context.Background(), tool, toolArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic(exitError{code: 1})
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
}

func mcpClientHelp() {
	fmt.Println("usage: kern mcp-client <subcommand>")
	fmt.Println()
	fmt.Println("Connect to external MCP servers and call their tools as mcp__<server>__<tool>:")
	fmt.Println("  add <name> --transport stdio|streamable-http [--command C [--arg A]... | --url U] [--header K=V]... [--env K=V]...")
	fmt.Println("  list")
	fmt.Println("  rm <name>")
	fmt.Println("  call <server> <tool> '<json-args>'")
	fmt.Println()
	fmt.Println("Config: .kern/mcp-servers.json (gitignored). No server is enabled by default.")
}