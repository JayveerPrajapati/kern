// Package storage provides a vendor-agnostic storage abstraction. The only
// implementation today is LocalStore, a file-per-key JSON store using atomic
// temp-file renames with zero third-party dependencies.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotFound is returned by Get when a key does not exist in the store.
var ErrNotFound = errors.New("storage: not found")

// Entry is the unit of storage. Value stays opaque JSON so callers own their
// own schema.
type Entry struct {
	Key   string
	Value json.RawMessage
}

// Store is a vendor-agnostic key/value storage abstraction. Implementations
// return standard-library errors and never panic.
type Store interface {
	Put(ctx context.Context, key string, value json.RawMessage) error
	Get(ctx context.Context, key string) (json.RawMessage, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context) ([]Entry, error)
}

// LocalStore is a file-per-key JSON store rooted at a directory. It persists
// values to <dir>/<key>.json using an atomic temp-file rename so a crash
// mid-write never corrupts an existing value.
type LocalStore struct {
	dir string
}

// NewLocal returns a LocalStore rooted at dir. The directory is created
// lazily on the first write.
func NewLocal(dir string) *LocalStore {
	return &LocalStore{dir: dir}
}

// Put stores value under key, atomically. The directory is created if needed.
func (s *LocalStore) Put(ctx context.Context, key string, value json.RawMessage) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.dir, key+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, value, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Get reads the value stored under key. If the file does not exist it returns
// the zero json.RawMessage(nil) and ErrNotFound. A present file whose bytes
// are an empty JSON document is a valid value, not a missing key.
func (s *LocalStore) Get(ctx context.Context, key string) (json.RawMessage, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.dir, key+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return json.RawMessage(b), nil
}

// Delete removes the value stored under key. A missing key is not an error.
func (s *LocalStore) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(s.dir, key+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// List returns every stored entry, sorted by Key, with Value set to the raw
// file bytes. In-progress .tmp files are excluded.
func (s *LocalStore) List(ctx context.Context) ([]Entry, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Entry{}, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Key: strings.TrimSuffix(name, ".json"), Value: json.RawMessage(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// validateKey rejects keys that would escape the store directory or be unsafe
// as a filename: empty keys, path separators, and ".." traversal are not
// allowed.
func validateKey(key string) error {
	if key == "" {
		return errors.New("storage: empty key")
	}
	if key == "." || key == ".." {
		return errors.New("storage: unsafe key")
	}
	if strings.ContainsAny(key, `/\`) || strings.Contains(key, "..") {
		return errors.New("storage: unsafe key")
	}
	return nil
}

// MarshalValue marshals v to opaque JSON for storage via Put.
func MarshalValue(v interface{}) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// UnmarshalValue unmarshals a stored raw value into out.
func UnmarshalValue(raw json.RawMessage, out interface{}) error {
	return json.Unmarshal(raw, out)
}

// Interface health check.
var _ Store = (*LocalStore)(nil)
