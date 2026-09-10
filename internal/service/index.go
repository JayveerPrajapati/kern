package service

import (
	"context"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/project"
)

// IndexService centralizes symbol-index operations: building, loading,
// inspecting status, and watching for changes. It is the single entry point
// for every delivery mechanism that needs the code index.
type IndexService interface {
	// Build creates or refreshes the symbol index for root and returns it.
	Build(ctx context.Context, root string) (*index.Index, error)
	// Load reads the cached index for root without rebuilding. It returns the
	// index even when stale; callers decide whether freshness matters.
	Load(ctx context.Context, root string) (*index.Index, error)
	// LoadOrBuild returns the cached index when it is fresh, otherwise
	// rebuilds it. This is the behavior `kern graph`/`kern search` use.
	LoadOrBuild(ctx context.Context, root string) (*index.Index, error)
	// Status reports the cached index's health without mutating anything
	// (read-only, CI-safe). strict selects a full content re-hash freshness
	// proof over the fast git tree-OID compare.
	Status(ctx context.Context, root string, strict bool) (*IndexStatus, error)
	// Watch monitors root and re-indexes on change, invoking onChange with
	// each refresh. It blocks until ctx is cancelled; onError (when non-nil)
	// receives non-fatal watch errors.
	Watch(ctx context.Context, root string, interval time.Duration, onChange func(WatchEvent), onError func(error)) error
}

// IndexStatus is a JSON-ready snapshot of a cached index's health, mirroring
// the fields `kern index --status [--json]` reports.
type IndexStatus struct {
	Root              string               `json:"root"`
	SchemaVersion     string               `json:"schema_version"`
	Built             bool                 `json:"built"`
	Symbols           int                  `json:"symbols"`
	Files             int                  `json:"files"`
	Packages          int                  `json:"packages"`
	Version           int                  `json:"version"`
	Stale             bool                 `json:"stale"`
	Languages         []string             `json:"languages"`
	Store             string               `json:"store"`
	PrecisionByLang   map[string]string    `json:"precision_by_lang,omitempty"`
	FreshnessProof    index.FreshnessProof `json:"freshness_proof,omitempty"`
	IndexIdentity     *index.IndexIdentity `json:"index_identity,omitempty"`
	TreeSitterEnabled bool                 `json:"tree_sitter_enabled"`
	SQLite            bool                 `json:"sqlite_enabled"`
}

// WatchEvent carries one re-index result from IndexService.Watch.
type WatchEvent struct {
	Changes []index.Change
	Index   *index.Index
}

// indexService is the default IndexService implementation: a thin facade over
// the internal/index and internal/project engines.
type indexService struct{}

func newIndexService() *indexService { return &indexService{} }

func resolveRoot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

func (s *indexService) Build(ctx context.Context, root string) (*index.Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	// Persist like `kern index` so Status/Load observe the build in other
	// processes and the SQLite store stays in sync when enabled.
	if err := ix.Save(); err != nil {
		return nil, err
	}
	if index.SQLiteEnabled() {
		if err := index.SaveSQLite(root, ix); err != nil {
			return nil, err
		}
	}
	return ix, nil
}

func (s *indexService) Load(ctx context.Context, root string) (*index.Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return index.Load(resolveRoot(root))
}

func (s *indexService) LoadOrBuild(ctx context.Context, root string) (*index.Index, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return index.LoadOrBuild(resolveRoot(root))
}

func (s *indexService) Status(ctx context.Context, root string, strict bool) (*IndexStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root = resolveRoot(root)
	status := &IndexStatus{
		Root:              root,
		SchemaVersion:     "2",
		Built:             false,
		Stale:             true,
		Languages:         []string{},
		PrecisionByLang:   map[string]string{},
		TreeSitterEnabled: index.TreesitterEnabled(),
		SQLite:            index.SQLiteEnabled(),
	}
	ix, err := index.Load(root)
	if err != nil || ix == nil {
		return status, nil // not built yet — not an error, just a fact
	}
	status.Built = true
	status.Symbols = len(ix.Symbols)
	status.Files = len(ix.FileHashes)
	status.Packages = len(ix.Pkgs)
	status.Version = ix.Version
	status.Stale = ix.Stale()
	status.Languages = ix.Languages()
	status.PrecisionByLang = ix.PrecisionByLang
	if status.SQLite {
		status.Store = index.SQLitePath(root)
	} else {
		status.Store = index.StorePath(root)
	}
	proof := ix.FreshnessProof(root)
	if strict {
		proof = ix.FreshnessProofStrict(root)
	}
	status.FreshnessProof = proof
	if ix.Identity != nil {
		status.IndexIdentity = ix.Identity
	}
	return status, nil
}

func (s *indexService) Watch(ctx context.Context, root string, interval time.Duration, onChange func(WatchEvent), onError func(error)) error {
	root = resolveRoot(root)
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return project.Watch(ctx, root, interval, func(changes []index.Change, ix *index.Index) {
		if onChange != nil {
			onChange(WatchEvent{Changes: changes, Index: ix})
		}
	}, onError)
}
