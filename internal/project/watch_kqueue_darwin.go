//go:build darwin

package project

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// kqueueWatcher is a zero-dependency native watcher backed by BSD kqueue.
// It registers EVFILT_VNODE on root and every descendant directory and fires
// on NOTE_WRITE|NOTE_DELETE|NOTE_RENAME|NOTE_EXTEND. New directories created
// after startup are picked up because the loop re-walks and re-registers
// periodically (5s) and right after every event batch.
type kqueueWatcher struct {
	kq        int
	fds       map[int]string // dir fd -> dir path
	files     map[int]string // file fd -> file path (content-edit watches)
	ch        chan struct{}
	closed    chan struct{} // Close closes this to tell the loop to exit
	done      chan struct{} // loop closes this when it has actually exited
	closeOnce sync.Once
}

func init() { nativeWatcherSupported = true }

func newNativeSource(root string) nativeSource {
	kq, err := syscall.Kqueue()
	if err != nil {
		return nil
	}
	w := &kqueueWatcher{
		kq:     kq,
		fds:    map[int]string{},
		files:  map[int]string{},
		ch:     make(chan struct{}, 1),
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go w.loop(root)
	return w
}

func (w *kqueueWatcher) Event() <-chan struct{} { return w.ch }

func (w *kqueueWatcher) Close() {
	w.closeOnce.Do(func() {
		close(w.closed) // tell the loop to exit
		<-w.done        // wait until the loop has actually stopped touching the maps
		syscall.Close(w.kq)
		for fd := range w.fds {
			syscall.Close(fd)
		}
		for fd := range w.files {
			syscall.Close(fd)
		}
		w.fds = map[int]string{}
		w.files = map[int]string{}
	})
}

func (w *kqueueWatcher) registerDir(p string) {
	fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_EVTONLY, 0)
	if err != nil {
		return
	}
	var evs = []syscall.Kevent_t{{
		Ident:  uint64(fd),
		Filter: syscall.EVFILT_VNODE,
		Flags:  syscall.EV_ADD | syscall.EV_CLEAR,
		Fflags: syscall.NOTE_WRITE | syscall.NOTE_DELETE | syscall.NOTE_RENAME | syscall.NOTE_EXTEND,
	}}
	if _, err := syscall.Kevent(w.kq, evs, nil, nil); err != nil {
		syscall.Close(fd)
		return
	}
	w.fds[fd] = p
}

// kqueueMaxFds bounds the total number of open kqueue vnode fds (one per
// watched dir + one per watched file). Content edits fire only for files
// with a registered watch; files beyond this cap are caught by the polling
// safety net, so a huge repo still converges within pollInterval.
const kqueueMaxFds = 4096

func (w *kqueueWatcher) registerFile(p string) {
	if len(w.fds)+len(w.files) >= kqueueMaxFds {
		return
	}
	fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_EVTONLY, 0)
	if err != nil {
		return
	}
	var evs = []syscall.Kevent_t{{
		Ident:  uint64(fd),
		Filter: syscall.EVFILT_VNODE,
		Flags:  syscall.EV_ADD | syscall.EV_CLEAR,
		Fflags: syscall.NOTE_WRITE | syscall.NOTE_DELETE | syscall.NOTE_RENAME | syscall.NOTE_EXTEND,
	}}
	if _, err := syscall.Kevent(w.kq, evs, nil, nil); err != nil {
		syscall.Close(fd)
		return
	}
	w.files[fd] = p
}

// reRegister walks the tree and adds watches for any directory or indexable
// file not yet known, closing fds for paths that disappeared. File watches
// (which catch content edits) are added until kqueueMaxFds is reached.
func (w *kqueueWatcher) reRegister(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil // skip unreadable subtrees
		}
		if d.IsDir() {
			if index.IgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			if _, ok := w.knownDir(p); !ok {
				w.registerDir(p)
			}
			return nil
		}
		if !isIndexablePath(p) {
			return nil
		}
		if _, ok := w.knownFile(p); !ok {
			w.registerFile(p)
		}
		return nil
	})
	// Drop fds for dirs and files that disappeared (kept O_RDONLY would leak).
	for fd, p := range w.fds {
		if _, err := os.Stat(p); err != nil {
			syscall.Close(fd)
			delete(w.fds, fd)
		}
	}
	for fd, p := range w.files {
		if _, err := os.Stat(p); err != nil {
			syscall.Close(fd)
			delete(w.files, fd)
		}
	}
}

func (w *kqueueWatcher) knownDir(p string) (string, bool) {
	for _, q := range w.fds {
		if q == p {
			return q, true
		}
	}
	return "", false
}

func (w *kqueueWatcher) knownFile(p string) (string, bool) {
	for _, q := range w.files {
		if q == p {
			return q, true
		}
	}
	return "", false
}

func (w *kqueueWatcher) loop(root string) {
	defer close(w.done) // signal Close() that the loop has stopped touching the maps
	w.reRegister(root)
	events := make([]syscall.Kevent_t, 32)
	nextRereg := time.Now().Add(2 * time.Second)
	timeout := &syscall.Timespec{Nsec: 500 * 1e6} // wake every 500ms to re-register
	for {
		select {
		case <-w.closed:
			return
		default:
		}
		n, err := syscall.Kevent(w.kq, nil, events, timeout)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			select {
			case <-w.closed:
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}
		fired := false
		for i := 0; i < n; i++ {
			if events[i].Filter == syscall.EVFILT_VNODE &&
				events[i].Fflags&(syscall.NOTE_WRITE|syscall.NOTE_DELETE|syscall.NOTE_RENAME|syscall.NOTE_EXTEND) != 0 {
				fired = true
			}
		}
		if fired {
			select {
			case w.ch <- struct{}{}:
			default:
			}
		}
		// Re-register periodically so directories created after startup are
		// picked up even when nothing below them has changed.
		if time.Now().After(nextRereg) {
			w.reRegister(root)
			nextRereg = time.Now().Add(2 * time.Second)
		}
	}
}
