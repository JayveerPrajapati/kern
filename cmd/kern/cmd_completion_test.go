package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestGenerateShellCompletions(t *testing.T) {
	// Bash
	var bashBuf bytes.Buffer
	generateBashCompletion(&bashBuf)
	bashOut := bashBuf.String()
	if !strings.Contains(bashOut, "complete -F _kern kern") {
		t.Errorf("bash completion missing registration: %s", bashOut)
	}
	if !strings.Contains(bashOut, "search") || !strings.Contains(bashOut, "optimize") {
		t.Errorf("bash completion missing core commands: %s", bashOut)
	}

	// Zsh
	var zshBuf bytes.Buffer
	generateZshCompletion(&zshBuf)
	zshOut := zshBuf.String()
	if !strings.Contains(zshOut, "#compdef kern") {
		t.Errorf("zsh completion missing #compdef: %s", zshOut)
	}
	if !strings.Contains(zshOut, "'search:") {
		t.Errorf("zsh completion missing search command: %s", zshOut)
	}

	// Fish
	var fishBuf bytes.Buffer
	generateFishCompletion(&fishBuf)
	fishOut := fishBuf.String()
	if !strings.Contains(fishOut, "complete -c kern -f") {
		t.Errorf("fish completion missing base config: %s", fishOut)
	}
	if !strings.Contains(fishOut, `complete -c kern -n "__fish_use_subcommand" -a "search"`) {
		t.Errorf("fish completion missing search command: %s", fishOut)
	}
}
