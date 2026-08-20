// Package memory persists distilled, cross-session lessons per project. An
// agent can record what it learned ("kern remember ..."), and the lessons are
// injected into new sessions via the buddy briefing, so the project "brain"
// carries over between sessions.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

const maxEntries = 50

// mu serializes the load→mutate→save cycle in Add (and the Clear removal) so
// concurrent writers to the same lesson file cannot clobber each other. The
// legacy Store is a value type rebuilt on every Load, so the guard is
// package-level rather than a field on Store.
var mu sync.Mutex

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
	mu.Lock()
	defer mu.Unlock()
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
	mu.Lock()
	defer mu.Unlock()
	err := os.Remove(Path(root))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// stopwords are dropped from recall queries so generic lessons about "code",
// "the", "build" do not dominate scoring.
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "of": true,
	"to": true, "for": true, "in": true, "on": true, "with": true, "is": true,
	"are": true, "be": true, "it": true, "this": true, "that": true, "i": true,
	"we": true, "you": true, "should": true, "must": true, "do": true, "not": true,
	"code": true, "file": true, "files": true, "project": true, "build": true,
	"kern": true, "use": true, "when": true, "if": true, "as": true, "by": true,
	"from": true, "at": true, "also": true, "using": true,
}

// tokens lower-cases and splits a string into non-trivial keyword tokens.
func tokens(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_')
	}) {
		w := strings.ToLower(f)
		if len(w) < 3 || stopwords[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// Recall returns the up-to-k most relevant past lessons for prompt, ranked by
// keyword overlap with the prompt's salient tokens. Deterministic: ties keep
// recency order.
func Recall(root, prompt string, k int) []Entry {
	ptoks := tokens(prompt)
	if len(ptoks) == 0 {
		return nil
	}
	type scored struct {
		e Entry
		s float64
	}
	var pool []scored
	for _, e := range List(root) {
		etoks := tokens(e.Text)
		if len(etoks) == 0 {
			continue
		}
		set := map[string]bool{}
		for _, t := range etoks {
			set[t] = true
		}
		overlap := 0
		for _, t := range ptoks {
			if set[t] {
				overlap++
			}
		}
		// Normalize by query size; add a small coverage bonus for extra
		// matching terms in the lesson.
		s := float64(overlap)/float64(len(ptoks)) + float64(overlap)/float64(len(etoks))*0.5
		if overlap > 0 {
			pool = append(pool, scored{e, s})
		}
	}
	// Stable sort desc by score keeps recency order on ties.
	for i := 0; i < len(pool); i++ {
		for j := i + 1; j < len(pool); j++ {
			if pool[j].s > pool[i].s {
				pool[i], pool[j] = pool[j], pool[i]
			}
		}
	}
	if len(pool) > k {
		pool = pool[:k]
	}
	out := make([]Entry, len(pool))
	for i, p := range pool {
		out[i] = p.e
	}
	return out
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		// Preserve the corrupt file for recovery and never silently proceed
		// with partial data (which would let the next save destroy the file).
		_ = os.Rename(path, path+".corrupt")
		return fmt.Errorf("memory: corrupt JSON at %s (renamed to .corrupt): %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write (temp file + rename) so a concurrent reader never observes
	// a partially-written store. The temp name is unique so concurrent writers
	// never clobber each other's staging file.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
