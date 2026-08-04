// Package memory persists distilled, cross-session lessons per project. An
// agent can record what it learned ("kern remember ..."), and the lessons are
// injected into new sessions via the buddy briefing, so the project "brain"
// carries over between sessions.
package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

const maxEntries = 50

// Entry is a single recorded lesson.
type Entry struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
}

// Store is a project's lesson list.
type Store struct {
	Root    string  `json:"root"`
	Entries []Entry `json:"entries"`
}

// Path returns the on-disk location for the memory of root.
func Path(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return cache.Path("memory", cache.Hash([]byte(abs))+".json")
}

// Load reads the project memory, or returns an empty store if absent.
func Load(root string) Store {
	var s Store
	if err := readJSON(Path(root), &s); err != nil {
		return Store{Root: root}
	}
	if s.Entries == nil {
		s.Entries = []Entry{}
	}
	return s
}

// Add appends a lesson, dropping the oldest entries beyond maxEntries.
func Add(root, lesson string) error {
	if strings.TrimSpace(lesson) == "" {
		return nil
	}
	s := Load(root)
	s.Entries = append(s.Entries, Entry{Time: time.Now().UTC(), Text: lesson})
	if len(s.Entries) > maxEntries {
		s.Entries = s.Entries[len(s.Entries)-maxEntries:]
	}
	return writeJSON(Path(root), s)
}

// List returns the lessons for root, most recent first.
func List(root string) []Entry {
	s := Load(root)
	out := make([]Entry, len(s.Entries))
	for i, e := range s.Entries {
		out[len(s.Entries)-1-i] = e
	}
	return out
}

// Clear removes all lessons for root.
func Clear(root string) error {
	err := os.Remove(Path(root))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
