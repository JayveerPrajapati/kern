package fit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// Request defines the input parameters for adaptive context fitting.
type Request struct {
	Root      string   `json:"root"`
	MaxTokens int      `json:"max_tokens"`
	Files     []string `json:"files,omitempty"`
	Symbols   []string `json:"symbols,omitempty"`
	Query     string   `json:"query,omitempty"`
}

// Result is the deterministic outcome of adaptive context fitting.
type Result struct {
	Tier       string       `json:"tier"` // "full", "folded", "summary"
	Budget     int          `json:"budget"`
	UsedTokens int          `json:"used_tokens"`
	SavingsPct float64      `json:"savings_pct"`
	FileCount  int          `json:"file_count"`
	Files      []FileResult `json:"files"`
	Content    string       `json:"content"`
}

// FileResult holds the per-file fitted representation.
type FileResult struct {
	Path   string `json:"path"`
	Tier   string `json:"tier"`
	Tokens int    `json:"tokens"`
}

// FitContext dynamically computes the highest fidelity source representation
// guaranteed to fit within the specified token budget.
func FitContext(ctx context.Context, req Request) (*Result, error) {
	if req.Root == "" {
		req.Root = "."
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 8000
	}

	absRoot, err := filepath.Abs(req.Root)
	if err != nil {
		absRoot = req.Root
	}

	// Resolve target files
	targetFiles := make(map[string]bool)
	for _, f := range req.Files {
		clean := filepath.Clean(f)
		if filepath.IsAbs(clean) {
			if rel, err := filepath.Rel(absRoot, clean); err == nil && !strings.HasPrefix(rel, "..") {
				targetFiles[rel] = true
			} else {
				targetFiles[clean] = true
			}
		} else {
			targetFiles[clean] = true
		}
	}

	// If symbols or query specified, resolve files via index
	if len(req.Symbols) > 0 || req.Query != "" {
		ix, _ := index.Load(absRoot)
		if ix == nil {
			ix, _ = index.Build(absRoot)
		}
		if ix != nil {
			for _, symName := range req.Symbols {
				for _, sym := range ix.Symbols {
					if sym.Name == symName || sym.FullName() == symName {
						targetFiles[sym.File] = true
					}
				}
			}
			if req.Query != "" {
				matches := ix.Search(req.Query, 10)
				for _, sym := range matches {
					targetFiles[sym.File] = true
				}
			}
		}
	}

	var filesList []string
	for f := range targetFiles {
		filesList = append(filesList, f)
	}
	sort.Strings(filesList)

	if len(filesList) == 0 {
		return &Result{
			Tier:       "empty",
			Budget:     req.MaxTokens,
			UsedTokens: 0,
			SavingsPct: 0,
			FileCount:  0,
			Files:      nil,
			Content:    "No matching files or symbols found to fit.",
		}, nil
	}

	// Read file contents
	fileContents := make(map[string]string)
	for _, rel := range filesList {
		full := filepath.Join(absRoot, rel)
		data, err := os.ReadFile(full)
		if err == nil {
			fileContents[rel] = string(data)
		}
	}

	// Try Tier 1: Full source
	tier1Content, tier1Files, tier1Tokens := renderTier(absRoot, filesList, fileContents, code.TierFull)
	if tier1Tokens <= req.MaxTokens {
		return &Result{
			Tier:       "full",
			Budget:     req.MaxTokens,
			UsedTokens: tier1Tokens,
			SavingsPct: 0,
			FileCount:  len(tier1Files),
			Files:      tier1Files,
			Content:    tier1Content,
		}, nil
	}

	// Try Tier 2: Folded AST (bodies elided)
	tier2Content, tier2Files, tier2Tokens := renderTier(absRoot, filesList, fileContents, code.TierFolded)
	savings := 0.0
	if tier1Tokens > 0 {
		savings = float64(tier1Tokens-tier2Tokens) / float64(tier1Tokens) * 100.0
	}
	if tier2Tokens <= req.MaxTokens {
		return &Result{
			Tier:       "folded",
			Budget:     req.MaxTokens,
			UsedTokens: tier2Tokens,
			SavingsPct: savings,
			FileCount:  len(tier2Files),
			Files:      tier2Files,
			Content:    tier2Content,
		}, nil
	}

	// Try Tier 3: Summary
	tier3Content, tier3Files, tier3Tokens := renderTier(absRoot, filesList, fileContents, code.TierSummary)
	if tier1Tokens > 0 {
		savings = float64(tier1Tokens-tier3Tokens) / float64(tier1Tokens) * 100.0
	}
	return &Result{
		Tier:       "summary",
		Budget:     req.MaxTokens,
		UsedTokens: tier3Tokens,
		SavingsPct: savings,
		FileCount:  len(tier3Files),
		Files:      tier3Files,
		Content:    tier3Content,
	}, nil
}

func renderTier(root string, files []string, contents map[string]string, tier code.Tier) (string, []FileResult, int) {
	var b strings.Builder
	var results []FileResult
	tierName := "full"
	switch tier {
	case code.TierFolded:
		tierName = "folded"
	case code.TierSummary:
		tierName = "summary"
	}

	for _, f := range files {
		c, ok := contents[f]
		if !ok {
			continue
		}
		rendered := code.RenderTier(f, []byte(c), tier)
		tokens := tokenize.Count(rendered)
		results = append(results, FileResult{
			Path:   f,
			Tier:   tierName,
			Tokens: tokens,
		})

		fmt.Fprintf(&b, "// --- %s (%s, ~%d tokens) ---\n", f, tierName, tokens)
		b.WriteString(rendered)
		b.WriteString("\n\n")
	}

	fullText := b.String()
	totalTokens := tokenize.Count(fullText)
	return fullText, results, totalTokens
}

// RenderJSON serializes the Result to indented JSON.
func (r *Result) RenderJSON() string {
	data, _ := json.MarshalIndent(r, "", "  ")
	return string(data)
}
