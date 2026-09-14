package merge3

import (
	"strings"
	"testing"
)

func TestMerge3_Go(t *testing.T) {
	base := `package service

type Server struct {
	Port int
}

func NewServer() *Server {
	return &Server{Port: 8080}
}
`

	// Local adds Start method
	local := `package service

import (
	"fmt"
)

type Server struct {
	Port int
	Host string
}

func NewServer() *Server {
	return &Server{Port: 8080}
}

func (s *Server) Start() {
	fmt.Println("start")
}
`

	// Remote adds Stop method
	remote := `package service

import (
	"log"
)

type Server struct {
	Port int
	Timeout int
}

func NewServer() *Server {
	return &Server{Port: 8080}
}

func (s *Server) Stop() {
	log.Println("stop")
}
`

	res, err := Merge3Way("server.go", []byte(base), []byte(local), []byte(remote))
	if err != nil {
		t.Fatalf("Merge3Way error: %v", err)
	}

	if !res.Clean {
		t.Fatalf("expected clean merge, got conflicts: %+v", res.Conflicts)
	}

	// Verify merged components
	if !strings.Contains(res.MergedCode, "fmt") || !strings.Contains(res.MergedCode, "log") {
		t.Errorf("expected merged imports in:\n%s", res.MergedCode)
	}
	if !strings.Contains(res.MergedCode, "Host") || !strings.Contains(res.MergedCode, "Timeout") {
		t.Errorf("expected merged struct fields in:\n%s", res.MergedCode)
	}
	if !strings.Contains(res.MergedCode, "Start()") || !strings.Contains(res.MergedCode, "Stop()") {
		t.Errorf("expected both methods in:\n%s", res.MergedCode)
	}
}

func TestMerge3_Python(t *testing.T) {
	base := `from typing import List

def get_items() -> List[str]:
    return ["a", "b"]
`

	// Local adds get_count
	local := `from typing import List
import os

def get_items() -> List[str]:
    return ["a", "b"]

def get_count() -> int:
    return len(get_items())
`

	// Remote adds clear_items
	remote := `from typing import List
import sys

def get_items() -> List[str]:
    return ["a", "b"]

def clear_items():
    pass
`

	res, err := Merge3Way("items.py", []byte(base), []byte(local), []byte(remote))
	if err != nil {
		t.Fatalf("Merge3Way Python error: %v", err)
	}

	if !res.Clean {
		t.Fatalf("expected clean Python merge, got: %+v", res.Conflicts)
	}

	if !strings.Contains(res.MergedCode, "import os") || !strings.Contains(res.MergedCode, "import sys") {
		t.Errorf("missing merged python imports: %s", res.MergedCode)
	}
	if !strings.Contains(res.MergedCode, "def get_count") || !strings.Contains(res.MergedCode, "def clear_items") {
		t.Errorf("missing merged python functions: %s", res.MergedCode)
	}
}

func TestMerge3_TypeScript(t *testing.T) {
	base := `import { Base } from './base';

export function calculate(x: number): number {
  return x * 2;
}
`

	// Local adds add
	local := `import { Base } from './base';
import { Logger } from './logger';

export function calculate(x: number): number {
  return x * 2;
}

export function add(a: number, b: number): number {
  return a + b;
}
`

	// Remote adds subtract
	remote := `import { Base } from './base';
import { Metrics } from './metrics';

export function calculate(x: number): number {
  return x * 2;
}

export function subtract(a: number, b: number): number {
  return a - b;
}
`

	res, err := Merge3Way("calc.ts", []byte(base), []byte(local), []byte(remote))
	if err != nil {
		t.Fatalf("Merge3Way TS error: %v", err)
	}

	if !res.Clean {
		t.Fatalf("expected clean TS merge, got: %+v", res.Conflicts)
	}

	if !strings.Contains(res.MergedCode, "import { Logger }") || !strings.Contains(res.MergedCode, "import { Metrics }") {
		t.Errorf("missing merged TS imports: %s", res.MergedCode)
	}
	if !strings.Contains(res.MergedCode, "function add") || !strings.Contains(res.MergedCode, "function subtract") {
		t.Errorf("missing merged TS functions: %s", res.MergedCode)
	}
}
