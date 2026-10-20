package langparse

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

//go:embed compiler/KernJavaBindings.java
var JavaCompilerSource string

type javaCompilerRecord struct {
	Kind     string   `json:"kind"`
	Key      string   `json:"key"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Name     string   `json:"name"`
	Receiver string   `json:"receiver"`
	Outer    string   `json:"outer"`
	Params   []string `json:"params"`
	Caller   string   `json:"caller"`
	Callee   string   `json:"callee"`
	Target   string   `json:"target"`
	Synth    string   `json:"synth"`
}

func BindJavaCompiler(root string, symbols []Symbol, classpath string, calls map[string][]CallEdge) (map[string][]CallEdge, error) {
	files := map[string]bool{}
	for _, symbol := range symbols {
		if symbol.Lang == "java" || strings.HasSuffix(symbol.File, ".java") {
			files[symbol.File] = true
		}
	}
	if len(files) == 0 {
		return calls, nil
	}
	java, err := exec.LookPath("java")
	if err != nil {
		return calls, fmt.Errorf("Java compiler bindings require a local JDK: %w", err)
	}
	directory, err := os.MkdirTemp("", "kern-javac-")
	if err != nil {
		return calls, err
	}
	defer os.RemoveAll(directory)
	helper := filepath.Join(directory, "KernJavaBindings.java")
	if err := os.WriteFile(helper, []byte(JavaCompilerSource), 0o600); err != nil {
		return calls, err
	}
	var paths []string
	for file := range files {
		if strings.ContainsAny(file, "\r\n") {
			return calls, fmt.Errorf("Java compiler bindings do not support newline-containing paths")
		}
		paths = append(paths, filepath.Join(root, file))
	}
	sort.Strings(paths)
	manifest := filepath.Join(directory, "sources.txt")
	if err := os.WriteFile(manifest, []byte(strings.Join(paths, "\n")), 0o600); err != nil {
		return calls, err
	}
	if classpath == "" {
		classpath = root
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, java, helper, manifest, classpath)
	command.Dir = root
	for _, environment := range os.Environ() {
		name := strings.SplitN(environment, "=", 2)[0]
		if name != "JAVA_TOOL_OPTIONS" && name != "JDK_JAVA_OPTIONS" && name != "_JAVA_OPTIONS" {
			command.Env = append(command.Env, environment)
		}
	}
	var output, diagnostics bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostics
	if err := command.Run(); err != nil {
		message := diagnostics.String()
		if len(message) > 4000 {
			message = message[:4000]
		}
		return calls, fmt.Errorf("Java compiler bindings: %w: %s", err, message)
	}
	definitions := map[string][]Symbol{}
	for _, symbol := range symbols {
		definitions[filepath.ToSlash(symbol.File)+":"+symbol.FullName()] = append(definitions[filepath.ToSlash(symbol.File)+":"+symbol.FullName()], symbol)
	}
	byKey := map[string]Symbol{}
	var records []javaCompilerRecord
	scanner := bufio.NewScanner(&output)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var record javaCompilerRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return calls, fmt.Errorf("invalid javac binding output: %w", err)
		}
		if record.Kind == "decl" {
			rel, err := filepath.Rel(root, record.File)
			if err != nil || !files[filepath.ToSlash(rel)] {
				continue
			}
			var matches []Symbol
			for _, symbol := range definitions[filepath.ToSlash(rel)+":"+record.Receiver+"."+record.Name] {
				if symbol.Line == record.Line && symbol.Outer == record.Outer && slices.Equal(symbol.Params, record.Params) {
					matches = append(matches, symbol)
				}
			}
			if len(matches) == 1 {
				byKey[record.Key] = matches[0]
			}
		} else if record.Kind == "call" {
			records = append(records, record)
		}
	}
	if err := scanner.Err(); err != nil {
		return calls, err
	}
	replacements := map[string]map[string][]CallEdge{}
	for _, symbol := range byKey {
		if replacements[symbol.FullName()] == nil {
			replacements[symbol.FullName()] = map[string][]CallEdge{}
		}
		replacements[symbol.FullName()][symbol.File] = nil
	}
	for _, record := range records {
		owner, found := byKey[record.Caller]
		if !found {
			continue
		}
		confidence := ConfidenceHigh
		if record.Synth != "" {
			confidence = ConfidenceLow
		}
		edge := CallEdge{Target: record.Target, SourceFile: owner.File, FromID: owner.DeclarationID(), Binding: "javac", Synth: record.Synth, Confidence: confidence}
		if callee, found := byKey[record.Callee]; found {
			edge.ToID = callee.DeclarationID()
		}
		replacements[owner.FullName()][owner.File] = append(replacements[owner.FullName()][owner.File], edge)
	}
	for owner, edges := range calls {
		var kept []CallEdge
		for _, edge := range edges {
			if edge.Synth == "dispatch:javac" {
				continue
			}
			if edge.Binding == "javac" {
				edge.Binding, edge.FromID, edge.ToID, edge.Confidence = "", "", "", ConfidenceMedium
			}
			kept = append(kept, edge)
		}
		calls[owner] = kept
	}
	for owner, byFile := range replacements {
		var merged []CallEdge
		for _, edge := range calls[owner] {
			if _, replaced := byFile[edge.SourceFile]; !replaced || (edge.Synth != "" && !strings.HasPrefix(edge.Synth, "dispatch")) {
				merged = append(merged, edge)
			}
		}
		for _, edges := range byFile {
			merged = append(merged, edges...)
		}
		calls[owner] = DedupeCallEdges(merged)
	}
	return calls, nil
}
