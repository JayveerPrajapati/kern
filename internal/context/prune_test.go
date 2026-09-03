package context

import (
	"strings"
	"testing"
)

func TestPruneGoASTStripsDocstringsAndDirectives(t *testing.T) {
	src := `//go:build !windows
// Package foo is an example package with verbose comments.
package foo

// Config holds configuration parameters.
// More verbose documentation that consumes tokens.
type Config struct {
	// Port is the network port.
	Port int
	// Host is the host name.
	Host string
}

// Service defines the core interface.
type Service interface {
	// Run starts the service.
	Run() error
}

// NewConfig returns a default config.
// Here are more lines of docstring.
func NewConfig() *Config {
	// In-line comment here
	return &Config{Port: 8080}
}
`
	pruned, err := PruneGo([]byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := string(pruned)

	if strings.Contains(out, "//go:build") {
		t.Errorf("expected //go:build stripped, got:\n%s", out)
	}
	if strings.Contains(out, "More verbose documentation") {
		t.Errorf("expected struct docstring stripped, got:\n%s", out)
	}
	if strings.Contains(out, "Here are more lines of docstring") {
		t.Errorf("expected func docstring stripped, got:\n%s", out)
	}
	if !strings.Contains(out, "type Config struct") {
		t.Errorf("expected struct preserved, got:\n%s", out)
	}
	if !strings.Contains(out, "func NewConfig() *Config") {
		t.Errorf("expected func signature preserved, got:\n%s", out)
	}
	if !strings.Contains(out, "type Service interface") {
		t.Errorf("expected interface preserved, got:\n%s", out)
	}
}

func TestPrunePythonStripsDocstrings(t *testing.T) {
	src := `"""Module docstring that is very long and verbose."""
import os

class Worker:
    """Worker class docstring with multiline details."""
    def __init__(self):
        # inline comment
        self.count = 0

    def run(self):
        """Run method docstring."""
        print("running")
`
	pruned := PrunePython([]byte(src))
	out := string(pruned)
	if strings.Contains(out, "Module docstring") || strings.Contains(out, "Worker class docstring") || strings.Contains(out, "Run method docstring") {
		t.Errorf("expected python docstrings stripped, got:\n%s", out)
	}
	if !strings.Contains(out, "class Worker:") || !strings.Contains(out, "def run(self):") {
		t.Errorf("expected signatures preserved, got:\n%s", out)
	}
}

func TestPruneCStyleComments(t *testing.T) {
	src := `/*
 * Multi-line header comment.
 * License and details.
 */
public class Main {
    // Single line comment
    public static void main(String[] args) {
        System.out.println("Hello");
    }
}
`
	pruned := PruneCStyle([]byte(src))
	out := string(pruned)
	if strings.Contains(out, "Multi-line header comment") || strings.Contains(out, "Single line comment") {
		t.Errorf("expected C-style comments stripped, got:\n%s", out)
	}
	if !strings.Contains(out, "public class Main") || !strings.Contains(out, "public static void main") {
		t.Errorf("expected class and method preserved, got:\n%s", out)
	}
}
