// Heal-playbook store (Feature Batch D): a small per-repo store of heal
// runbooks keyed by the deterministic error signature of an incident class.
// When a new incident is ingested and a stored playbook matches its
// signature, the playbook is auto-attached to the incident so the report
// carries the runbook steps. Deterministic, stdlib-only, no network.
package incident

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
)

// Playbook is a heal runbook attached to an incident class: a deterministic
// error-signature key, the ordered remediation steps, the source that
// contributed the playbook, and the timestamp it was recorded.
type Playbook struct {
	Signature string    `json:"signature"`
	Steps     []string  `json:"steps"`
	Source    string    `json:"source,omitempty"`
	TS        time.Time `json:"ts"`
}

// PlaybookStore persists heal playbooks per project root as JSON at
// <root>/.kern/playbooks.json (0600, atomic temp-file rename via fsutil —
// the same persistence pattern as the incident store). A corrupt file loads
// as a fresh (empty) store and is logged, never a panic.
type PlaybookStore struct {
	root string
	path string

	mu sync.Mutex // guards all read/modify/write paths to prevent lost updates
}

// NewPlaybookStore returns the playbook store for the given project root.
// Storage path: <root>/.kern/playbooks.json.
func NewPlaybookStore(root string) *PlaybookStore {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return &PlaybookStore{root: root, path: filepath.Join(abs, ".kern", "playbooks.json")}
}

func (s *PlaybookStore) load() []Playbook {
	var list []Playbook
	b, err := os.ReadFile(s.path)
	if err != nil {
		return []Playbook{}
	}
	if err := json.Unmarshal(b, &list); err != nil {
		// Corrupt file → fresh store; surface the corruption instead of
		// silently dropping the stored data or panicking.
		log.Printf("playbooks: store load %s: unmarshal: %v (starting fresh)", s.path, err)
	}
	if list == nil {
		list = []Playbook{}
	}
	return list
}

func (s *PlaybookStore) save(list []Playbook) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write (unique temp name + rename) so concurrent writers never
	// clobber each other and readers never see a half-written file.
	return fsutil.WriteFileAtomic(s.path, b, 0o600)
}

// add inserts or replaces a playbook by signature (a signature is the key of
// an incident class, so adding the same signature again updates the runbook).
func (s *PlaybookStore) add(pb Playbook) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pb.Signature = strings.TrimSpace(pb.Signature)
	if pb.Signature == "" {
		return errors.New("playbooks: signature is required")
	}
	if pb.TS.IsZero() {
		pb.TS = time.Now().UTC()
	}
	list := s.load()
	kept := list[:0]
	for _, it := range list {
		if it.Signature != pb.Signature {
			kept = append(kept, it)
		}
	}
	kept = append(kept, pb)
	return s.save(kept)
}

func (s *PlaybookStore) list() ([]Playbook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := s.load()
	// Deterministic: newest first, then signature asc.
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].TS.Equal(list[j].TS) {
			return list[i].TS.After(list[j].TS)
		}
		return list[i].Signature < list[j].Signature
	})
	return list, nil
}

func (s *PlaybookStore) find(inc domain.Incident) (Playbook, bool) {
	sig := SignatureForIncident(inc)
	for _, pb := range s.load() {
		if pb.Signature == sig {
			return pb, true
		}
	}
	return Playbook{}, false
}

// AddPlaybook stores a playbook for the given project root, inserting or
// replacing by signature. It returns an error when the signature is empty.
func AddPlaybook(root string, pb Playbook) error {
	return NewPlaybookStore(root).add(pb)
}

// ListPlaybooks returns all stored playbooks for the project root, newest
// first. A missing or corrupt store returns an empty list, never an error.
func ListPlaybooks(root string) ([]Playbook, error) {
	return NewPlaybookStore(root).list()
}

// FindPlaybook returns the stored playbook whose signature matches the
// incident's deterministic error signature, if any.
func FindPlaybook(root string, inc domain.Incident) (Playbook, bool) {
	return NewPlaybookStore(root).find(inc)
}

// FindBySignature returns the stored playbook with an exactly matching
// signature, if any. Exported so the heal fast path can look up recorded
// fixes by signature (heal.Playbook adapter in the caller).
func (s *PlaybookStore) FindBySignature(signature string) (Playbook, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pb := range s.load() {
		if pb.Signature == signature {
			return pb, true
		}
	}
	return Playbook{}, false
}

// UpsertBySignature stores a playbook keyed by signature, inserting or
// replacing the entry with the same signature (empty signature errors).
// Exported so the heal fast path can record fixes by signature.
func (s *PlaybookStore) UpsertBySignature(signature string, steps []string) error {
	return s.add(Playbook{Signature: signature, Steps: steps})
}

// errorClassHash derives the error-class signal of an incident message: the
// first 12 characters of the content hash of the trimmed message — the exact
// same content-hash grouping the learning extractor uses (learning.keyFor's
// "sig:" fallback, cache.Hash truncated to 12 chars).
func errorClassHash(message string) string {
	return cache.Hash([]byte(strings.TrimSpace(message)))[:12]
}

// SignatureForAlert derives the deterministic playbook signature of an
// alert's incident class, reusing the learning extractor's grouping signal
// (service scope + error class): "scope:<service>:<error-class>" when the
// service is known, else "sig:<error-class>".
func SignatureForAlert(a domain.Alert) string {
	svc := strings.TrimSpace(a.Service)
	if svc != "" {
		return "scope:" + svc + ":" + errorClassHash(a.Message)
	}
	return "sig:" + errorClassHash(a.Message)
}

// SignatureForIncident derives the deterministic playbook signature of an
// incident, preferring the correlated affected service and falling back to
// the raw alert's service; the error class is derived from the incident
// title (or alert message).
func SignatureForIncident(inc domain.Incident) string {
	svc := strings.TrimSpace(inc.AffectedService)
	if svc == "" {
		svc = strings.TrimSpace(inc.Alert.Service)
	}
	msg := strings.TrimSpace(inc.Title)
	if msg == "" {
		msg = strings.TrimSpace(inc.Alert.Message)
	}
	if svc != "" {
		return "scope:" + svc + ":" + errorClassHash(msg)
	}
	return "sig:" + errorClassHash(msg)
}
