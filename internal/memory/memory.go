// Package memory persists distilled, cross-session lessons per project. An
// agent can record what it learned ("kern remember ..."), and the lessons are
// injected into new sessions via the buddy briefing, so the project "brain"
// carries over between sessions.
package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
)

const maxEntries = 50

// mu serializes the load→mutate→save cycle in Add (and the Clear removal) so
// concurrent writers to the same lesson file cannot clobber each other. The
// legacy Store is a value type rebuilt on every Load, so the guard is
// package-level rather than a field on Store.
var mu sync.Mutex

// Entry is a single recorded lesson. Source distinguishes deliberate lessons
// ("", set by kern remember / kern_memory_add) from automatic session captures
// ("auto", written by the hooks/plugin conversation capture). Auto entries are
// labeled on list and excluded from recall by default (see Recall) so raw
// prompts never leak back into an LLM context.
type Entry struct {
	Time   time.Time `json:"time"`
	Text   string    `json:"text"`
	Source string    `json:"source,omitempty"`
}

// Store is a project's lesson list.
type Store struct {
	Root    string  `json:"root"`
	Entries []Entry `json:"entries"`
}

// DefaultRecallLimit is the number of recall hits when the caller does not
// specify one. Shared by the CLI and MCP surfaces so the default cannot
// drift between them.
const DefaultRecallLimit = 5

// NoRecallMatch is the no-match hint surfaced by every recall path (CLI
// `kern recall` / `kern memory recall`, MCP `kern_memory_recall` and the
// `kern_memory_recall` action). It points the user at the fix (record a
// lesson) instead of printing nothing.
const NoRecallMatch = "no matching lessons (record one with: kern memory add <lesson>)"

// FormatEntry renders one entry as a single line (no trailing newline) in
// the format shared by every surface: "2006-01-02 15:04  [auto] lesson text".
// Auto captures carry the [auto] label so raw session captures are visibly
// distinct from deliberate lessons wherever entries are listed or recalled.
func FormatEntry(e Entry) string {
	marker := ""
	if e.Source == "auto" {
		marker = "[auto] "
	}
	return e.Time.UTC().Format("2006-01-02 15:04") + "  " + marker + e.Text
}

// FormatEntries renders entries one per line; empty string when none.
func FormatEntries(entries []Entry) string {
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = FormatEntry(e)
	}
	return strings.Join(lines, "\n")
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
		// Corrupt (already renamed to .corrupt by readJSON, data preserved
		// for recovery) or unreadable: log it — a silent reset would let the
		// next write look like a wipe (L5-3). Absent-file is the normal
		// first-use path and also lands here quietly.
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("memory: v1 store unreadable for %s (%v); starting empty (backup at .corrupt if corrupt)", root, err)
		}
		return Store{Root: root}
	}
	if s.Entries == nil {
		s.Entries = []Entry{}
	}
	// Migration: entries written before Source existed carry no field. Backfill
	// the auto label for the conversation-capture prefixes so legacy raw
	// prompts are not silently treated as agent lessons.
	for i := range s.Entries {
		if s.Entries[i].Source == "" && isAutoCapture(s.Entries[i].Text) {
			s.Entries[i].Source = "auto"
		}
	}
	return s
}

// isAutoCapture reports whether a lesson text matches an automatic session
// capture (raw prompts, tool outcomes, sanitized change snapshots) rather than
// an explicitly remembered lesson.
func isAutoCapture(text string) bool {
	return strings.HasPrefix(text, "User: ") ||
		strings.HasPrefix(text, "Edited ") ||
		strings.HasPrefix(text, "Command failed: ") ||
		strings.HasPrefix(text, "Explored ") ||
		strings.HasPrefix(text, "latest change:")
}

// Add appends a lesson, enforcing the maxEntries cap without letting an
// automatic capture evict an explicit lesson (see trimToCap). A
// conversation-capture text is tagged Source "auto" (see isAutoCapture) so it
// is labeled and excluded from recall regardless of which entry point wrote it
// (CLI, MCP, or the opencode/hook plugin).
func Add(root, lesson string) error {
	source := ""
	if isAutoCapture(lesson) {
		source = "auto"
	}
	_, err := addWithSource(root, lesson, source)
	return err
}

// AddAuto appends an explicitly automatic capture (raw prompt, tool outcome)
// tagged Source "auto". Use it from session-capture code paths. The bool
// reports whether a fresh entry landed — an identical capture within the
// 60s dedup window is swallowed (false, nil).
func AddAuto(root, lesson string) (bool, error) {
	return addWithSource(root, lesson, "auto")
}

// AddExplicit records a deliberate, user-authored lesson — the shared write
// path behind `kern memory add`, `kern remember` and kern_memory action=add.
// It dual-writes:
//   - the TYPED store (ememory/<hash>.json) with Source "user", the store
//     buddy's "Project memory" section reads. P2-14 moved the digest onto
//     the typed store, so explicit lessons that wrote only the v1 store
//     stopped rendering anywhere in the digest; routing them here fixes
//     that. Source "user" is not "auto", so CurrentMemories (which filters
//     only by status/type) keeps it and brief.go's Source == "auto" guard
//     does not skip it.
//   - the v1 lesson store (memory/<hash>.json) with Source "" so the
//     existing v1 consumers (`kern memory recall`/`list`, kern_memory
//     recall/remove/clear, the digest's "Recent session activity") keep
//     seeing the same explicit lessons.
//
// Automatic session captures are NOT routed here — hook/plugin capture uses
// AddAuto (v1, Source "auto") and stays in "Recent session activity".
func AddExplicit(root, lesson string) (bool, error) {
	if strings.TrimSpace(lesson) == "" {
		return false, nil
	}
	// Explicit is always genuine (L5-4): the caller chose to remember this
	// text, so its Source is "user" in both stores no matter what it starts
	// with — a real lesson like "Edited files must always ship with tests"
	// stays recallable and prune --auto-safe. Auto routing is signal-driven
	// only: the plugin/hooks pass --auto (AddAuto); the prefix classifier
	// stays on the legacy Add entry point and Load's read-side migration.
	if _, err := NewMemoryStore(root).Add(domain.Memory{
		Type:    domain.MemoryLesson,
		Content: lesson,
		Source:  "user",
		Scope:   "project",
	}); err != nil {
		return false, err
	}
	// v1 write with Source "user" (not "") so Load's legacy migration
	// cannot relabel auto-shaped explicit lessons as auto, and recall/list
	// keep finding it (they only filter Source "auto").
	return addWithSource(root, lesson, "user")
}

// addTypedAuto writes an auto capture to the typed store, skipping the write
// when an identical capture already landed within the 60s dedup window (the
// same window addWithSource uses for the v1 store): several agent surfaces
// can fire the same conversation event within seconds, and the explicit
// path must not triplicate it in the typed store either. The bool reports
// whether a fresh typed entry landed.
func addTypedAuto(root, lesson string) (bool, error) {
	st := NewMemoryStore(root)
	mems, err := st.CurrentMemories("")
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	for _, m := range mems { // newest-first (sortRecency)
		if now.Sub(m.CreatedAt) > 60*time.Second {
			break // entries are chronological: older ones are out of window
		}
		if m.Content == lesson {
			return false, nil
		}
	}
	_, err = st.Add(domain.Memory{
		Type:    domain.MemoryLesson,
		Content: lesson,
		Source:  "auto",
		Scope:   "project",
	})
	return true, err
}

func addWithSource(root, lesson, source string) (bool, error) {
	if strings.TrimSpace(lesson) == "" {
		return false, nil
	}
	mu.Lock()
	defer mu.Unlock()
	// Cross-process serialization (L5-3): the plugin's `kern remember
	// --auto` subprocess and the MCP server's in-process AddExplicit both
	// run Load→append→writeJSON; last-writer-wins dropped the other's
	// entry. The sidecar .lock (OS-dropped if a holder crashes) serializes
	// load-modify-write across processes; mu stays for in-process callers.
	fl, err := cache.LockFile(Path(root))
	if err != nil {
		return false, fmt.Errorf("memory: lock store: %w", err)
	}
	defer fl.Unlock()
	s := Load(root)
	// Dedupe automatic captures: the same conversation event can be
	// captured by several agent surfaces (opencode plugin, Claude
	// PostToolUse, Codex/Gemini hooks) firing within seconds of each
	// other, which previously triplicated every auto entry. The window is
	// signal-driven (L5-4): it fires only for source "auto" writes — the
	// auto entry points (AddAuto, auto-classified legacy Add) — and an
	// auto duplicate of an already-stored genuine lesson dedupes too. A
	// genuine explicit lesson (source "user") is never deduped, whatever
	// its text starts with (deliberate re-remember is user intent; pinned
	// by test).
	if source == "auto" {
		now := time.Now().UTC()
		for i := len(s.Entries) - 1; i >= 0; i-- {
			e := s.Entries[i]
			if now.Sub(e.Time) > 60*time.Second {
				break // entries are chronological: older ones are out of window
			}
			if e.Text == lesson {
				return false, nil
			}
		}
	}
	s.Entries = append(s.Entries, Entry{Time: time.Now().UTC(), Text: lesson, Source: source})
	s.Entries = trimToCap(s.Entries)
	return true, writeJSON(Path(root), s)
}

// trimToCap enforces the maxEntries hard cap without letting an automatic
// capture evict an explicit lesson. Automatic captures (Source "auto", raw
// prompts and tool outcomes) are session noise and regenerable, so when the
// store is over capacity the OLDEST auto entries are dropped first. Only when
// no auto entries remain (an explicit-only store) is the oldest explicit
// lesson dropped, so the hard cap still bounds a store that holds nothing but
// deliberate lessons. An auto flood can therefore evict older auto captures
// but never an explicit lesson.
func trimToCap(entries []Entry) []Entry {
	if len(entries) <= maxEntries {
		return entries
	}
	over := len(entries) - maxEntries
	dropped := 0
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Source == "auto" && dropped < over {
			dropped++
			continue
		}
		out = append(out, e)
	}
	// Defensive: the store held fewer auto entries than we needed to drop
	// (an explicit-only store at capacity) — honor the hard cap by dropping
	// the oldest remaining (explicit) entry as well.
	if len(out) > maxEntries {
		out = out[len(out)-maxEntries:]
	}
	return out
}

// List returns the lessons for root, most recent first.
//
// The read is serialized with writers (QA 5.4): the MCP server dispatches
// tools/call requests concurrently (one goroutine per call), so a recall sent
// right after an add could read the store file BEFORE the add's atomic rename
// landed — "add then recall within one session" missed the just-added lesson.
// mu (in-process) and the cross-process file lock (the same one writers hold)
// order the read after any in-flight write, so add→recall in one session
// deterministically sees the write.
func List(root string) []Entry {
	mu.Lock()
	defer mu.Unlock()
	fl, err := cache.LockFile(Path(root))
	if err != nil {
		return reverseEntries(Load(root)) // degrade loudly to the atomic read; writes stay safe
	}
	defer fl.Unlock()
	return reverseEntries(Load(root))
}

// reverseEntries renders a store newest-first (the display order every list
// surface uses; the store keeps append order on disk).
func reverseEntries(s Store) []Entry {
	out := make([]Entry, len(s.Entries))
	for i, e := range s.Entries {
		out[len(s.Entries)-1-i] = e
	}
	return out
}

// Clear removes all lessons for root from BOTH stores: the v1 lesson store
// AND the typed store. `kern memory clear` / kern_memory clear previously
// emptied only the v1 store, leaving buddy's "Project memory" (typed store)
// populated with the very lessons the user asked to clear.
func Clear(root string) error {
	mu.Lock()
	defer mu.Unlock()
	fl, err := cache.LockFile(Path(root)) // cross-process L5-3
	if err != nil {
		return fmt.Errorf("memory: lock store: %w", err)
	}
	defer fl.Unlock()
	err = os.Remove(Path(root))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return NewMemoryStore(root).Clear()
}

// RemoveIndex deletes the entry at the given 1-based position in the
// order 'kern memory list' shows (newest first) and returns it, so the
// caller can show what went. QA F5: targeted removal — Clear stays the
// whole-store nuke. The removal is mirrored in the typed store (every
// entry with the same content) so the lesson stops rendering in buddy's
// "Project memory" too.
func RemoveIndex(root string, n int) (Entry, error) {
	mu.Lock()
	defer mu.Unlock()
	fl, err := cache.LockFile(Path(root)) // cross-process L5-3
	if err != nil {
		return Entry{}, fmt.Errorf("memory: lock store: %w", err)
	}
	defer fl.Unlock()
	s := Load(root)
	if n < 1 || n > len(s.Entries) {
		return Entry{}, fmt.Errorf("index %d out of range (1..%d) — see 'kern memory list'", n, len(s.Entries))
	}
	// The store keeps append (oldest-first) order; List reverses it for
	// display. Translate the list position into the stored position.
	idx := len(s.Entries) - n
	removed := s.Entries[idx]
	s.Entries = append(s.Entries[:idx], s.Entries[idx+1:]...)
	if err := writeJSON(Path(root), s); err != nil {
		return Entry{}, err
	}
	if _, err := NewMemoryStore(root).RemoveContent(removed.Text); err != nil {
		return Entry{}, err
	}
	return removed, nil
}

// RemovePrefix deletes the first stored entry whose text starts with the
// given prefix and returns it. Empty store or no match is an error. The
// typed store is mirrored too: every entry whose content starts with the
// prefix is removed (the typed store can hold several captures sharing the
// prefix, e.g. triplicated "Edited ..." snapshots).
func RemovePrefix(root, prefix string) (Entry, error) {
	mu.Lock()
	defer mu.Unlock()
	fl, err := cache.LockFile(Path(root)) // cross-process L5-3
	if err != nil {
		return Entry{}, fmt.Errorf("memory: lock store: %w", err)
	}
	defer fl.Unlock()
	s := Load(root)
	for i, e := range s.Entries {
		if strings.HasPrefix(e.Text, prefix) {
			removed := e
			s.Entries = append(s.Entries[:i], s.Entries[i+1:]...)
			if err := writeJSON(Path(root), s); err != nil {
				return Entry{}, err
			}
			if _, err := NewMemoryStore(root).RemovePrefix(prefix); err != nil {
				return Entry{}, err
			}
			return removed, nil
		}
	}
	return Entry{}, fmt.Errorf("no memory entry starts with %q — see 'kern memory list'", prefix)
}

// PruneStats reports what `kern memory prune --auto` removed.
type PruneStats struct {
	// TypedRemoved counts typed entries dropped for being Source "auto" or
	// auto-shaped content (raw prompts, tool outcomes, edited-file
	// snapshots, failed commands).
	TypedRemoved int
	// TypedCollapsed counts exact-duplicate typed entries reduced to their
	// most recent copy.
	TypedCollapsed int
	// V1Removed counts v1 entries dropped for being Source "auto".
	V1Removed int
	// V1Collapsed counts exact-duplicate v1 entries reduced to their most
	// recent copy.
	V1Collapsed int
}

// PruneAuto bulk-removes automatic captures from BOTH stores and collapses
// exact duplicates, keeping each distinct lesson's most recent copy — the
// cleanup path for the pre-F2 junk (failed commands, triplicated "Edited X"
// snapshots) and for auto captures generally.
//
// Typed store: entries with Source "auto" OR auto-shaped content
// (isAutoCapture) are removed — the auto-shaped check catches legacy
// captures written as Source "user" before F2 routing. v1 store: Source
// "auto" entries are removed (auto-shaped entries are already relabeled
// "auto" by Load's migration).
//
// Genuine lessons are never fully removed: after the auto removal, exact
// duplicates (identical content) collapse to their most recent copy, so a
// user/learning lesson is at worst deduplicated, never deleted.
func PruneAuto(root string) (PruneStats, error) {
	var stats PruneStats

	// Typed store (released before the v1 lock: the only nested ordering
	// in the package is v1-lock→typed-lock, kept deadlock-free).
	st := NewMemoryStore(root)
	tfl, err := cache.LockFile(st.path) // cross-process L5-3
	if err != nil {
		return stats, fmt.Errorf("memory: lock typed store: %w", err)
	}
	st.mu.Lock()
	ms := st.load()
	typedKept := make([]domain.Memory, 0, len(ms))
	for _, m := range ms {
		if m.Source == "auto" || (m.Source == "" && isAutoCapture(m.Content)) {
			stats.TypedRemoved++
			continue
		}
		typedKept = append(typedKept, m)
	}
	typedKept, stats.TypedCollapsed = collapseTypedDuplicates(typedKept)
	if len(typedKept) != len(ms) {
		if err := st.save(typedKept); err != nil {
			st.mu.Unlock()
			tfl.Unlock()
			return stats, err
		}
	}
	st.mu.Unlock()
	tfl.Unlock()

	// v1 store.
	mu.Lock()
	defer mu.Unlock()
	fl, err := cache.LockFile(Path(root)) // cross-process L5-3
	if err != nil {
		return stats, fmt.Errorf("memory: lock store: %w", err)
	}
	defer fl.Unlock()
	s := Load(root)
	v1Kept := make([]Entry, 0, len(s.Entries))
	for _, e := range s.Entries {
		if e.Source == "auto" {
			stats.V1Removed++
			continue
		}
		v1Kept = append(v1Kept, e)
	}
	v1Kept, stats.V1Collapsed = collapseEntryDuplicates(v1Kept)
	if len(v1Kept) != len(s.Entries) {
		if err := writeJSON(Path(root), Store{Root: root, Entries: v1Kept}); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

// collapseTypedDuplicates reduces exact-duplicate typed memories (identical
// Content) to their most recent copy, preserving order. The most recent copy
// is the one with the latest CreatedAt (ties: latest UpdatedAt, then first
// in slice). Entries with empty content are never collapsed.
func collapseTypedDuplicates(in []domain.Memory) ([]domain.Memory, int) {
	if len(in) == 0 {
		return nil, 0
	}
	best := map[string]int{} // content -> index of the most recent copy
	for i, m := range in {
		if m.Content == "" {
			continue
		}
		prev, ok := best[m.Content]
		if !ok || m.CreatedAt.After(in[prev].CreatedAt) ||
			(m.CreatedAt.Equal(in[prev].CreatedAt) && m.UpdatedAt.After(in[prev].UpdatedAt)) {
			best[m.Content] = i
		}
	}
	keep := map[int]bool{}
	for _, i := range best {
		keep[i] = true
	}
	collapsed := 0
	out := make([]domain.Memory, 0, len(in))
	for i, m := range in {
		if m.Content == "" || keep[i] {
			out = append(out, m)
		} else {
			collapsed++
		}
	}
	return out, collapsed
}

// collapseEntryDuplicates reduces exact-duplicate v1 entries (identical
// Text) to their most recent copy (latest Time), preserving order. Entries
// with empty text are never collapsed.
func collapseEntryDuplicates(in []Entry) ([]Entry, int) {
	if len(in) == 0 {
		return nil, 0
	}
	best := map[string]int{} // text -> index of the most recent copy
	for i, e := range in {
		if e.Text == "" {
			continue
		}
		prev, ok := best[e.Text]
		if !ok || e.Time.After(in[prev].Time) {
			best[e.Text] = i
		}
	}
	keep := map[int]bool{}
	for _, i := range best {
		keep[i] = true
	}
	collapsed := 0
	out := make([]Entry, 0, len(in))
	for i, e := range in {
		if e.Text == "" || keep[i] {
			out = append(out, e)
		} else {
			collapsed++
		}
	}
	return out, collapsed
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
		// Auto captures (raw prompts, tool outcomes) are never recalled: they
		// can carry PII and are session context, not project lessons (A17).
		if e.Source == "auto" {
			continue
		}
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
	// Deterministic ranking: score desc, then recency desc (newer first),
	// then the entry text as the final tiebreaker (Entry carries no ID field,
	// so the unique text is the deterministic key). O(n log n) sort.Slice
	// replaces the former O(n^2) bubble sort.
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].s != pool[j].s {
			return pool[i].s > pool[j].s
		}
		if !pool[i].e.Time.Equal(pool[j].e.Time) {
			return pool[i].e.Time.After(pool[j].e.Time)
		}
		return pool[i].e.Text < pool[j].e.Text
	})
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write (temp file + rename) so a concurrent reader never observes
	// a partially-written store.
	return fsutil.WriteFileAtomic(path, b, 0o600)
}
