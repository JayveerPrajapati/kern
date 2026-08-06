// Package precache speculatively warms the on-disk caches (code summaries and
// the document vector index) in the background so interactive tools are fast
// when they run (#20). It is a watch-style daemon: on each tick it scans the
// tree for files whose content hash is not yet cached and fills the gap.
package precache

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/docsearch"
)

// Report summarizes one warm pass.
type Report struct {
	Root       string
	Warmed     int  // summaries newly computed + cached
	CacheHits  int  // summaries already cached
	DocChunks  int  // document chunks indexed (docs/index saved)
	DocsSaved  bool // doc index was persisted
	Dur        time.Duration
	SourceMiss bool // root does not exist or is empty
}

var ignoreDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"vendor": true, "dist": true, "build": true, ".cache": true,
}

var sourceExts = map[string]bool{
	".go": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".py": true, ".rb": true, ".rs": true, ".java": true, ".c": true,
	".h": true, ".cpp": true, ".cs": true, ".php": true, ".swift": true,
	".kt": true, ".sh": true, ".pl": true, ".lua": true,
}

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
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil || rel == "." {
			return nil
		}
		if d.IsDir() {
			if ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if !sourceExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		content, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
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
	rep.Dur = time.Since(start)
	return rep
}

// Watch runs Warm every interval until stop is closed, emitting a Report per
// pass. It returns immediately after starting the goroutine.
func Watch(root string, interval time.Duration, stop <-chan struct{}) <-chan Report {
	out := make(chan Report)
	go func() {
		defer close(out)
		// Initial warm immediately.
		out <- *Warm(root)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				out <- *Warm(root)
			}
		}
	}()
	return out
}
