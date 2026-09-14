package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type completionCmd struct {
	Name string
	Help string
}

var commandListProvider func() []completionCmd

func init() {
	commandListProvider = func() []completionCmd {
		var list []completionCmd
		for name, entry := range commandTable {
			if strings.HasPrefix(name, "-") {
				continue
			}
			list = append(list, completionCmd{Name: name, Help: entry.help})
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].Name < list[j].Name
		})
		return list
	}
}

func getCompletionCommands() []completionCmd {
	if commandListProvider != nil {
		return commandListProvider()
	}
	return nil
}

func generateBashCompletion(w io.Writer) {
	cmds := getCompletionCommands()
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	_, _ = fmt.Fprintf(w, `# bash completion for kern
_kern() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    if [ "$COMP_CWORD" -eq 1 ]; then
        local commands="%s"
        COMPREPLY=( $(compgen -W "${commands}" -- "$cur") )
        return 0
    fi
}
complete -F _kern kern
`, strings.Join(names, " "))
}

func generateZshCompletion(w io.Writer) {
	cmds := getCompletionCommands()
	var b strings.Builder
	for _, c := range cmds {
		help := strings.ReplaceAll(c.Help, "'", `'\''`)
		if help == "" {
			help = c.Name
		}
		b.WriteString(fmt.Sprintf("        '%s:%s'\n", c.Name, help))
	}
	_, _ = fmt.Fprintf(w, `#compdef kern
_kern() {
    local -a commands
    commands=(
%s    )
    _describe -t commands 'kern command' commands
}
_kern "$@"
`, b.String())
}

func generateFishCompletion(w io.Writer) {
	cmds := getCompletionCommands()
	var b strings.Builder
	for _, c := range cmds {
		help := strings.ReplaceAll(c.Help, `"`, `\"`)
		if help == "" {
			help = c.Name
		}
		b.WriteString(fmt.Sprintf("complete -c kern -n \"__fish_use_subcommand\" -a \"%s\" -d \"%s\"\n", c.Name, help))
	}
	_, _ = fmt.Fprintf(w, `# fish completion for kern
complete -c kern -f
%s`, b.String())
}

func runCompletion(rest []string) int {
	if len(rest) < 1 || rest[0] == "--help" || rest[0] == "-h" {
		fmt.Print(`usage: kern completion <bash|zsh|fish>

Generate shell completion scripts for kern.

To load completions:

Bash:
  $ source <(kern completion bash)
  # To load completions for each session, execute once:
  # Linux:
  $ kern completion bash > /etc/bash_completion.d/kern
  # macOS:
  $ kern completion bash > $(brew --prefix)/etc/bash_completion.d/kern

Zsh:
  # If shell completion is not already enabled in your environment,
  # you will need to enable it. You can execute the following once:
  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ kern completion zsh > "${fpath[1]}/_kern"

Fish:
  $ kern completion fish | source
  # To load completions for each session, execute once:
  $ kern completion fish > ~/.config/fish/completions/kern.fish
`)
		if len(rest) < 1 {
			return 2
		}
		return 0
	}
	shell := strings.ToLower(rest[0])
	switch shell {
	case "bash":
		generateBashCompletion(os.Stdout)
		return 0
	case "zsh":
		generateZshCompletion(os.Stdout)
		return 0
	case "fish":
		generateFishCompletion(os.Stdout)
		return 0
	default:
		fatalUsage("unsupported shell %q (supported: bash, zsh, fish)", shell)
		return 2
	}
}
