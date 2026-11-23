package mcp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// appendContextGuidance adds a compact, deterministic anti-stall footer to
// orchestration outputs. File-referencing outputs are self-sufficient for the
// files they name; the footer says so, counts how many of those files were
// already served earlier in the session, and names a deterministic first file.
// Outputs without file references honestly report that no affected-file set was
// resolved instead of implying that context is complete. JSON envelopes keep
// their shape: guidance is added to a string "content" field when present.
func (s *Server) appendContextGuidance(args map[string]any, text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") {
		return s.appendContextGuidanceText(args, text)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		return text
	}
	content, ok := envelope["content"].(string)
	if !ok {
		return text
	}
	envelope["content"] = s.appendContextGuidanceText(args, content)
	out, err := json.Marshal(envelope)
	if err != nil {
		return text
	}
	return string(out)
}

func (s *Server) appendContextGuidanceText(args map[string]any, text string) string {
	files := ledgerFilesFromText(text)
	var b strings.Builder
	b.WriteString("\n## Context sufficiency\n")
	if len(files) == 0 {
		b.WriteString("ready-to-execute: unknown\n")
		b.WriteString("missing: no affected-file references in this output\n")
		b.WriteString("first slice: use kern_plan or kern_research to resolve affected files\n")

		return text + b.String()
	}

	previous := 0
	if s.ledger != nil {
		covered := s.ledger.FilesCovered(sessionKeyFor(args))
		for _, file := range files {
			if covered[file] {
				previous++
			}
		}
	}
	b.WriteString("ready-to-execute: true\n")
	fmt.Fprintf(&b, "covered: %d/%d referenced files (%d previously served)\n", len(files), len(files), previous)
	b.WriteString("missing: none\n")

	first := files[0]
	b.WriteString("\n## First slice\n")
	fmt.Fprintf(&b, "start with: %s\n", first)
	fmt.Fprintf(&b, "verify with: %s\n", testCommandForPath(first))
	return text + b.String()
}

func testCommandForPath(path string) string {
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "." {
		return "go test ."
	}
	return "go test ./" + dir
}
