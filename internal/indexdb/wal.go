package indexdb

import (
	"database/sql"
	"encoding/binary"
	"os"
)

// WAL valve: bounds the write-ahead log for the incremental
// watch-daemon/MCP write pattern (many small writes over a long-lived
// process). wal_autocheckpoint is disabled at open, so SQLite's default
// 1000-page autocheckpoint never fires on its own; Checkpoint runs a PASSIVE
// checkpoint only once the live WAL crosses WalTrigger, which keeps the
// per-write check cheap and never blocks readers or writers. Bulk builds wrap
// the whole index in one transaction and are checkpointed at that transaction
// boundary.
const (
	// WalSoftCap is the target ceiling for the WAL: 8 MiB.
	WalSoftCap = 8 << 20
	// WalTrigger is the size at which Checkpoint fires: 2 x WalSoftCap.
	WalTrigger = 2 * WalSoftCap // 16 MiB
)

// WalLiveFrames returns the number of uncheckpointed WAL frames by reading
// the WAL-index header in the -shm file (mxFrame at offset 16 minus nBackfill
// at offset 96; layout per https://www.sqlite.org/walformat.html, frozen since
// SQLite 3.7.0). modernc.org/sqlite does not expose PRAGMA wal_pages/wal_size,
// so this is the only side-effect-free live-WAL probe the store has. A
// negative value means the file is missing or unreadable (checkpoint to be
// safe).
func WalLiveFrames(storePath string) int64 {
	f, err := os.Open(storePath + "-shm")
	if err != nil {
		return -1
	}
	defer func() { _ = f.Close() }()
	var hdr [100]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		return -1
	}
	mx := int64(binary.LittleEndian.Uint32(hdr[16:20]))
	nb := int64(binary.LittleEndian.Uint32(hdr[96:100]))
	if mx < nb {
		return -1 // torn read (concurrent checkpoint): treat as unreadable
	}
	return mx - nb
}

// Checkpoint runs a passive WAL checkpoint once the write-ahead log exceeds
// WalTrigger. Call it after a write commits. PASSIVE checkpoints as much as it
// can without blocking; when another connection holds a read lock it returns
// busy and the next write re-checks. A nil, closed, or WAL-less store is a
// no-op.
func Checkpoint(db *sql.DB, storePath string, closed bool) error {
	if db == nil || closed {
		return nil
	}
	// Cheap disk gate: the live WAL can exceed WalTrigger only once the -wal
	// file's high-water size reaches it (SQLite never truncates it while the
	// store is open), so a small file proves the WAL is small too.
	st, err := os.Stat(storePath + "-wal")
	if err != nil {
		return nil // no WAL file yet (fresh store)
	}
	if st.Size() < WalTrigger {
		return nil
	}
	// Big file may be a high-water mark with a small live WAL; measure before
	// checkpointing so the flush stays rare.
	if frames := WalLiveFrames(storePath); frames >= 0 {
		var pageSize int64
		if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
			return err
		}
		if frames*pageSize <= WalTrigger {
			return nil
		}
	}
	_, err = db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	return err
}
