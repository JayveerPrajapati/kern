package host

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// fileAdapter is the shared implementation of Adapter for the five built-in
// host instruction files. It manages exactly one marked block per adapter
// name and never rewrites content outside the markers.
type fileAdapter struct {
	name string
	file string
}

func (a *fileAdapter) Name() string { return a.name }

func (a *fileAdapter) FilePath(root string) string { return filepath.Join(root, a.file) }

// Detect reports whether the instruction file exists.
func (a *fileAdapter) Detect(root string) bool {
	_, err := os.Stat(a.FilePath(root))
	return err == nil
}

// Inject renders the context block and writes it into the instruction file.
// If the file already contains this adapter's start marker, only the old block
// between the markers is replaced; otherwise the block is appended at EOF.
// Missing files are created (with parent directories, e.g. .cursor/rules).
func (a *fileAdapter) Inject(root string, pkt *domain.ContextPacket, budget int) (string, error) {
	block := RenderSummary(pkt, budget)
	start, end := blockMarkers(a.name)
	path := a.FilePath(root)
	content, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		content = nil
	}
	text := string(content)
	var out string
	if s := strings.Index(text, start); s >= 0 {
		if e := strings.Index(text, end); e >= 0 && e > s {
			out = text[:s] + start + "\n" + block + "\n" + end + text[e+len(end):]
		} else {
			// Stale start marker without an end marker: treat as no block.
			text = text[:s]
			out = appendBlock(text, start, block, end)
		}
	} else {
		out = appendBlock(text, start, block, end)
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return "", err
	}
	return block, nil
}

// appendBlock appends a fresh marked block at EOF, separated from existing
// content by a blank line.
func appendBlock(text, start, block, end string) string {
	sep := ""
	if text != "" && !strings.HasSuffix(text, "\n") {
		sep = "\n"
	}
	return text + sep + "\n" + start + "\n" + block + "\n" + end + "\n"
}

// Extract returns the current injected block between the markers (trimmed),
// or "" when the file is missing or has no block.
func (a *fileAdapter) Extract(root string) (string, error) {
	content, err := os.ReadFile(a.FilePath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	start, end := blockMarkers(a.name)
	text := string(content)
	s := strings.Index(text, start)
	e := strings.Index(text, end)
	if s < 0 || e < 0 || e <= s {
		return "", nil
	}
	return strings.TrimSpace(text[s+len(start) : e]), nil
}

// Uninstall removes only this adapter's marked block (and its markers),
// leaving the rest of the file byte-identical. A missing file or a file
// without this adapter's block is a no-op returning nil.
func (a *fileAdapter) Uninstall(root string) error {
	path := a.FilePath(root)
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	start, end := blockMarkers(a.name)
	text := string(content)
	s := strings.Index(text, start)
	e := strings.Index(text, end)
	if s < 0 || e < 0 || e <= s {
		return nil
	}
	// Swallow the newline the injector added before the start marker and the
	// newline after the end marker so removal restores the prior file bytes.
	s2 := s
	if s2 > 0 && text[s2-1] == '\n' {
		s2--
	}
	e2 := e + len(end)
	if e2 < len(text) && text[e2] == '\n' {
		e2++
	}
	return os.WriteFile(path, []byte(text[:s2]+text[e2:]), 0o644)
}

// NewOpenCodeAdapter manages the OpenCode instruction file (AGENTS.md).
func NewOpenCodeAdapter() Adapter { return &fileAdapter{name: "opencode", file: "AGENTS.md"} }

// NewClaudeAdapter manages the Claude instruction file (CLAUDE.md).
func NewClaudeAdapter() Adapter { return &fileAdapter{name: "claude", file: "CLAUDE.md"} }

// NewCursorAdapter manages the Cursor rules file
// (.cursor/rules/kern-context.mdc).
func NewCursorAdapter() Adapter {
	return &fileAdapter{name: "cursor", file: filepath.Join(".cursor", "rules", "kern-context.mdc")}
}

// NewCopilotAdapter manages the GitHub Copilot instruction file
// (.github/copilot-instructions.md).
func NewCopilotAdapter() Adapter {
	return &fileAdapter{name: "copilot", file: filepath.Join(".github", "copilot-instructions.md")}
}

// NewCodexAdapter manages the Codex instruction file (AGENTS.md).
func NewCodexAdapter() Adapter { return &fileAdapter{name: "codex", file: "AGENTS.md"} }
