package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// chainFile is the append-only JSON-lines file a LogStore writes. Every line
// is one entry: {"k":<key>,"v":<value>}.
const chainFile = "chain.jsonl"

// chainLine is the on-disk record for one LogStore entry.
type chainLine struct {
	K string          `json:"k"`
	V json.RawMessage `json:"v"`
}

// TailReader is implemented by stores that can return the most recently
// appended entry without listing (and re-reading) the whole store. The audit
// log uses it to refresh its tamper-evident chain head in O(1) instead of
// re-listing every persisted entry on each append.
type TailReader interface {
	// LastEntry returns the most recently appended entry, or ErrNotFound
	// when the store is empty.
	LastEntry(ctx context.Context) (Entry, error)
}

// LogStore is a hybrid key/value store: it reads legacy per-key JSON files
// (the LocalStore format, <dir>/<key>.json — e.g. audit-audit-1.json) written
// by older kern binaries, and writes new entries as append-only JSON lines in
// <dir>/chain.jsonl, so an append is O(1) no matter how many entries already
// exist. Values stay opaque JSON; callers own their own schema.
//
// Mixed-version caveat: an older kern binary still writes per-key .json files
// for the same directory. A key may therefore exist BOTH as a legacy file and
// as a chain.jsonl line — Get prefers the legacy file (it shadows the line),
// and List reports both. Keep only one writer format active per key; the
// audit layer serializes writers with its cross-process lock.
//
// Crash caveat: an append is a single O_APPEND write, so a crash mid-write
// can leave a partial trailing line. Readers skip lines that do not parse as
// {"k":...,"v":...}; the last complete entry is never lost.
type LogStore struct {
	dir string
}

// NewLog returns a LogStore rooted at dir. The directory is created lazily on
// the first write.
func NewLog(dir string) *LogStore {
	return &LogStore{dir: dir}
}

// Put appends one JSON line {"k":key,"v":value} to <dir>/chain.jsonl in O(1).
// A stale legacy per-key file for the same key is removed first so the fresh
// chain line is not shadowed by old data on Get/List.
func (s *LogStore) Put(ctx context.Context, key string, value json.RawMessage) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	// Drop a stale legacy shadow so the new line is authoritative.
	_ = os.Remove(filepath.Join(s.dir, key+".json"))

	f, err := os.OpenFile(filepath.Join(s.dir, chainFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(chainLine{K: key, V: value})
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := f.Write(line); err != nil {
		return err
	}
	return nil
}

// Get returns the value stored under key. A legacy per-key file wins over a
// chain.jsonl line; when only the chain holds the key, the LAST matching line
// is returned (an append-only log can hold several writes for one key).
func (s *LogStore) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if b, err := os.ReadFile(filepath.Join(s.dir, key+".json")); err == nil {
		return json.RawMessage(b), nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s.getChain(ctx, key)
}

// getChain scans chain.jsonl and returns the value of the last line whose key
// matches. Lines that do not parse are skipped (torn/corrupt writes).
func (s *LogStore) getChain(ctx context.Context, key string) (json.RawMessage, error) {
	f, err := os.Open(filepath.Join(s.dir, chainFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer f.Close()
	var found json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var cl chainLine
		if err := json.Unmarshal(sc.Bytes(), &cl); err != nil {
			continue
		}
		if cl.K == key {
			found = cl.V
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}

// List returns every stored entry: legacy per-key .json files first in
// LocalStore order (sorted by key), then chain.jsonl lines in file order.
func (s *LogStore) List(ctx context.Context) ([]Entry, error) {
	legacy, err := s.listLegacy(ctx)
	if err != nil {
		return nil, err
	}
	chain, err := s.listChain(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(legacy)+len(chain))
	out = append(out, legacy...)
	return append(out, chain...), nil
}

// listLegacy returns the per-key .json files (LocalStore format), sorted by
// key. In-progress .tmp files and chain.jsonl itself are excluded.
func (s *LogStore) listLegacy(ctx context.Context) ([]Entry, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Key: strings.TrimSuffix(name, ".json"), Value: json.RawMessage(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// listChain returns every chain.jsonl line in file order. Lines that do not
// parse are skipped (torn/corrupt writes).
func (s *LogStore) listChain(ctx context.Context) ([]Entry, error) {
	f, err := os.Open(filepath.Join(s.dir, chainFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var cl chainLine
		if err := json.Unmarshal(sc.Bytes(), &cl); err != nil {
			continue
		}
		out = append(out, Entry{Key: cl.K, Value: cl.V})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes the value stored under key: the legacy per-key file (if any)
// and every chain.jsonl line whose key matches. A missing key is not an
// error. The chain.jsonl rewrite is atomic (temp file + rename); callers that
// write concurrently from other processes must serialize writes (e.g. the
// audit log's cross-process lock) so a rename does not orphan in-flight
// appends.
func (s *LogStore) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.dir, key+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	path := filepath.Join(s.dir, chainFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var out []byte
	for _, line := range bytes.SplitAfter(data, []byte{'\n'}) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		var cl chainLine
		if err := json.Unmarshal(trimmed, &cl); err == nil && cl.K == key {
			continue // drop matching line
		}
		out = append(out, line...)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LastEntry returns the most recently appended entry: the last complete
// chain.jsonl line, read from a tail chunk near the end of the file (O(1)).
// When chain.jsonl is absent or holds no parseable line it falls back to
// scanning the legacy per-key files and returning the entry with the largest
// trailing numeric key suffix ("audit-audit-1096" beats "audit-audit-9",
// matching write order for audit-style keys); stores with no numeric-suffix
// keys fall back to key order. ErrNotFound is returned when the store is
// empty.
func (s *LogStore) LastEntry(ctx context.Context) (Entry, error) {
	if e, ok, err := s.lastChainLine(ctx); err != nil {
		return Entry{}, err
	} else if ok {
		return e, nil
	}
	return s.lastLegacyEntry(ctx)
}

// lastChainLine returns the last COMPLETE chain.jsonl line. A torn trailing
// segment (crash mid-append) and any corrupt line are skipped, so the result
// is the last line that parses as a chain record. ok=false when the file is
// absent or yields no parseable line.
func (s *LogStore) lastChainLine(ctx context.Context) (Entry, bool, error) {
	f, err := os.Open(filepath.Join(s.dir, chainFile))
	if err != nil {
		if os.IsNotExist(err) {
			return Entry{}, false, nil
		}
		return Entry{}, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Entry{}, false, err
	}
	if fi.Size() == 0 {
		return Entry{}, false, nil
	}
	// Read the tail chunk: enough to hold the last complete line plus any
	// partial line from a crashed write. Audit entries are far smaller than
	// this; a single line spanning the chunk is pathological and triggers the
	// full-scan fallback below.
	const tailChunk = 64 << 10
	n := int64(tailChunk)
	if fi.Size() < n {
		n = fi.Size()
	}
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, fi.Size()-n); err != nil && err != io.EOF {
		return Entry{}, false, err
	}
	// A file that does not end in '\n' has a torn final segment; drop it so
	// only complete lines are considered.
	if buf[len(buf)-1] != '\n' {
		if j := bytes.LastIndexByte(buf, '\n'); j >= 0 {
			buf = buf[:j+1]
		} else {
			buf = buf[:0]
		}
	}
	if len(buf) == 0 {
		// No newline in the tail chunk: the last line is longer than the
		// chunk, or the file is a single torn line. Full-scan fallback.
		return s.lastChainLineFull(ctx)
	}
	segments := bytes.Split(buf, []byte{'\n'})
	// The final element is empty (buf ends with '\n').
	for i := len(segments) - 2; i >= 0; i-- {
		trimmed := bytes.TrimSpace(segments[i])
		if len(trimmed) == 0 {
			continue
		}
		var cl chainLine
		if err := json.Unmarshal(trimmed, &cl); err != nil {
			continue
		}
		if i == 0 && n < fi.Size() {
			// The first segment of the chunk but the chunk starts mid-file:
			// this segment is a fragment of a longer line that began before
			// the chunk. Only trust it when the chunk covers the whole file.
			break
		}
		return Entry{Key: cl.K, Value: cl.V}, true, nil
	}
	return s.lastChainLineFull(ctx)
}

// lastChainLineFull scans the whole chain file and returns the last line that
// parses as a chain record. O(n); used only when the tail-chunk fast path
// cannot identify a clean last line.
func (s *LogStore) lastChainLineFull(ctx context.Context) (Entry, bool, error) {
	entries, err := s.listChain(ctx)
	if err != nil {
		return Entry{}, false, err
	}
	if len(entries) == 0 {
		return Entry{}, false, nil
	}
	return entries[len(entries)-1], true, nil
}

// lastLegacyEntry returns the legacy per-key entry with the largest trailing
// numeric key suffix (write order for audit-style keys), falling back to key
// order when no key has a numeric suffix.
func (s *LogStore) lastLegacyEntry(ctx context.Context) (Entry, error) {
	entries, err := s.listLegacy(ctx)
	if err != nil {
		return Entry{}, err
	}
	if len(entries) == 0 {
		return Entry{}, ErrNotFound
	}
	best := entries[0]
	for _, e := range entries[1:] {
		if compareKeysBySuffix(e.Key, best.Key) > 0 {
			best = e
		}
	}
	return best, nil
}

// compareKeysBySuffix orders keys by their trailing numeric suffix when the
// non-numeric prefixes match ("audit-audit-10" > "audit-audit-9"), falling
// back to plain lexical order.
func compareKeysBySuffix(a, b string) int {
	pa, na, oka := splitTrailingNum(a)
	pb, nb, okb := splitTrailingNum(b)
	if oka && okb && pa == pb {
		switch {
		case na < nb:
			return -1
		case na > nb:
			return 1
		}
		return 0
	}
	return strings.Compare(a, b)
}

// splitTrailingNum splits "audit-audit-1096" into ("audit-audit-", 1096).
func splitTrailingNum(s string) (prefix string, n int, ok bool) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) {
		return s, 0, false
	}
	n, err := strconv.Atoi(s[i:])
	if err != nil {
		return s, 0, false
	}
	return s[:i], n, true
}

// Interface health checks.
var _ Store = (*LogStore)(nil)
var _ TailReader = (*LogStore)(nil)
