// Package precache speculatively warms the on-disk caches (code summaries and
// the document vector index) in the background so interactive tools are fast
// when they run. It is a watch-style daemon: on each tick it scans the tree
// for files whose content hash is not yet cached and fills the gap.
package precache

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/brief"
	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// Report summarizes one warm pass.
type Report struct {
	Root        string
	Warmed      int    // summaries newly computed + cached
	CacheHits   int    // summaries already cached
	DocChunks   int    // document chunks indexed (docs/index saved)
	DocsSaved   bool   // doc index was persisted
	IndexStatus string // AST index after the pass: fresh | built | failed
	Dur         time.Duration
	SourceMiss  bool // root does not exist or is empty
}

var ignoreDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"vendor": true, "dist": true, "build": true, ".cache": true,
	".kern": true,
}

// Per-pass warm budgets bound how much work a single Warm pass can do so a
// huge file or a very large tree cannot cause unbounded memory/CPU/IO spikes.
// These mirror the per-file size and per-pass count caps used in fw.Detect.
const (
	// maxFileSize skips any single indexed file larger than 1MB so a giant
	// file cannot be read into memory (OOM guard) or dominate a pass.
	maxFileSize = 1 << 20
	// maxSourceFiles caps the number of source files read per Warm pass.
	maxSourceFiles = 2000
	// maxBytesPerPass caps the total bytes read across all files in a pass,
	// so sustained background warming stays within a bounded budget.
	maxBytesPerPass = 100 << 20
)

// errBudgetExceeded aborts the file walk once the per-pass file/byte budget is
// exhausted, so we do not keep traversing and reading a large tree needlessly.
var errBudgetExceeded = errors.New("precache: per-pass budget exceeded")

// Warm scans root once and fills any missing summary/doc caches.
func Warm(root string) *Report {
	rep := &Report{Root: root}
	start := time.Now()
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		rep.SourceMiss = true
		return rep
	}
	var mu sync.Mutex
	filesRead := 0
	bytesRead := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			if index.IgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if !index.QuickExt(rel) {
			return nil
		}
		// Per-pass budget: stop reading files once we have warmed enough
		// files or bytes this pass, so a large repo cannot sustain heavy
		// CPU/IO (Bug 2).
		if filesRead >= maxSourceFiles || bytesRead >= maxBytesPerPass {
			return errBudgetExceeded
		}
		// Size cap: skip files larger than 1MB so a huge file cannot OOM on
		// os.ReadFile (Bug 1).
		finfo, serr := d.Info()
		if serr != nil || finfo.Size() > maxFileSize {
			return nil
		}
		content, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		filesRead++
		bytesRead += len(content)
		h := cache.Hash(content)
		var cached code.Summary
		mu.Lock()
		defer mu.Unlock()
		if cache.Load("code/"+h, &cached) == nil {
			rep.CacheHits++
			return nil
		}
		sum := code.Summarize(rel, content, 200)
		_ = cache.Store("code/"+h, sum)
		rep.Warmed++
		return nil
	})
	// Documents: re-index only when the on-disk index is missing or stale.
	if ix := docsearch.Load(root); ix == nil {
		if nix, err := docsearch.IndexDir(root); err == nil && len(nix.Docs) > 0 {
			if serr := nix.Save(); serr == nil {
				rep.DocChunks = len(nix.Docs)
				rep.DocsSaved = true
			}
		}
	} else {
		rep.DocChunks = len(ix.Docs)
	}
	// AST index: build+persist when missing or stale so kern_buddy renders
	// the full digest on its next call instead of the cold pipeline.
	warmBefore := indexLoadsFresh(root)
	switch err := brief.Warm(root); {
	case err != nil:
		rep.IndexStatus = "failed"
	case warmBefore:
		rep.IndexStatus = "fresh"
	default:
		rep.IndexStatus = "built"
	}
	rep.Dur = time.Since(start)
	return rep
}

// indexLoadsFresh reports whether the AST index cache is present and current.
func indexLoadsFresh(root string) bool {
	ix, err := index.Load(root)
	return err == nil && ix != nil && !ix.Stale()
}

// Watch runs Warm every interval until stop is closed, emitting a Report per
// pass. It returns immediately after starting the goroutine.
func Watch(root string, interval time.Duration, stop <-chan struct{}) <-chan Report {
	out := make(chan Report)
	go func() {
		defer close(out)
		// Initial warm immediately. The send is guarded by the same select as
		// the loop so a stop signal fired while no one is reading cannot leave
		// the goroutine blocked forever on the first send.
		select {
		case out <- *Warm(root):
		case <-stop:
			return
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				r := *Warm(root)
				select {
				case out <- r:
				case <-stop:
					return
				}
			}
		}
	}()
	return out
}
