//go:build linux

package project

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// inotifyWatcher is a zero-dependency native watcher backed by Linux inotify.
// It registers a watch on root and every descendant directory and fires on
// IN_MODIFY/IN_CREATE/IN_DELETE/IN_MOVED_TO|FROM. New directories created
// after startup are caught by periodic re-registration (5s) plus a
// re-register right after each fired batch.
type inotifyWatcher struct {
	fd        int
	dirs      map[int]string // watch descriptor -> dir path
	files     map[int]string // watch descriptor -> file path (content edits)
	ch        chan string
	closed    chan struct{} // Close closes this to tell the loop to exit
	done      chan struct{} // loop closes this when it has actually exited
	closeOnce sync.Once
}

func init() { nativeWatcherSupported = true }

func newNativeSource(root string) nativeSource {
	fd, err := syscall.InotifyInit1(syscall.IN_NONBLOCK)
	if err != nil {
		return nil
	}
	w := &inotifyWatcher{
		fd:     fd,
		dirs:   map[int]string{},
		files:  map[int]string{},
		ch:     make(chan string, 16),
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go w.loop(root)
	return w
}

func (w *inotifyWatcher) Event() <-chan string { return w.ch }

func (w *inotifyWatcher) Close() {
	w.closeOnce.Do(func() {
		close(w.closed)
		<-w.done // wait until the loop has actually stopped touching the maps
		syscall.Close(w.fd)
		w.dirs = map[int]string{}
		w.files = map[int]string{}
	})
}

func (w *inotifyWatcher) registrationMask() uint32 {
	return syscall.IN_MODIFY | syscall.IN_CREATE | syscall.IN_DELETE |
		syscall.IN_MOVED_TO | syscall.IN_MOVED_FROM | syscall.IN_CLOSE_WRITE | syscall.IN_ONLYDIR
}

// reRegister walks the tree and adds watches for any directory or indexable
// file not yet known, removing watches for paths that disappeared. File
// watches carry IN_MODIFY/IN_CLOSE_WRITE so a save to an existing file fires
// natively instead of waiting for the polling fallback.
func (w *inotifyWatcher) reRegister(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			if index.IgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			if !w.watchedDir(p) {
				wd, err := syscall.InotifyAddWatch(w.fd, p, w.registrationMask())
				if err == nil {
					w.dirs[wd] = p
				}
			}
			return nil
		}
		// File: watch content edits too (IN_MODIFY/IN_CLOSE_WRITE) so a save
		// to an existing file is delivered natively instead of waiting for
		// the polling fallback.
		if !isIndexablePath(p) {
			return nil
		}
		if !w.watchedFile(p) {
			wd, err := syscall.InotifyAddWatch(w.fd, p, syscall.IN_MODIFY|syscall.IN_CLOSE_WRITE|syscall.IN_DELETE|syscall.IN_MOVED_FROM|syscall.IN_MOVED_TO)
			if err == nil {
				w.files[wd] = p
			}
		}
		return nil
	})
	// Drop watches for dirs and files that disappeared.
	for wd, p := range w.dirs {
		if _, err := os.Stat(p); err != nil {
			_, _ = syscall.InotifyRmWatch(w.fd, uint32(wd))
			delete(w.dirs, wd)
		}
	}
	for wd, p := range w.files {
		if _, err := os.Stat(p); err != nil {
			_, _ = syscall.InotifyRmWatch(w.fd, uint32(wd))
			delete(w.files, wd)
		}
	}
}

func (w *inotifyWatcher) watchedDir(p string) bool {
	for _, q := range w.dirs {
		if q == p {
			return true
		}
	}
	return false
}

func (w *inotifyWatcher) watchedFile(p string) bool {
	for _, q := range w.files {
		if q == p {
			return true
		}
	}
	return false
}

// eventPath resolves the file a fired inotify event refers to: for a file
// watch the registered file path, for a directory watch the directory path
// joined with the event's name (the created/deleted/renamed entry). name is
// the raw NUL-terminated name bytes following the event header.
func (w *inotifyWatcher) eventPath(ev *syscall.InotifyEvent, name []byte) string {
	if p, ok := w.files[int(ev.Wd)]; ok {
		return p
	}
	p, ok := w.dirs[int(ev.Wd)]
	if !ok {
		return ""
	}
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	if len(name) == 0 {
		return p
	}
	return filepath.Join(p, string(name))
}

func (w *inotifyWatcher) loop(root string) {
	defer close(w.done)       // signal Close() that the loop has stopped touching the maps
	buf := make([]byte, 4096) // events are read in a loop; large enough for a burst
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.closed:
			return
		case <-ticker.C:
			w.reRegister(root)
		default:
		}
		n, err := syscall.Read(w.fd, buf)
		if err != nil {
			select {
			case <-w.closed:
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		fired := false
		var paths []string
		for off := 0; off+syscall.SizeofInotifyEvent <= n; {
			ev := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[off]))
			if ev.Mask&(syscall.IN_MODIFY|syscall.IN_CREATE|syscall.IN_DELETE|
				syscall.IN_MOVED_TO|syscall.IN_MOVED_FROM|syscall.IN_CLOSE_WRITE) != 0 {
				fired = true
				nameLen := int(ev.Len)
				if nameLen > n-off-syscall.SizeofInotifyEvent {
					nameLen = n - off - syscall.SizeofInotifyEvent
				}
				if p := w.eventPath(ev, buf[off+syscall.SizeofInotifyEvent:off+syscall.SizeofInotifyEvent+nameLen]); p != "" {
					paths = append(paths, p)
				}
			}
			off += syscall.SizeofInotifyEvent + int(ev.Len)
		}
		if fired {
			for _, p := range paths {
				if rel, rerr := filepath.Rel(root, p); rerr == nil {
					p = rel
				}
				select {
				case w.ch <- p:
				default:
				}
			}
			w.reRegister(root)
		}
	}
}
