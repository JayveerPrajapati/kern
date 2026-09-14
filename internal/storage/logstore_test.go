package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestLogStoreCRUD(t *testing.T) {
	s := NewLog(t.TempDir())
	ctx := context.Background()

	vals := map[string]json.RawMessage{
		"audit-audit-1": json.RawMessage(`{"n":2}`),
		"audit-audit-2": json.RawMessage(`"first"`),
		"audit-audit-3": json.RawMessage(`[1,2,3]`),
	}
	// Put in a fixed order: map iteration is randomized in Go, and the
	// on-disk assertions below expect the chain lines in this sequence.
	putOrder := []string{"audit-audit-1", "audit-audit-2", "audit-audit-3"}
	for _, k := range putOrder {
		if err := s.Put(ctx, k, vals[k]); err != nil {
			t.Fatalf("Put(%q): %v", k, err)
		}
	}

	// On-disk format: exactly one JSON line per Put, {"k":key,"v":value}.
	raw, err := os.ReadFile(filepath.Join(s.dir, chainFile))
	if err != nil {
		t.Fatalf("read chain.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != len(vals) {
		t.Fatalf("chain.jsonl has %d lines, want %d", len(lines), len(vals))
	}
	var cl chainLine
	if err := json.Unmarshal([]byte(lines[0]), &cl); err != nil {
		t.Fatalf("line 1 is not a chain line: %v", err)
	}
	if cl.K != "audit-audit-1" {
		t.Errorf("line 1 key = %q, want audit-audit-1", cl.K)
	}
	if string(cl.V) != string(vals["audit-audit-1"]) {
		t.Errorf("line 1 value = %s, want %s", cl.V, vals["audit-audit-1"])
	}

	// Get each back and assert bytes equal.
	for k, want := range vals {
		got, err := s.Get(ctx, k)
		if err != nil {
			t.Fatalf("Get(%q): %v", k, err)
		}
		if !reflect.DeepEqual([]byte(got), []byte(want)) {
			t.Errorf("Get(%q) = %s, want %s", k, got, want)
		}
	}
	// A missing key is ErrNotFound.
	if _, err := s.Get(ctx, "audit-audit-99"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}

	// List returns all entries in append order (no legacy files yet).
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantKeys := []string{"audit-audit-1", "audit-audit-2", "audit-audit-3"}
	if len(list) != len(wantKeys) {
		t.Fatalf("List returned %d entries, want %d", len(list), len(wantKeys))
	}
	for i, key := range wantKeys {
		if list[i].Key != key {
			t.Errorf("List[%d].Key = %q, want %q", i, list[i].Key, key)
		}
	}

	// Delete one, then Get returns ErrNotFound and List shrinks.
	if err := s.Delete(ctx, "audit-audit-2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "audit-audit-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	// Deleting a missing key is not an error.
	if err := s.Delete(ctx, "audit-audit-2"); err != nil {
		t.Errorf("Delete of missing key = %v, want nil", err)
	}
	list, err = s.List(ctx)
	if err != nil {
		t.Fatalf("List after Delete: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List after Delete = %d entries, want 2", len(list))
	}
}

func TestLogStoreLegacyMerge(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Phase 1: an older binary wrote per-key files (LocalStore format).
	legacy := NewLocal(dir)
	for k, v := range map[string]string{
		"audit-audit-1": `{"ID":"audit-1"}`,
		"audit-audit-2": `{"ID":"audit-2"}`,
	} {
		if err := legacy.Put(ctx, k, json.RawMessage(v)); err != nil {
			t.Fatalf("legacy Put(%q): %v", k, err)
		}
	}

	// Phase 2: the new store appends to chain.jsonl.
	s := NewLog(dir)
	if err := s.Put(ctx, "audit-audit-3", json.RawMessage(`{"ID":"audit-3"}`)); err != nil {
		t.Fatalf("chain Put: %v", err)
	}

	// List: legacy files first (key order), then chain lines in append order.
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"audit-audit-1", "audit-audit-2", "audit-audit-3"}
	if len(list) != len(want) {
		t.Fatalf("List = %d entries, want %d", len(list), len(want))
	}
	for i, key := range want {
		if list[i].Key != key {
			t.Errorf("List[%d].Key = %q, want %q", i, list[i].Key, key)
		}
	}

	// Get on a legacy-only key still works.
	got, err := s.Get(ctx, "audit-audit-2")
	if err != nil {
		t.Fatalf("Get(legacy key): %v", err)
	}
	if string(got) != `{"ID":"audit-2"}` {
		t.Errorf("Get(legacy key) = %s, want {\"ID\":\"audit-2\"}", got)
	}

	// Re-putting a legacy key through the chain store replaces it: the stale
	// per-key file is removed so the fresh value is not shadowed.
	if err := s.Put(ctx, "audit-audit-1", json.RawMessage(`{"ID":"audit-1-new"}`)); err != nil {
		t.Fatalf("re-Put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit-audit-1.json")); !os.IsNotExist(err) {
		t.Error("stale legacy file still present after Put")
	}
	got, err = s.Get(ctx, "audit-audit-1")
	if err != nil {
		t.Fatalf("Get(re-put key): %v", err)
	}
	if string(got) != `{"ID":"audit-1-new"}` {
		t.Errorf("Get(re-put key) = %s, want {\"ID\":\"audit-1-new\"}", got)
	}
}

func TestLogStoreDelete(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	legacy := NewLocal(dir)
	if err := legacy.Put(ctx, "audit-audit-1", json.RawMessage(`{"ID":"audit-1"}`)); err != nil {
		t.Fatalf("legacy Put: %v", err)
	}
	s := NewLog(dir)
	for i := 2; i <= 4; i++ {
		if err := s.Put(ctx, fmt.Sprintf("audit-audit-%d", i), json.RawMessage(`{"ID":"audit-`+strconv.Itoa(i)+`"}`)); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	// Create a dual-state key: a chain line + a legacy file written later by
	// an older binary (the mixed-version caveat).
	if err := legacy.Put(ctx, "audit-audit-3", json.RawMessage(`{"ID":"audit-3-old"}`)); err != nil {
		t.Fatalf("legacy shadow Put: %v", err)
	}

	// Delete removes BOTH forms.
	if err := s.Delete(ctx, "audit-audit-3"); err != nil {
		t.Fatalf("Delete dual-state: %v", err)
	}
	if _, err := s.Get(ctx, "audit-audit-3"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}

	// Delete removes a legacy-only key and its file.
	if err := s.Delete(ctx, "audit-audit-1"); err != nil {
		t.Fatalf("Delete legacy-only: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit-audit-1.json")); !os.IsNotExist(err) {
		t.Error("legacy file not removed by Delete")
	}

	// Remaining entries intact.
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List after Delete = %d entries, want 2", len(list))
	}
	if list[0].Key != "audit-audit-2" || list[1].Key != "audit-audit-4" {
		t.Errorf("List after Delete = %q, %q; want audit-audit-2, audit-audit-4", list[0].Key, list[1].Key)
	}
}

func TestLogStoreLastEntry(t *testing.T) {
	ctx := context.Background()

	// Empty store → ErrNotFound.
	if _, err := NewLog(t.TempDir()).LastEntry(ctx); !errors.Is(err, ErrNotFound) {
		t.Errorf("LastEntry on empty store = %v, want ErrNotFound", err)
	}

	// Legacy-only store → the entry with the largest numeric key suffix,
	// regardless of write order.
	dir := t.TempDir()
	legacy := NewLocal(dir)
	for _, k := range []string{"audit-audit-9", "audit-audit-1", "audit-audit-10"} {
		if err := legacy.Put(ctx, k, json.RawMessage(`{"ID":"`+strings.TrimPrefix(k, "audit-audit-")+`"}`)); err != nil {
			t.Fatalf("legacy Put(%q): %v", k, err)
		}
	}
	ls := NewLog(dir)
	last, err := ls.LastEntry(ctx)
	if err != nil {
		t.Fatalf("LastEntry (legacy-only): %v", err)
	}
	if last.Key != "audit-audit-10" {
		t.Errorf("LastEntry (legacy-only) = %q, want audit-audit-10", last.Key)
	}

	// A chain.jsonl tail wins over legacy files.
	if err := ls.Put(ctx, "audit-audit-11", json.RawMessage(`{"ID":"audit-11"}`)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	last, err = ls.LastEntry(ctx)
	if err != nil {
		t.Fatalf("LastEntry (mixed): %v", err)
	}
	if last.Key != "audit-audit-11" {
		t.Errorf("LastEntry (mixed) = %q, want audit-audit-11", last.Key)
	}

	// A torn trailing line (crash mid-append) is skipped; the last COMPLETE
	// line is returned.
	dir2 := t.TempDir()
	s2 := NewLog(dir2)
	for i := 1; i <= 3; i++ {
		if err := s2.Put(ctx, fmt.Sprintf("audit-audit-%d", i), json.RawMessage(`{"n":`+strconv.Itoa(i)+`}`)); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	f, err := os.OpenFile(filepath.Join(dir2, chainFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open chain for torn write: %v", err)
	}
	if _, err := f.WriteString(`{"k":"audit-audit-4","v":{"n":4}`); err != nil {
		t.Fatalf("torn write: %v", err)
	}
	_ = f.Close()
	last, err = s2.LastEntry(ctx)
	if err != nil {
		t.Fatalf("LastEntry (torn tail): %v", err)
	}
	if last.Key != "audit-audit-3" {
		t.Errorf("LastEntry with torn tail = %q, want audit-audit-3", last.Key)
	}
}

// TestLogStoreAppend2000 is a sanity check that the append path stays O(1):
// 2000 appends, then List/LastEntry still report everything correctly.
func TestLogStoreAppend2000(t *testing.T) {
	s := NewLog(t.TempDir())
	ctx := context.Background()
	const n = 2000
	for i := 1; i <= n; i++ {
		key := fmt.Sprintf("audit-audit-%d", i)
		if err := s.Put(ctx, key, json.RawMessage(`{"n":`+strconv.Itoa(i)+`}`)); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != n {
		t.Fatalf("List = %d entries, want %d", len(list), n)
	}
	last, err := s.LastEntry(ctx)
	if err != nil {
		t.Fatalf("LastEntry: %v", err)
	}
	if last.Key != fmt.Sprintf("audit-audit-%d", n) {
		t.Errorf("LastEntry = %q, want audit-audit-%d", last.Key, n)
	}
}

func TestLogStoreUnsafeKey(t *testing.T) {
	s := NewLog(t.TempDir())
	ctx := context.Background()
	for _, key := range []string{"a/b", "..", "."} {
		if err := s.Put(ctx, key, json.RawMessage(`{}`)); err == nil {
			t.Errorf("Put(%q): expected error, got nil", key)
		}
	}
}

// TestLogStorePutFailureKeepsShadow: when the chain append fails, the legacy
// per-key shadow (holding the prior value) must survive — the old code
// deleted it before the write, losing the value on failure. A11.
func TestLogStorePutFailureKeepsShadow(t *testing.T) {
	dir := t.TempDir()
	s := NewLog(dir)
	ctx := context.Background()
	key := "audit-audit-1"
	old := json.RawMessage(`{"n":1}`)
	// Legacy per-key shadow holding the prior value.
	if err := os.WriteFile(filepath.Join(dir, key+".json"), old, 0o600); err != nil {
		t.Fatalf("write shadow: %v", err)
	}
	// Break the chain append: a directory at the chain path makes the
	// O_APPEND open fail.
	if err := os.MkdirAll(filepath.Join(dir, chainFile), 0o755); err != nil {
		t.Fatalf("mkdir chain: %v", err)
	}
	if err := s.Put(ctx, key, json.RawMessage(`{"n":2}`)); err == nil {
		t.Fatal("Put() = nil with an unopenable chain, want error")
	}
	// The prior value must still be readable through the shadow.
	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(old) {
		t.Errorf("Get = %s, want the prior value %s preserved", got, old)
	}
}

// TestLogStoreListDedupesRewrittenKeys: Put appends a chain line per write,
// so a key rewritten N times has N lines in chain.jsonl. List must report
// the key once (the LAST write — append order is write order), matching the
// value Get returns. Without the dedupe, consumers that expect one value per
// key (the audit tamper chain) break on every rewritten key.
func TestLogStoreListDedupesRewrittenKeys(t *testing.T) {
	ctx := context.Background()
	s := NewLog(t.TempDir())

	_ = s.Put(ctx, "k1", []byte(`{"v":1}`))
	_ = s.Put(ctx, "k2", []byte(`{"v":2}`))
	_ = s.Put(ctx, "k1", []byte(`{"v":"revised"}`)) // rewrite k1

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List = %d entries, want 2 (k1 deduped)", len(list))
	}
	for _, e := range list {
		got, err := s.Get(ctx, e.Key)
		if err != nil {
			t.Fatalf("Get(%s): %v", e.Key, err)
		}
		if string(got) != string(e.Value) {
			t.Errorf("List value for %s = %s, want the Get value %s", e.Key, e.Value, got)
		}
	}
	// The LAST write of k1 must be the one reported.
	var k1 Entry
	for _, e := range list {
		if e.Key == "k1" {
			k1 = e
		}
	}
	if string(k1.Value) != `{"v":"revised"}` {
		t.Errorf("k1 value = %s, want the last write", k1.Value)
	}
}

// TestLogStoreListFileShadowsLines: a key present BOTH as a legacy per-key
// file and as chain lines (mixed-version window) is reported once, with the
// FILE version winning — Get semantics.
func TestLogStoreListFileShadowsLines(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := NewLog(dir)

	_ = s.Put(ctx, "k1", []byte(`"chain-old"`))
	// Simulate a legacy file: an older binary wrote k1 as a per-key file
	// without removing the chain line (the pre-lock race window).
	_ = os.WriteFile(filepath.Join(dir, "k1.json"), []byte(`"file-version"`), 0o600)

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List = %d entries, want 1 (file shadows lines)", len(list))
	}
	if string(list[0].Value) != `"file-version"` {
		t.Errorf("List value = %s, want the file version", list[0].Value)
	}
	got, err := s.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != `"file-version"` {
		t.Errorf("Get = %s, want the file version", got)
	}
}

// TestLogStoreRewriteAll: the repair path replaces the ENTIRE store content
// with exactly the given entries — one chain line per key, no legacy files,
// no stale duplicate lines.
func TestLogStoreRewriteAll(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := NewLog(dir)

	_ = s.Put(ctx, "k1", []byte(`"old-1"`))
	_ = s.Put(ctx, "k2", []byte(`"old-2"`))
	_ = s.Put(ctx, "k1", []byte(`"old-1b"`)) // stale duplicate line for k1
	_ = os.WriteFile(filepath.Join(dir, "k3.json"), []byte(`"legacy-file-3"`), 0o600)

	entries := []Entry{
		{Key: "k1", Value: json.RawMessage(`"new-1"`)},
		{Key: "k2", Value: json.RawMessage(`"new-2"`)},
	}
	if err := s.RewriteAll(ctx, entries); err != nil {
		t.Fatalf("RewriteAll: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List = %d entries, want exactly 2 (k3 dropped, k1 deduped)", len(list))
	}
	byKey := map[string]string{}
	for _, e := range list {
		byKey[e.Key] = string(e.Value)
	}
	if byKey["k1"] != `"new-1"` || byKey["k2"] != `"new-2"` {
		t.Errorf("after rewrite: %v", byKey)
	}
	if _, err := s.Get(ctx, "k3"); err == nil {
		t.Error("Get(k3) succeeded after RewriteAll dropped it, want ErrNotFound")
	}
	// No legacy files remain: the whole store is chain lines now.
	des, _ := os.ReadDir(dir)
	for _, de := range des {
		if strings.HasSuffix(de.Name(), ".json") {
			t.Errorf("legacy file %s survived RewriteAll", de.Name())
		}
	}
}
