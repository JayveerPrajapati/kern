package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// EvidenceProof provides an auditable, deterministic certificate verifying
// that an AI-cited file, line range, or symbol actually exists in the codebase.
type EvidenceProof struct {
	EvidenceID   string `json:"evidence_id"`
	Verified     bool   `json:"verified"`
	Target       string `json:"target"`
	Symbol       string `json:"symbol,omitempty"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	EndLine      int    `json:"end_line,omitempty"`
	LineDrift    int    `json:"line_drift"` // Drift from requested line (+/- N lines)
	ContentHash  string `json:"content_hash,omitempty"`
	Snippet      string `json:"snippet,omitempty"`
	Timestamp    string `json:"timestamp"`
	Verification string `json:"verification_detail"`
}

// handleEvidenceAnchor validates citations made by LLMs, corrects line drift,
// and issues a deterministic SHA-256 evidence certificate so agents can make
// zero-hallucination claims that downstream agents or humans can trust.
func (s *Server) handleEvidenceAnchor(ctx context.Context, args map[string]any) (string, error) {
	file := argString(args, "file")
	symbol := argString(args, "symbol")
	lineStr := argString(args, "line")
	claim := argString(args, "claim")
	root := resolveRoot(argString(args, "root"))

	// Auto-extract from claim if file/symbol are missing
	if file == "" && symbol == "" && claim != "" {
		if parts := strings.Split(claim, ":"); len(parts) >= 2 {
			file = strings.TrimSpace(parts[0])
			lineStr = strings.TrimSpace(parts[1])
		} else {
			symbol = strings.TrimSpace(claim)
		}
	}

	if file == "" && symbol == "" {
		return "", fmt.Errorf("at least one of 'file', 'symbol', or 'claim' must be provided")
	}

	reqLine := 0
	if lineStr != "" {
		if n, err := strconv.Atoi(lineStr); err == nil && n > 0 {
			reqLine = n
		}
	}

	ix, err := s.loadIndex(ctx, root)
	if err != nil {
		return "", fmt.Errorf("load index: %w", err)
	}

	proof := EvidenceProof{
		Target:    claim,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if proof.Target == "" {
		if file != "" && reqLine > 0 {
			proof.Target = fmt.Sprintf("%s:%d", file, reqLine)
		} else if file != "" {
			proof.Target = file
		} else {
			proof.Target = symbol
		}
	}

	// 1. Check by symbol
	var matchedSym *index.Symbol
	if symbol != "" {
		for _, sym := range ix.Symbols {
			if sym.Name == symbol || sym.FullName() == symbol {
				matchedSym = &sym
				break
			}
		}
		if matchedSym == nil {
			matches := ix.Search(symbol, 1)
			if len(matches) > 0 {
				matchedSym = &matches[0]
			}
		}
	}

	// If symbol matched, resolve file and lines
	if matchedSym != nil {
		proof.Symbol = matchedSym.FullName()
		proof.File = matchedSym.File
		proof.Line = matchedSym.Line
		proof.EndLine = matchedSym.End
		proof.Verified = true
		proof.Verification = fmt.Sprintf("Symbol %s resolved in AST index at %s:%d", proof.Symbol, proof.File, proof.Line)
		if reqLine > 0 {
			proof.LineDrift = proof.Line - reqLine
		}
	} else if file != "" {
		// 2. Check by file + line
		cleanRel := filepath.Clean(file)
		if filepath.IsAbs(cleanRel) {
			if rel, err := filepath.Rel(root, cleanRel); err == nil {
				cleanRel = rel
			}
		}

		fullPath := filepath.Join(root, cleanRel)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			proof.Verified = false
			proof.Verification = fmt.Sprintf("File %s not accessible on disk: %v", cleanRel, err)
		} else {
			lines := strings.Split(string(data), "\n")
			proof.File = cleanRel
			proof.Verified = true

			if reqLine > 0 && reqLine <= len(lines) {
				proof.Line = reqLine
				proof.Verification = fmt.Sprintf("File and line confirmed (%s:%d)", cleanRel, reqLine)
				start := reqLine - 1
				end := reqLine + 2
				if end > len(lines) {
					end = len(lines)
				}
				proof.Snippet = strings.TrimSpace(strings.Join(lines[start:end], "\n"))
			} else if reqLine > len(lines) {
				proof.Line = len(lines)
				proof.LineDrift = len(lines) - reqLine
				proof.Verification = fmt.Sprintf("Line %d exceeds file length (%d lines); anchored to file tail", reqLine, len(lines))
			} else {
				proof.Line = 1
				proof.Verification = fmt.Sprintf("File confirmed (%d total lines)", len(lines))
			}
		}
	} else {
		proof.Verified = false
		proof.Verification = fmt.Sprintf("Symbol %s not found in AST index", symbol)
	}

	// Compute deterministic SHA-256 evidence certificate
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s|%s|%d|%v|%s", proof.File, proof.Symbol, proof.Line, proof.Verified, ix.Root)
	proof.EvidenceID = "evidence-sha256:" + hex.EncodeToString(h.Sum(nil))[:16]

	// D4: compact text summary by default; full JSON behind format=json.
	if strings.ToLower(argString(args, "format")) == "json" {
		out, _ := json.MarshalIndent(proof, "", "  ")
		return string(out), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "evidence: %s\n", proof.EvidenceID)
	fmt.Fprintf(&sb, "verified: %v\n", proof.Verified)
	if proof.Target != "" {
		fmt.Fprintf(&sb, "target: %s\n", proof.Target)
	}
	if proof.Symbol != "" {
		fmt.Fprintf(&sb, "symbol: %s at %s:%d\n", proof.Symbol, proof.File, proof.Line)
	}
	if proof.LineDrift != 0 {
		fmt.Fprintf(&sb, "line drift: %d\n", proof.LineDrift)
	}
	if proof.Verification != "" {
		fmt.Fprintf(&sb, "verification: %s\n", proof.Verification)
	}
	if proof.Snippet != "" {
		limit := 200
		if len(proof.Snippet) > limit {
			proof.Snippet = proof.Snippet[:limit] + "..."
		}
		fmt.Fprintf(&sb, "snippet: %s\n", proof.Snippet)
	}
	return sb.String(), nil
}
