package mutation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Crash-safe journaling for mutation evaluation.
//
// Before a mutant is written to a real source file, the original content is
// backed up under <root>/.kern/mutation-backup/<ts>-<pid>/ and recorded in a
// per-run journal <root>/.kern/mutation-journal-<pid>.json (PID-suffixed so
// concurrent runs never clobber each other's crash record). Every exit path —
// normal return, error, panic — restores all journaled files via defer, and
// SIGINT/SIGTERM install a handler that restores everything before re-raising
// the signal. A stale journal left behind by a killed process is restored by
// the next Run before any new mutation happens (self-heal).
//
// Ordering is crash-safe: backup file is written first, then the journal entry
// is persisted (atomically: temp + rename), and only then is the mutant
// written. A crash before the journal persist leaves the real file untouched;
// a crash after it leaves a journal entry that the next run restores.

const (
	journalPrefix   = "mutation-journal-"
	journalSuffix   = ".json"
	backupDirName   = "mutation-backup"
	mutationKernDir = ".kern"
)

// journalEntry records one temporarily-modified file: the real absolute path
// and the absolute path of the backup holding the original content.
type journalEntry struct {
	AbsPath string `json:"abs_path"`
	Backup  string `json:"backup"`
}

// mutationJournal is the crash-safe record of files a Run has modified.
type mutationJournal struct {
	PID       int            `json:"pid"`
	Started   string         `json:"started"`
	Root      string         `json:"root"`
	BackupDir string         `json:"backup_dir"`
	Entries   []journalEntry `json:"entries"`

	path  string         // <root>/.kern/mutation-journal-<pid>.json
	index map[string]int // abs path -> index in Entries

	// mu guards Entries/index across add (the Run goroutine), the
	// SIGINT/SIGTERM restore goroutine, and persist, so a signal arriving
	// mid-run never reads a half-updated slice (oracle gate R3).
	mu sync.Mutex
}

func journalPathFor(root string) string {
	return filepath.Join(root, mutationKernDir, fmt.Sprintf("%s%d%s", journalPrefix, os.Getpid(), journalSuffix))
}

// newMutationJournal creates a fresh journal and its backup directory.
func newMutationJournal(root string) (*mutationJournal, error) {
	j := &mutationJournal{
		PID:       os.Getpid(),
		Started:   time.Now().UTC().Format(time.RFC3339),
		Root:      root,
		BackupDir: filepath.Join(root, mutationKernDir, backupDirName, fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())),
		path:      journalPathFor(root),
		index:     map[string]int{},
	}
	if err := os.MkdirAll(j.BackupDir, 0o755); err != nil {
		return nil, fmt.Errorf("mutation journal: create backup dir: %w", err)
	}
	return j, nil
}

// add backs up orig to the backup dir (once per path) and appends the entry,
// then persists the journal atomically. Must be called BEFORE the mutant is
// written to the real file.
func (j *mutationJournal) add(absPath string, orig []byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.index[absPath]; ok {
		return nil
	}
	rel, err := filepath.Rel(j.Root, absPath)
	if err != nil {
		rel = filepath.Base(absPath)
	}
	bp := filepath.Join(j.BackupDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(bp), 0o755); err != nil {
		return fmt.Errorf("mutation journal: create backup for %s: %w", absPath, err)
	}
	if err := os.WriteFile(bp, orig, 0o644); err != nil {
		return fmt.Errorf("mutation journal: write backup for %s: %w", absPath, err)
	}
	j.index[absPath] = len(j.Entries)
	j.Entries = append(j.Entries, journalEntry{AbsPath: absPath, Backup: bp})
	return j.persistLocked()
}

// persistLocked atomically rewrites the journal file (temp + rename) so a
// crash never leaves a half-written journal. Caller must hold j.mu.
func (j *mutationJournal) persistLocked() error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, j.path)
}

// restore copies every journaled original back into place. It is idempotent
// and leaves the backups in place so a later restore (or the next run's
// self-heal) still works. Returns the number of files restored and the first
// error encountered, if any.
func (j *mutationJournal) restore() (int, error) {
	// Snapshot under the lock: the SIGINT/SIGTERM goroutine may restore
	// while the Run goroutine is mid-add (oracle gate R3). The copy is taken
	// so file I/O happens without holding the lock.
	j.mu.Lock()
	entries := make([]journalEntry, len(j.Entries))
	copy(entries, j.Entries)
	j.mu.Unlock()
	restored := 0
	var errs []string
	for _, e := range entries {
		orig, err := os.ReadFile(e.Backup)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", e.AbsPath, err))
			continue
		}
		if err := os.WriteFile(e.AbsPath, orig, 0o644); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", e.AbsPath, err))
			continue
		}
		restored++
	}
	if len(errs) > 0 {
		return restored, fmt.Errorf("restore %d file(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return restored, nil
}

// cleanup removes the journal file and this journal's backup tree. Call after
// a successful restore.
func (j *mutationJournal) cleanup() {
	_ = os.Remove(j.path)
	_ = os.RemoveAll(j.BackupDir)
	// Remove the now-empty parent mutation-backup/ dir. os.Remove only
	// succeeds on an empty directory, so a concurrent run's backup subdir
	// keeps the parent in place.
	_ = os.Remove(filepath.Dir(j.BackupDir))
}

// loadMutationJournal reads a journal file (a stale one left by a crashed
// run, or one produced by persist) back from disk.
func loadMutationJournal(path string) (*mutationJournal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var j mutationJournal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("parse journal %s: %w", path, err)
	}
	j.path = path
	j.index = map[string]int{}
	for i, e := range j.Entries {
		j.index[e.AbsPath] = i
	}
	return &j, nil
}

// recoverStaleJournals restores every file left modified by an interrupted
// previous mutation run: any <root>/.kern/mutation-journal-*.json plus its
// backup tree. Journals owned by a LIVE process (a concurrent mutate run in
// the same repo) are skipped — restoring them would clobber their on-disk
// mutants mid-run and cleanup would delete their backups (oracle gate R2).
// Journals whose restore fully succeeds are removed; journals with failures
// are kept so a later run can retry. Returns the total number of files
// restored and an error aggregating any failures.
func recoverStaleJournals(root string) (int, error) {
	matches, err := filepath.Glob(filepath.Join(root, mutationKernDir, journalPrefix+"*"+journalSuffix))
	if err != nil || len(matches) == 0 {
		return 0, nil
	}
	total := 0
	var errs []string
	for _, p := range matches {
		if strings.HasSuffix(p, ".tmp") {
			continue
		}
		j, lerr := loadMutationJournal(p)
		if lerr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p, lerr))
			continue
		}
		if processAlive(j.PID) {
			// A live concurrent mutation run owns this journal — leave it
			// and its backups strictly alone (oracle gate R2).
			continue
		}
		n, rerr := j.restore()
		total += n
		if rerr != nil {
			// Keep the journal + backups for a later retry.
			errs = append(errs, fmt.Sprintf("%s: %v", p, rerr))
			continue
		}
		j.cleanup()
	}
	if len(errs) > 0 {
		return total, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return total, nil
}

// processAlive reports whether pid belongs to a running process. It is
// deliberately conservative: EPERM (exists, not ours to signal) counts as
// alive, and any unexpected probe error counts as alive too — self-heal
// must never clobber a journal whose owner might still be running.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return true // could not even look it up — assume the worst
	}
	if err := p.Signal(syscall.Signal(0)); err == nil {
		return true
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM) {
		return true // exists but not ours to signal
	}
	return false
}

// Signal-safe restore (audit C2): SIGINT/SIGTERM restore every active
// journal's files before the process dies. Handlers are installed lazily on
// the first Run so packages that never mutate stay signal-clean.
var (
	signalSetupOnce sync.Once
	activeMu        sync.Mutex
	activeJournals  = map[*mutationJournal]struct{}{}
)

func ensureSignalRestore() {
	signalSetupOnce.Do(func() {
		ch := make(chan os.Signal, 2)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		go func() {
			s := <-ch
			activeMu.Lock()
			journals := make([]*mutationJournal, 0, len(activeJournals))
			for j := range activeJournals {
				journals = append(journals, j)
			}
			activeMu.Unlock()
			for _, j := range journals {
				if _, rerr := j.restore(); rerr != nil {
					fmt.Fprintf(os.Stderr, "mutation: interrupt restore failed: %v\n", rerr)
				}
				// No cleanup here: deleting backups while the Run goroutine
				// may be mid-add would strand a mutant with a journal entry
				// pointing at a deleted backup (oracle gate R3). The
				// journal + backups are left in place for the next run's
				// self-heal — restore is idempotent.
				activeMu.Lock()
				delete(activeJournals, j)
				activeMu.Unlock()
			}
			// Re-raise with default disposition so the process dies with the
			// conventional 128+signal exit code.
			signal.Stop(ch)
			if p, perr := os.FindProcess(os.Getpid()); perr == nil {
				_ = p.Signal(s)
			}
			time.Sleep(200 * time.Millisecond)
			os.Exit(1)
		}()
	})
}

func (j *mutationJournal) register() {
	activeMu.Lock()
	activeJournals[j] = struct{}{}
	activeMu.Unlock()
}

func (j *mutationJournal) unregister() {
	activeMu.Lock()
	delete(activeJournals, j)
	activeMu.Unlock()
}
