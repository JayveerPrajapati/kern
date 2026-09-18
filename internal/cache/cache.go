// Package cache persists kern state on the local machine, outside any user
// workspace, so nothing generated is ever visible in a project.
//
// Lifecycle: dormant entries older than the archive threshold are gzipped
// to "<name>.json.gz" twins and stale ones are evicted by Maintain (gc.go).
// Readers stay transparent to that: Load falls back to the .gz twin, Exists
// reports either variant, and Store always keeps the active copy plain.
package cache

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Dir returns the kern cache root, honouring XDG_CACHE_HOME.
func Dir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "kern")
}

// Path returns an absolute path inside the cache root.
func Path(parts ...string) string {
	return filepath.Join(append([]string{Dir()}, parts...)...)
}

// Ensure creates the cache root (and any parents) if needed.
func Ensure() error {
	return os.MkdirAll(Dir(), 0o755)
}

// Hash returns the hex SHA-256 of a byte slice.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Store writes v as JSON under key. Keys are namespaced with a subdir.
func Store(key string, v any) error {
	// opportunistic GC of the data dir this key lives in (rate-limited
	// to once an hour by the .maintained-at marker); best-effort, swallowed.
	MaintainOnce(Path("data"))
	if err := Ensure(); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	path := Path("data", key+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := atomicWrite(path, data); err != nil {
		return err
	}
	// A dormant .gz twin must not outlive its freshly written plain active
	// copy; drop it so the twin is never the newer of the two.
	_ = os.Remove(path + ".gz")
	return nil
}

// atomicWrite writes data via a temp file + rename so a crash mid-write cannot
// truncate an existing entry into corrupt JSON.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Load reads JSON previously stored under key. Returns os.ErrNotExist if
// absent. If the plain file is missing but its gzip twin "<path>.json.gz"
// exists (archival), the twin is transparently decompressed.
func Load(key string, v any) error {
	// opportunistic GC of the data dir this key lives in (rate-limited
	// to once an hour by the .maintained-at marker); best-effort, swallowed.
	MaintainOnce(Path("data"))
	path := Path("data", key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Fall back to the dormant gzip twin.
			data, err = gunzipFile(path + ".gz")
		}
		if err != nil {
			return err
		}
	}
	return json.Unmarshal(data, v)
}

// gunzipFile reads and decompresses a .gz file. Used to serve archived
// (dormant) cache entries transparently.
func gunzipFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(zr)
}

// Exists reports whether a key is cached, either as a plain file or as a
// gzip-archived twin.
func Exists(key string) bool {
	path := Path("data", key+".json")
	if _, err := os.Stat(path); err == nil {
		return true
	}
	_, err := os.Stat(path + ".gz")
	return err == nil
}

// Remove deletes a cached entry: the plain file and any dormant gzip twin
// so an expired entry can never resurface from its archive. An absent
// key is a no-op (returns nil).
func Remove(key string) error {
	path := Path("data", key+".json")
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	if gzErr := os.Remove(path + ".gz"); gzErr != nil && !errors.Is(gzErr, fs.ErrNotExist) && err == nil {
		err = gzErr
	}
	return err
}
