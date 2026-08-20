package storage

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestLocalStoreCRUD(t *testing.T) {
	s := NewLocal(t.TempDir())
	ctx := context.Background()

	vals := map[string]json.RawMessage{
		"bravo": json.RawMessage(`{"n":2}`),
		"alpha": json.RawMessage(`"first"`),
		"delta": json.RawMessage(`[1,2,3]`),
	}
	for k, v := range vals {
		if err := s.Put(ctx, k, v); err != nil {
			t.Fatalf("Put(%q): %v", k, err)
		}
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

	// List returns all entries sorted by key.
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantKeys := []string{"alpha", "bravo", "delta"}
	if len(list) != len(wantKeys) {
		t.Fatalf("List returned %d entries, want %d", len(list), len(wantKeys))
	}
	for i, key := range wantKeys {
		if list[i].Key != key {
			t.Errorf("List[%d].Key = %q, want %q", i, list[i].Key, key)
		}
	}

	// Delete one, then Get returns ErrNotFound.
	if err := s.Delete(ctx, "bravo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "bravo"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}

	// Deleting a missing key is not an error.
	if err := s.Delete(ctx, "bravo"); err != nil {
		t.Errorf("Delete of missing key = %v, want nil", err)
	}
}

func TestLocalStoreUnsafeKey(t *testing.T) {
	s := NewLocal(t.TempDir())
	ctx := context.Background()
	for _, key := range []string{"a/b", "..", "."} {
		if err := s.Put(ctx, key, json.RawMessage(`{}`)); err == nil {
			t.Errorf("Put(%q): expected error, got nil", key)
		}
	}
}

func TestMarshalValue(t *testing.T) {
	type record struct {
		Name string
		N    int
	}
	in := record{Name: "kern", N: 7}
	raw, err := MarshalValue(in)
	if err != nil {
		t.Fatalf("MarshalValue: %v", err)
	}
	var out record
	if err := UnmarshalValue(raw, &out); err != nil {
		t.Fatalf("UnmarshalValue: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip = %+v, want %+v", out, in)
	}
}
