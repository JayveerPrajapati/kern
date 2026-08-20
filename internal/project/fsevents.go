package project

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// fileWatcher wraps a file-event source to receive near-real-time
// notifications when source files change. The underlying source is, in
// preference order: a native kqueue/inotify watcher (pure stdlib, no deps), a
// host OS file-event tool (inotifywait on Linux, fswatch on macOS), or nil —
// where nil means the polling-based Stale() check in index.Stale() serves as
// the fallback.
type fileWatcher struct {
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
	notify   func()
}

// newFileWatcher starts a background file watcher for root. The notify
// callback is called (via Invalidate) when a source file is created,
// modified, or deleted. It prefers the external inotifywait/fswatch tool;
// when unavailable it returns nil and the polling fallback is used. The
// stdlib-native kqueue/inotify watcher is used by the long-lived
// kern watch command instead of per-session watchers, because opening one
// kqueue/inotify fd per directory would exhaust file descriptors when many
// sessions exist (each MCP tool server owns one).
func newFileWatcher(root string, notify func()) *fileWatcher {
	name, args := lookupWatcherCmd(root)
	if name == "" {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	fw := &fileWatcher{
		ctx:    ctx,
		cancel: cancel,
		notify: notify,
	}
	fw.wg.Add(1)
	go fw.run(ctx, name, args, root)
	return fw
}

// WatcherCommand returns the name and args of the native file-event watcher
// tool that would be used for root, or an error when no native tool is
// available on this platform. Useful for callers that want to report which
// watching mode is active.
func WatcherCommand(root string) (string, []string, error) {
	name, args := lookupWatcherCmd(root)
	if name == "" {
		return "", nil, os.ErrNotExist
	}
	return name, args, nil
}

// lookupWatcherCmd returns the command and args to run for file-event watching,
// or empty strings when no native tool is available for the current platform.
func lookupWatcherCmd(root string) (string, []string) {
	switch runtime.GOOS {
	case "linux":
		if p, err := exec.LookPath("inotifywait"); err == nil {
			return p, []string{"-m", "-r", "--format", "%w%f", "-e", "modify", "-e", "create", "-e", "delete", root}
		}
		if p, err := exec.LookPath("fswatch"); err == nil {
			return p, []string{"-0", "-e", ".*", "-i", sourceExtRegex(), root}
		}
	case "darwin":
		if p, err := exec.LookPath("fswatch"); err == nil {
			return p, []string{"-0", "-e", ".*", "-i", sourceExtRegex(), root}
		}
	}
	return "", nil
}

// sourceExtRegex returns a regex matching the extensions kern indexes.
func sourceExtRegex() string {
	return `\.(go|py|js|mjs|cjs|jsx|ts|tsx|vue|svelte|astro|css|scss|less|html|md|mdx|markdown|json|jsonc|yml|yaml|rs|c|h|cc|cpp|cxx|hpp|hxx|cs|java|rb|php|sh|bash)$`
}

// run executes the watcher command and debounces file-change events.
func (fw *fileWatcher) run(ctx context.Context, name string, args []string, root string) {
	defer fw.wg.Done()
	byLine := strings.Contains(filepath.Base(name), "inotifywait")
	cmd := exec.CommandContext(ctx, name, args...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}

	var (
		debounce   time.Timer
		debounceMu sync.Mutex
		hasPending bool
		reset      = func() {
			debounce.Stop()
			debounce = *time.AfterFunc(200*time.Millisecond, func() {
				debounceMu.Lock()
				defer debounceMu.Unlock()
				if hasPending {
					hasPending = false
					fw.notify()
				}
			})
			hasPending = true
		}
	)
	debounce = *time.AfterFunc(0, func() {})
	debounce.Stop()

	reader := bufio.NewReader(pipe)
	for {
		var path string
		if byLine {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			path = strings.TrimSpace(line)
		} else {
			path, err := reader.ReadString(0)
			if err != nil {
				return
			}
			path = strings.Trim(path, "\x00")
		}
		if path == "" {
			continue
		}
		if !isIndexablePath(path) {
			continue
		}
		debounceMu.Lock()
		reset()
		debounceMu.Unlock()
	}
}

// isIndexablePath checks whether a changed file is one kern would index.
func isIndexablePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range indexableExts {
		if ext == e {
			return true
		}
	}
	return false
}

var indexableExts = []string{
	".go", ".py", ".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx",
	".vue", ".svelte", ".astro",
	".css", ".scss", ".less", ".html", ".htm",
	".md", ".mdx", ".markdown",
	".json", ".jsonc",
	".yml", ".yaml",
	".rs", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hxx",
	".cs", ".java", ".rb", ".php", ".sh", ".bash",
}

// Stop terminates the watcher process.
func (fw *fileWatcher) Stop() {
	if fw == nil {
		return
	}
	fw.stopOnce.Do(func() {
		fw.cancel()
		fw.wg.Wait()
	})
}
