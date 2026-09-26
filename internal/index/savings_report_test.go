package index

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// TestTokenSavingsReport is the machine-generated, reproducible token-savings
// report for kern's compact context modes (graph / context / neighborhood)
// versus the naive baseline of pasting the FULL source of every file the
// query touches.
//
// Two run modes:
//
//	# (re)write docs/benchmarks/token-savings.md with freshly computed numbers
//	go test ./internal/index -run TestTokenSavingsReport -update
//	# (or: KERN_UPDATE_GOLDEN=1 go test ./internal/index -run TestTokenSavingsReport)
//
//	# validate: recompute everything, compare against the embedded golden
//	# checksum of the numeric payload, and verify the stored doc matches
//	go test ./internal/index -run TestTokenSavingsReport -count=1
//
// When the plain run fails because the numbers changed (fixture edit,
// tokenizer change, extraction change), rerun with -update to regenerate the
// doc, then copy the new sha256 it logs into goldenNumbersSHA256 below.
var updateGolden = flag.Bool("update", false, "rewrite docs/benchmarks/token-savings.md with freshly computed numbers")

// goldenNumbersSHA256 is the sha256 of the canonical numeric payload of the
// report (per-symbol x per-mode rows + per-mode aggregates + overall row —
// no date/HEAD lines, so it is stable across commits). Regenerate with:
//
//	go test ./internal/index -run TestTokenSavingsReport -update
//
// and copy the logged value here.
const goldenNumbersSHA256 = "2566ac3251d3150342c6051f6fab2d99918dd23fb339eef5c72047aeac1ca247"

const reportRelPath = "docs/benchmarks/token-savings.md"

// reportModes are the three compact-context modes under test. They must match
// the mode labels used by compactFor.
var reportModes = []string{"graph", "context", "neighborhood"}

// hubSymbols are the representative "hub" symbols of the fixture: public lib
// symbols that are both called from other packages and call into lib helpers,
// so every mode has a non-trivial neighborhood (definition + callers +
// callees across several files).
var hubSymbols = []string{"NewStore", "Store.Save", "Store.Get", "LoadConfig", "Cache.Put"}

// savingsFixtureFiles is the deterministic in-test fixture: a small
// multi-package Go service (lib hub package + app consumers + main driver),
// 60-150 lines per file, written to t.TempDir() and indexed with index.Build
// on every run. Files must stay syntactically valid Go (kern parses with
// go/ast; it does not type-check or compile them).
var savingsFixtureFiles = map[string]string{
	"go.mod": `module example.com/bench

go 1.23
`,
	"lib/logger.go": `package lib

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// LogLevel controls how much the logger emits. Higher levels are quieter.
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

// ParseLevel converts a string name into a LogLevel. Unknown names fall
// back to LevelInfo so a misconfigured deployment stays operational.
func ParseLevel(name string) LogLevel {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	case "info", "":
		return LevelInfo
	default:
		return LevelInfo
	}
}

// Logger is a small leveled logger that writes structured lines to w. It
// is safe for concurrent use from multiple goroutines.
type Logger struct {
	mu    sync.Mutex
	w     io.Writer
	level LogLevel
}

// NewLogger builds a Logger that writes to w at the given level.
func NewLogger(w io.Writer, level LogLevel) *Logger {
	return &Logger{w: w, level: level}
}

// log writes one line when lvl passes the configured threshold.
func (l *Logger) log(lvl LogLevel, msg string, kv ...string) {
	if lvl < l.level {
		return
	}
	var b strings.Builder
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString(" level=")
	b.WriteString(levelName(lvl))
	b.WriteString(" msg=")
	b.WriteString(msg)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(" ")
		b.WriteString(kv[i])
		b.WriteString("=")
		b.WriteString(kv[i+1])
	}
	b.WriteString("\n")
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = io.WriteString(l.w, b.String())
}

// Debug logs at debug level.
func (l *Logger) Debug(msg string, kv ...string) { l.log(LevelDebug, msg, kv...) }

// Info logs at info level.
func (l *Logger) Info(msg string, kv ...string) { l.log(LevelInfo, msg, kv...) }

// Warn logs at warn level.
func (l *Logger) Warn(msg string, kv ...string) { l.log(LevelWarn, msg, kv...) }

// Error logs at error level.
func (l *Logger) Error(msg string, kv ...string) { l.log(LevelError, msg, kv...) }

// Errorf formats and logs at error level.
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.log(LevelError, fmt.Sprintf(format, args...))
}

// levelName returns the canonical name of a LogLevel.
func levelName(lvl LogLevel) string {
	switch lvl {
	case LevelDebug:
		return "debug"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

// WithFields is a compatibility shim that returns a logger sharing the
// writer and level; the fixture does not use key/value chaining yet.
func (l *Logger) WithFields(kv ...string) *Logger {
	_ = kv
	return l
}
`,
	"lib/cache.go": `package lib

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// Cache is a small thread-safe bounded key/value cache with LRU-style
// recency ordering. When full, the least-recently used entry is evicted.
type Cache struct {
	mu     sync.Mutex
	cap    int
	items  map[string]string
	order  []string
	hits   int
	misses int
}

// NewCache returns an empty cache holding at most cap entries. A cap of
// zero disables caching entirely (every Get misses).
func NewCache(cap int) *Cache {
	if cap < 0 {
		cap = 0
	}
	return &Cache{cap: cap, items: map[string]string{}}
}

// Put stores value under key, evicting the oldest entry when the cache is
// full. Re-inserting an existing key refreshes its recency.
func (c *Cache) Put(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cap == 0 {
		return
	}
	if _, ok := c.items[key]; ok {
		c.removeKey(key)
	}
	c.items[key] = value
	c.order = append(c.order, key)
	if len(c.order) > c.cap {
		evict(c)
	}
}

// Get returns the value stored under key and whether it was present,
// updating recency on a hit.
func (c *Cache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[key]
	if !ok {
		c.misses++
		return "", false
	}
	c.hits++
	c.removeKey(key)
	c.order = append(c.order, key)
	return v, true
}

// Len reports how many entries are currently cached.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Stats returns cumulative hit and miss counts since the cache was built.
func (c *Cache) Stats() (hits, misses int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}

// removeKey drops key from the recency list without touching items.
func (c *Cache) removeKey(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// evict drops the oldest entry from a full cache.
func evict(c *Cache) {
	if len(c.order) == 0 {
		return
	}
	oldest := c.order[0]
	c.order = c.order[1:]
	delete(c.items, oldest)
}

// hashKey returns a stable hex digest used to namespace composite keys so
// store-level keys never collide with cache-level keys.
func hashKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
`,
	"lib/config.go": `package lib

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Config holds the runtime knobs for a Store instance.
type Config struct {
	Addr      string
	DataDir   string
	LogLevel  string
	CacheSize int
	MaxBytes  int64
	ReadOnly  bool
}

// DefaultConfig returns a Config with production-sane defaults. Callers
// may override individual fields before passing it to NewStore.
func DefaultConfig() Config {
	return Config{
		Addr:      ":8080",
		DataDir:   "./data",
		LogLevel:  "info",
		CacheSize: 1024,
		MaxBytes:  1 << 20,
		ReadOnly:  false,
	}
}

// LoadConfig reads a key=value config file and layers it over the
// defaults. Unknown keys are ignored so newer files stay forward
// compatible with older binaries.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cfg = applyKey(cfg, strings.TrimSpace(k), strings.TrimSpace(v))
	}
	if err := sc.Err(); err != nil {
		return cfg, err
	}
	return sanitize(cfg), nil
}

// applyKey layers one parsed key onto cfg, ignoring unknown keys.
func applyKey(cfg Config, k, v string) Config {
	switch k {
	case "addr":
		cfg.Addr = v
	case "data_dir":
		cfg.DataDir = v
	case "log_level":
		cfg.LogLevel = v
	case "cache_size":
		if n, err := strconv.Atoi(v); err == nil {
			cfg.CacheSize = n
		}
	case "max_bytes":
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.MaxBytes = n
		}
	case "read_only":
		cfg.ReadOnly = strings.EqualFold(v, "true")
	}
	return cfg
}

// sanitize clamps out-of-range fields so a hostile file cannot crash the
// process with an absurd allocation.
func sanitize(cfg Config) Config {
	if cfg.CacheSize < 0 {
		cfg.CacheSize = 0
	}
	if cfg.MaxBytes < 0 {
		cfg.MaxBytes = 0
	}
	return cfg
}
`,
	"lib/store.go": `package lib

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Store is the hub of the benchmark fixture: a key/value store layered
// over a cache and a logger. Most of the codebase funnels through it.
type Store struct {
	mu     sync.RWMutex
	cfg    Config
	cache  *Cache
	log    *Logger
	data   map[string]string
	opened bool
}

// NewStore validates cfg and wires together the cache, logger and data
// map. The returned store is closed until Open is called.
func NewStore(cfg Config, cache *Cache, log *Logger) (*Store, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	s := &Store{
		cfg:   cfg,
		cache: cache,
		log:   log,
		data:  map[string]string{},
	}
	log.Info("store constructed", "cache_size", fmt.Sprint(cfg.CacheSize))
	return s, nil
}

// Open marks the store ready for reads and writes.
func (s *Store) Open() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opened {
		return errors.New("store already open")
	}
	s.opened = true
	s.log.Info("store opened", "data_dir", s.cfg.DataDir)
	return nil
}

// Close flushes and marks the store closed.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.opened {
		return errors.New("store not open")
	}
	s.opened = false
	s.log.Info("store closed")
	return nil
}

// Save writes value under key, caching the composite key for fast reads.
func (s *Store) Save(key, value string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	s.mu.Lock()
	if s.cfg.ReadOnly {
		s.mu.Unlock()
		return errors.New("store is read-only")
	}
	s.data[key] = value
	s.mu.Unlock()
	ck := hashKey("kv", key)
	s.cache.Put(ck, value)
	s.log.Info("saved", "key", key)
	return nil
}

// Get reads value under key, consulting the cache first.
func (s *Store) Get(key string) (string, bool) {
	ck := hashKey("kv", key)
	if v, ok := s.cache.Get(ck); ok {
		s.log.Debug("cache hit", "key", key)
		return v, true
	}
	s.mu.RLock()
	v, ok := s.data[key]
	s.mu.RUnlock()
	if ok {
		s.cache.Put(ck, v)
	}
	s.log.Debug("cache miss", "key", key)
	return v, ok
}

// Snapshot returns a copy of all keys sorted, for diagnostics.
func (s *Store) Snapshot() []string {
	s.mu.RLock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	s.mu.RUnlock()
	sort.Strings(keys)
	return keys
}

// CacheStats proxies the underlying cache counters.
func (s *Store) CacheStats() (hits, misses int) {
	return s.cache.Stats()
}

// validate rejects configs that would misbehave at runtime.
func validate(cfg Config) error {
	if cfg.CacheSize < 0 {
		return errors.New("negative cache size")
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		return errors.New("empty data dir")
	}
	return nil
}

// validateKey rejects empty or oversized keys.
func validateKey(key string) error {
	if key == "" {
		return errors.New("empty key")
	}
	if len(key) > 4096 {
		return fmt.Errorf("key too long: %d", len(key))
	}
	return nil
}
`,
	"app/server.go": `package app

import (
	"fmt"
	"net/http"
	"strings"

	"example.com/bench/lib"
)

// Server exposes the store over HTTP. It is one of the two main callers
// of the lib package in the fixture.
type Server struct {
	store *lib.Store
	log   *lib.Logger
	addr  string
}

// NewServer wires an HTTP server around a store.
func NewServer(store *lib.Store, log *lib.Logger, addr string) *Server {
	return &Server{store: store, log: log, addr: addr}
}

// HandleGet serves a single key lookup.
func (s *Server) HandleGet(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/get/")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	v, ok := s.store.Get(key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = fmt.Fprintln(w, v)
	s.log.Info("served get", "key", key)
}

// HandleSave accepts a key=value body and stores it.
func (s *Server) HandleSave(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/save/")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	value := strings.TrimSpace(r.URL.Query().Get("value"))
	if err := s.store.Save(key, value); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
	s.log.Info("served save", "key", key)
}

// HandleStats reports store and cache counters.
func (s *Server) HandleStats(w http.ResponseWriter, r *http.Request) {
	keys := s.store.Snapshot()
	hits, misses := s.store.CacheStats()
	_, _ = fmt.Fprintf(w, "keys=%d hits=%d misses=%d\n", len(keys), hits, misses)
	s.log.Warn("stats served", "keys", fmt.Sprint(len(keys)))
}

// HandleHealth reports that the server is up.
func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = fmt.Fprintln(w, "ok")
}

// Serve runs the HTTP listener until the context is cancelled.
func (s *Server) Serve() error {
	s.log.Info("listening", "addr", s.addr)
	return http.ListenAndServe(s.addr, nil)
}
`,
	"app/handlers.go": `package app

import (
	"strings"

	"example.com/bench/lib"
)

// UserHandler implements the user-facing API on top of the store, using
// the cache directly for fast profile lookups.
type UserHandler struct {
	store *lib.Store
	cache *lib.Cache
	log   *lib.Logger
}

// NewUserHandler builds a handler from shared dependencies.
func NewUserHandler(store *lib.Store, cache *lib.Cache, log *lib.Logger) *UserHandler {
	return &UserHandler{store: store, cache: cache, log: log}
}

// Register stores a user profile under the normalized user id.
func (h *UserHandler) Register(userID, profile string) error {
	id := normalizeID(userID)
	if err := h.store.Save("user:"+id, profile); err != nil {
		return err
	}
	h.cache.Put("profile:"+id, profile)
	h.log.Info("registered user", "user", id)
	return nil
}

// Lookup returns a user profile, preferring the cache.
func (h *UserHandler) Lookup(userID string) (string, bool) {
	id := normalizeID(userID)
	if v, ok := h.cache.Get("profile:" + id); ok {
		return v, true
	}
	v, ok := h.store.Get("user:" + id)
	if ok {
		h.cache.Put("profile:"+id, v)
	}
	return v, ok
}

// Delete removes a user profile from the store. The fixture store has no
// delete primitive, so it overwrites the entry with an empty tombstone.
func (h *UserHandler) Delete(userID string) error {
	id := normalizeID(userID)
	return h.store.Save("user:"+id, "")
}

// Touch refreshes the cache entry for an existing user.
func (h *UserHandler) Touch(userID string) {
	id := normalizeID(userID)
	if v, ok := h.store.Get("user:" + id); ok {
		h.cache.Put("profile:"+id, v)
	}
}

// Count reports how many users are registered.
func (h *UserHandler) Count() int {
	return len(h.store.Snapshot())
}

// normalizeID lowercases and trims a user id for canonical storage.
func normalizeID(userID string) string {
	return strings.ToLower(strings.TrimSpace(userID))
}
`,
	"app/wiring.go": `package app

import (
	"os"

	"example.com/bench/lib"
)

// Version is the fixture wiring contract version, bumped whenever the
// dependency shape of the app package changes. Consumers may record it
// alongside their own version for compatibility reporting.
const Version = 1

// BuildDeps assembles the store, cache and logger from a config. It is
// the composition root of the app package: every other path (DefaultDeps,
// RunServer, tests) funnels through it so the wiring stays consistent.
func BuildDeps(cfg lib.Config) (*lib.Store, *lib.Cache, *lib.Logger, error) {
	log := lib.NewLogger(os.Stdout, lib.ParseLevel(cfg.LogLevel))
	cache := lib.NewCache(cfg.CacheSize)
	store, err := lib.NewStore(cfg, cache, log)
	if err != nil {
		return nil, nil, nil, err
	}
	return store, cache, log, nil
}

// DefaultDeps builds dependencies from DefaultConfig, useful for tests
// and local runs.
func DefaultDeps() (*lib.Store, *lib.Cache, *lib.Logger, error) {
	return BuildDeps(lib.DefaultConfig())
}

// WireReport describes one dependency wiring for diagnostics.
type WireReport struct {
	StoreCap int
	CacheCap int
	Level    string
}

// InspectDeps returns a WireReport describing a built dependency set.
func InspectDeps(store *lib.Store, cache *lib.Cache, log *lib.Logger) WireReport {
	_ = store
	_ = cache
	_ = log
	return WireReport{StoreCap: 1, CacheCap: 1, Level: "info"}
}

// RunServer wires dependencies, constructs the HTTP server and handlers,
// and returns the wiring error (if any). The fixture does not serve.
func RunServer(cfg lib.Config) error {
	store, cache, log, err := BuildDeps(cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	server := NewServer(store, log, cfg.Addr)
	handlers := NewUserHandler(store, cache, log)
	report := InspectDeps(store, cache, log)
	_ = server
	_ = handlers
	_ = report
	log.Info("dependencies wired")
	return nil
}
`,
	"main/main.go": `package main

import (
	"fmt"
	"os"

	"example.com/bench/lib"
)

// Runner drives a store session against the benchmark fixture: it opens
// the store, writes and reads a handful of records, and closes it.
type Runner struct {
	store *lib.Store
	cache *lib.Cache
	log   *lib.Logger
}

// NewRunner constructs the runner's dependencies.
func NewRunner(cfg lib.Config) (*Runner, error) {
	log := lib.NewLogger(os.Stdout, lib.ParseLevel(cfg.LogLevel))
	cache := lib.NewCache(cfg.CacheSize)
	store, err := lib.NewStore(cfg, cache, log)
	if err != nil {
		return nil, err
	}
	return &Runner{store: store, cache: cache, log: log}, nil
}

// Run exercises the store: save, get, snapshot and cache stats.
func (r *Runner) Run() error {
	if err := r.store.Open(); err != nil {
		return err
	}
	defer r.store.Close()
	records := map[string]string{
		"alpha": "first value",
		"beta":  "second value",
		"gamma": "third value",
	}
	for k, v := range records {
		if err := r.store.Save(k, v); err != nil {
			return err
		}
	}
	if v, ok := r.store.Get("alpha"); ok {
		r.log.Info("read back", "key", "alpha", "value", v)
	}
	r.cache.Put("session", "active")
	if v, ok := r.cache.Get("session"); ok {
		r.log.Debug("session", "state", v)
	}
	keys := r.store.Snapshot()
	r.log.Info("snapshot", "count", fmt.Sprint(len(keys)))
	return nil
}

// main loads configuration and runs one session.
func main() {
	cfg, err := lib.LoadConfig(defaultConfigPath())
	if err != nil {
		cfg = lib.DefaultConfig()
	}
	runner, err := NewRunner(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := runner.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// defaultConfigPath returns the conventional config location, overridable
// through the BENCH_CONFIG environment variable.
func defaultConfigPath() string {
	if p := os.Getenv("BENCH_CONFIG"); p != "" {
		return p
	}
	return "bench.conf"
}
`,
}

// sample is one measured row: a symbol queried through one mode.
type sample struct {
	symbol  string
	mode    string
	naive   int // tokens in the concatenated full source of every touched file
	compact int // tokens in kern's compact output for that mode
	savings int // floor((naive - compact) / naive * 100)
	defFile string
	defFull int // tokens of the definition file's full source (helper cross-check)
}

// TestTokenSavingsReport computes the token-savings report and either writes
// it (-update / KERN_UPDATE_GOLDEN=1) or validates the stored report against
// the embedded golden checksum.
func TestTokenSavingsReport(t *testing.T) {
	dir := writeTree(t, savingsFixtureFiles)
	ix, err := Build(dir)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	update := *updateGolden || os.Getenv("KERN_UPDATE_GOLDEN") == "1"

	var samples []sample
	perMode := map[string][2]int{} // mode -> {totalNaive, totalCompact}
	for _, sym := range hubSymbols {
		g, ok := ix.Neighborhood(sym)
		if !ok {
			t.Fatalf("Neighborhood(%q) not found — fixture symbol missing", sym)
		}
		// The fixture is designed so every hub has def + callers + callees:
		// guard against a fixture regression that silently empties a
		// neighborhood (the report would still "pass" with trivial numbers).
		if len(g.Nodes) < 3 {
			t.Fatalf("Neighborhood(%q) has only %d nodes; want >= 3 (def + callers + callees)", sym, len(g.Nodes))
		}
		if files := distinctNodeFiles(g); len(files) < 2 {
			t.Fatalf("Neighborhood(%q) touches %d file(s); want >= 2", sym, len(files))
		}

		// Naive baseline: the concatenated FULL source of every file the
		// query touches — exactly what a naive agent would paste. Mirrors
		// TokenSavingsForNeighborhood's file walk (same node order).
		naiveText := concatTouchedFiles(ix, g)
		naiveTokens := tokenize.Count(naiveText)
		if naiveTokens <= 0 {
			t.Fatalf("%s: naive baseline is 0 tokens", sym)
		}

		defs := ix.symbolsFor(sym)
		if len(defs) == 0 {
			t.Fatalf("%s: no symbols", sym)
		}
		defFile := defs[0].File
		defFull := 0
		if data, err := os.ReadFile(filepath.Join(ix.Root, defFile)); err == nil {
			defFull = tokenize.Count(string(data))
		}

		for _, mode := range reportModes {
			compact := compactFor(ix, sym, g, mode)
			if compact == "" {
				t.Fatalf("%s/%s: compact output is empty", sym, mode)
			}
			compactTokens := tokenize.Count(compact)
			st := computeTokenSavings(naiveText, compact, mode)
			// Internal consistency: the stats engine must agree with a
			// direct recount, and savings must be non-negative and exact.
			if st.FullContext != naiveTokens {
				t.Fatalf("%s/%s: computeTokenSavings full=%d, want %d", sym, mode, st.FullContext, naiveTokens)
			}
			if st.CompactTokens != compactTokens {
				t.Fatalf("%s/%s: computeTokenSavings compact=%d, want %d", sym, mode, st.CompactTokens, compactTokens)
			}
			if naiveTokens > 0 {
				want := int(float64(naiveTokens-compactTokens) / float64(naiveTokens) * 100)
				if st.SavingsPct != want {
					t.Fatalf("%s/%s: savings=%d%%, want %d%%", sym, mode, st.SavingsPct, want)
				}
			}
			if compactTokens < 0 || naiveTokens < 0 {
				t.Fatalf("%s/%s: negative token count (compact=%d naive=%d)", sym, mode, compactTokens, naiveTokens)
			}
			samples = append(samples, sample{
				symbol: sym, mode: mode,
				naive: naiveTokens, compact: compactTokens, savings: st.SavingsPct,
				defFile: defFile, defFull: defFull,
			})
			acc := perMode[mode]
			acc[0] += naiveTokens
			acc[1] += compactTokens
			perMode[mode] = acc
		}

		// Helper cross-checks: the production helpers must agree with the
		// report's computations on their own baselines.
		if nbh := ix.TokenSavingsForNeighborhood(g); nbh.FullContext != naiveTokens {
			t.Fatalf("%s: TokenSavingsForNeighborhood full=%d, want %d", sym, nbh.FullContext, naiveTokens)
		}
		graphOut := ix.Graph(sym)
		if gs := ix.TokenSavingsForGraph(defFile, graphOut); gs.FullContext != defFull {
			t.Fatalf("%s: TokenSavingsForGraph full=%d, want %d (def-file-only baseline)", sym, gs.FullContext, defFull)
		}
		ctxOut := ix.Context(sym, 12)
		if cs := ix.TokenSavingsForContext(defFile, ctxOut); cs.FullContext != defFull {
			t.Fatalf("%s: TokenSavingsForContext full=%d, want %d (def-file-only baseline)", sym, cs.FullContext, defFull)
		}
	}

	// Aggregates.
	totalNaive, totalCompact := 0, 0
	for _, s := range samples {
		totalNaive += s.naive
		totalCompact += s.compact
	}
	overall := savingsPct(totalNaive, totalCompact)

	// Canonical numeric payload (rows + per-mode aggregates + overall), the
	// thing goldenNumbersSHA256 anchors. Deliberately excludes the date and
	// git-HEAD lines so the golden stays valid across commits.
	var payload strings.Builder
	rows := make([]string, 0, len(samples))
	for _, s := range samples {
		rows = append(rows, fmt.Sprintf("| %s | %s | %d | %d | %d%% |", s.symbol, s.mode, s.naive, s.compact, s.savings))
		fmt.Fprintf(&payload, "%s|%s|%d|%d|%d\n", s.symbol, s.mode, s.naive, s.compact, s.savings)
	}
	for _, mode := range reportModes {
		acc := perMode[mode]
		fmt.Fprintf(&payload, "AGG|%s|%d|%d|%d\n", mode, acc[0], acc[1], savingsPct(acc[0], acc[1]))
	}
	fmt.Fprintf(&payload, "OVERALL|%d|%d|%d\n", totalNaive, totalCompact, overall)
	payloadSHA := sha256Hex(payload.String())

	report := renderReport(ix, samples, rows, perMode, totalNaive, totalCompact, overall, payloadSHA)

	root := repoRootFrom(t)
	docPath := filepath.Join(root, filepath.FromSlash(reportRelPath))

	if update {
		if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(docPath, []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d lines)", docPath, strings.Count(report, "\n"))
		t.Logf("NEXT numeric sha256 for goldenNumbersSHA256: %s", payloadSHA)
		t.Logf("aggregate savings: overall=%d%%  graph=%d%%  context=%d%%  neighborhood=%d%%",
			overall, savingsPct(perMode["graph"][0], perMode["graph"][1]),
			savingsPct(perMode["context"][0], perMode["context"][1]),
			savingsPct(perMode["neighborhood"][0], perMode["neighborhood"][1]))
		return
	}

	// Plain mode: validate the stored report against the golden checksum.
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("report not generated yet — run `go test ./internal/index -run TestTokenSavingsReport -update` first: %v", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		t.Fatalf("stored report %s is empty", docPath)
	}
	content := string(data)
	if goldenNumbersSHA256 == "" {
		t.Fatalf("goldenNumbersSHA256 is empty — run with -update and copy the logged sha256 into the constant")
	}
	if payloadSHA != goldenNumbersSHA256 {
		t.Fatalf("recomputed numbers no longer match the golden checksum:\n  got  %s\n  want %s\nrun `go test ./internal/index -run TestTokenSavingsReport -update` and refresh goldenNumbersSHA256",
			payloadSHA, goldenNumbersSHA256)
	}
	if !strings.Contains(content, "**Numeric checksum (sha256):** "+goldenNumbersSHA256) {
		t.Fatalf("stored report's checksum line does not match the golden checksum %s", goldenNumbersSHA256)
	}
	for _, row := range rows {
		if !strings.Contains(content, row) {
			t.Fatalf("stored report is missing row %q — regenerate with -update", row)
		}
	}
	t.Logf("report OK: %s (%d lines)", docPath, strings.Count(content, "\n"))
}

// compactFor returns kern's actual compact output for one mode.
func compactFor(ix *Index, sym string, g GraphResult, mode string) string {
	switch mode {
	case "graph":
		return ix.Graph(sym)
	case "context":
		return ix.Context(sym, 12)
	case "neighborhood":
		return g.GraphJSON()
	}
	return ""
}

// concatTouchedFiles concatenates the full source of every unique file the
// neighborhood references, in node order — the naive baseline. This is the
// same walk TokenSavingsForNeighborhood performs, so the two must agree.
func concatTouchedFiles(ix *Index, g GraphResult) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, n := range g.Nodes {
		if n.File != "" && !seen[n.File] {
			seen[n.File] = true
			if data, err := os.ReadFile(filepath.Join(ix.Root, n.File)); err == nil {
				b.Write(data)
			}
		}
	}
	return b.String()
}

// distinctNodeFiles returns the sorted set of files referenced by a graph.
func distinctNodeFiles(g GraphResult) []string {
	seen := map[string]bool{}
	for _, n := range g.Nodes {
		if n.File != "" {
			seen[n.File] = true
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func savingsPct(naive, compact int) int {
	if naive <= 0 {
		return 0
	}
	return int(float64(naive-compact) / float64(naive) * 100)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// renderReport renders the full markdown document. The date and git HEAD
// lines are informational only; the golden checksum anchors the numeric
// payload (rows + aggregates), which is stable across commits.
func renderReport(ix *Index, samples []sample, rows []string, perMode map[string][2]int, totalNaive, totalCompact, overall int, payloadSHA string) string {
	head := gitHead()
	var b strings.Builder
	fmt.Fprintf(&b, "# kern Token Savings Report — compact context vs naive full-file context\n\n")
	fmt.Fprintf(&b, "**Status:** machine-generated · **Suite:** TS-001 · **Generation date:** %s\n", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&b, "**Git HEAD:** %s\n", head)
	fmt.Fprintf(&b, "**Tokenizer:** `internal/tokenize.Count` (deterministic offline BPE, cl100k_base)\n")
	fmt.Fprintf(&b, "**Numeric checksum (sha256):** %s\n", payloadSHA)
	fmt.Fprintf(&b, "**Reproduce:** `go test ./internal/index -run TestTokenSavingsReport -update`\n\n")

	b.WriteString("## 1. Methodology\n\n")
	b.WriteString("- **Fixture:** a deterministic in-test tree — `lib` (hub package: store, cache, config, logger), `app` (HTTP server + user handlers + wiring), `main` (driver). Written to `t.TempDir()` and indexed with `index.Build` on every run; sources live in `internal/index/savings_report_test.go` (`savingsFixtureFiles`).\n")
	fmt.Fprintf(&b, "- **Queries:** %d hub symbols × %d modes = %d samples. Hub symbols: `%s`.\n", len(hubSymbols), len(reportModes), len(samples), strings.Join(hubSymbols, "`, `"))
	b.WriteString("- **Modes:** `graph` (`Index.Graph`), `context` (`Index.Context(sym, 12)`), `neighborhood` (`GraphResult.GraphJSON` via `Index.Neighborhood`).\n")
	b.WriteString("- **Naive baseline:** the concatenated FULL source of every file the query touches (definition, caller and callee files — what a naive agent would paste). File set and order mirror `TokenSavingsForNeighborhood`'s walk, so the neighborhood baseline equals that helper exactly; graph/context use the same all-touched-files baseline (stricter than their def-file-only helper baseline).\n")
	b.WriteString("- **Counter:** `internal/tokenize.Count` — the same deterministic counter kern's `TokenSavingsFor*` helpers use.\n")
	b.WriteString("- **Savings:** `floor((naive − compact) / naive × 100)`, computed by `computeTokenSavings` (the engine behind all three helpers); the compact output counted includes kern's appended stats summary line where the API emits one.\n")
	b.WriteString("- **Reproduction (one command):**\n\n```\ngo test ./internal/index -run TestTokenSavingsReport -update   # write the report\ngo test ./internal/index -run TestTokenSavingsReport -count=1  # validate against the golden checksum\n```\n\n")

	b.WriteString("## 2. Fixture\n\n")
	b.WriteString("| file | package | approx lines | role |\n")
	b.WriteString("|---|---|---|---|\n")
	names := make([]string, 0, len(savingsFixtureFiles))
	for name := range savingsFixtureFiles {
		if strings.HasSuffix(name, ".go") {
			names = append(names, name)
		}
	}
	sortStrings(names)
	for _, name := range names {
		content := savingsFixtureFiles[name]
		pkg := strings.TrimPrefix(content, "package ")
		pkg = strings.SplitN(pkg, "\n", 2)[0]
		lines := strings.Count(content, "\n")
		role := fixtureRole(name)
		fmt.Fprintf(&b, "| `%s` | `%s` | %d | %s |\n", name, pkg, lines, role)
	}
	b.WriteString("\n")

	b.WriteString("## 3. Per-symbol × per-mode results\n\n")
	b.WriteString("| symbol | mode | naive tokens | compact tokens | savings % |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, row := range rows {
		b.WriteString(row)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	b.WriteString("## 4. Aggregates\n\n")
	b.WriteString("| mode | naive tokens | compact tokens | savings % |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, mode := range reportModes {
		acc := perMode[mode]
		fmt.Fprintf(&b, "| %s | %d | %d | %d%% |\n", mode, acc[0], acc[1], savingsPct(acc[0], acc[1]))
	}
	fmt.Fprintf(&b, "| **overall** | **%d** | **%d** | **%d%%** |\n\n", totalNaive, totalCompact, overall)

	// Verdict, derived from the measured numbers.
	minMode := 100
	minModeName := ""
	for _, mode := range reportModes {
		if p := savingsPct(perMode[mode][0], perMode[mode][1]); p < minMode {
			minMode = p
			minModeName = mode
		}
	}
	surgical := 0
	for _, s := range samples {
		if s.savings >= 80 {
			surgical++
		}
	}
	backed := overall >= 50 && minMode >= 40
	b.WriteString("## 5. Verdict\n\n")
	fmt.Fprintf(&b, "For this fixture class — a small multi-package Go service (lib hub + app + main consumers), %d samples — the claim of **substantial savings** is **%s**.\n\n",
		len(samples), verdictWord(backed))
	fmt.Fprintf(&b, "- Overall savings: **%d%%** (%d → %d tokens).\n", overall, totalNaive, totalCompact)
	fmt.Fprintf(&b, "- Per-mode: graph %d%%, context %d%%, neighborhood %d%% (weakest mode: %s at %d%%).\n",
		savingsPct(perMode["graph"][0], perMode["graph"][1]),
		savingsPct(perMode["context"][0], perMode["context"][1]),
		savingsPct(perMode["neighborhood"][0], perMode["neighborhood"][1]),
		minModeName, minMode)
	fmt.Fprintf(&b, "- **Surgical context**: compact output stays at or below 20%% of the naive full-file paste (≥80%% savings) in **%d of %d** samples.\n", surgical, len(samples))
	if backed {
		b.WriteString("- kern's graph/context/neighborhood outputs replace multi-file full-source pastes with a fraction of the tokens while retaining the same symbol, caller and callee information. On this fixture the savings claim is quantitative, not marketing.\n")
	} else {
		b.WriteString("- The measured savings did not reach the 50% overall / 40% per-mode bar on this fixture. Investigate before claiming substantial savings for this fixture class.\n")
	}
	return b.String()
}

func verdictWord(backed bool) string {
	if backed {
		return "BACKED"
	}
	return "NOT BACKED"
}

// fixtureRole describes each fixture file's part in the benchmark.
func fixtureRole(rel string) string {
	switch rel {
	case "lib/store.go":
		return "hub: Store struct, NewStore, Save/Get/Open/Close + helpers"
	case "lib/cache.go":
		return "Cache + eviction, called by Store and app"
	case "lib/config.go":
		return "Config, DefaultConfig, LoadConfig + parser helpers"
	case "lib/logger.go":
		return "Logger + ParseLevel, called everywhere"
	case "app/server.go":
		return "HTTP Server calling Store methods via field chain"
	case "app/handlers.go":
		return "UserHandler calling Store/Cache via field chain"
	case "app/wiring.go":
		return "composition root: BuildDeps/DefaultDeps/RunServer"
	case "main/main.go":
		return "driver: Runner + main(), direct lib consumer"
	}
	return ""
}

// repoRootFrom locates the repository root (the directory containing go.mod)
// by walking up from the test binary's working directory (the package dir).
func repoRootFrom(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root (go.mod) from %s", dir)
	return ""
}

// gitHead returns the current HEAD commit, or "unknown" when git is
// unavailable (e.g. the tree is not a checkout).
func gitHead() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
