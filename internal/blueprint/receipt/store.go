package receipt

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNotFound is returned by Get/Latest when no receipt exists under the
// requested id (or at all). It maps to exit code 3 in `blueprint
// verify-receipt` — distinct from a tampered receipt (exit 2).
var ErrNotFound = errors.New("receipt not found")

// Store persists receipts as .blueprint/receipts/<receipt_id>.json inside a
// repository. .blueprint/ is gitignored, so receipts are local evidence, not
// commit content; the merge gate reads them from the same checkout the CI run
// produced them in (or a preserved .blueprint/ via a workspace cache).
type Store struct {
	dir string
}

// NewStore returns a Store rooted at <repoRoot>/.blueprint/receipts. The
// directory is created lazily on Save.
func NewStore(repoRoot string) *Store {
	return &Store{dir: filepath.Join(repoRoot, ".blueprint", "receipts")}
}

// Save writes the receipt as <receipt_id>.json, creating the directory if
// missing.
func (s *Store) Save(r *Receipt) error {
	if r.ReceiptID == "" {
		return errors.New("receipt has no id")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, r.ReceiptID+".json"), b, 0o644)
}

// Get reads and verifies the receipt with the given id. A missing file maps
// to ErrNotFound; a receipt that fails Verify (tampered) returns the verify
// error so the caller can distinguish the two.
func (s *Store) Get(id string) (*Receipt, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, err
	}
	var r Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("receipt %s: cannot parse: %w", id, err)
	}
	if err := r.Verify(); err != nil {
		return nil, fmt.Errorf("receipt %s: %w", id, err)
	}
	return &r, nil
}

// Latest returns the receipt with the most recent GeneratedAt. It does NOT
// verify signatures: a tampered receipt is returned as-is (when still
// parseable) so `blueprint verify-receipt` reports exit 2 (tampered) rather
// than exit 3 (not found). Files that no longer parse as JSON are skipped;
// ErrNotFound is returned when there are none.
func (s *Store) Latest() (*Receipt, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var receipts []*Receipt
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var r Receipt
		if err := json.Unmarshal(data, &r); err != nil || r.GeneratedAt.IsZero() {
			continue
		}
		receipts = append(receipts, &r)
	}
	if len(receipts) == 0 {
		return nil, ErrNotFound
	}
	sort.Slice(receipts, func(i, j int) bool {
		return receipts[i].GeneratedAt.After(receipts[j].GeneratedAt)
	})
	return receipts[0], nil
}
