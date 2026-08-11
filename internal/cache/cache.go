// Package cache persists kern state on the local machine, outside any user
// workspace, so nothing generated is ever visible in a project.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	return atomicWrite(path, data)
}

// atomicWrite writes data to path via a temp file + rename so a crash mid-write
// cannot truncate an existing entry to corrupt JSON (W2-52).
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Load reads JSON previously stored under key. Returns os.ErrNotExist if absent.
func Load(key string, v any) error {
	path := Path("data", key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// Exists reports whether a key is cached.
func Exists(key string) bool {
	_, err := os.Stat(Path("data", key+".json"))
	return err == nil
}
