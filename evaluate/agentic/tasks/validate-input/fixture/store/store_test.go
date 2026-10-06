package store

import "testing"

func TestSaveRejectsBadInput(t *testing.T) {
	s := New()
	if err := s.Save("", 1); err == nil {
		t.Fatal("Save accepted an empty name")
	}
	if err := s.Save("widget", 0); err == nil {
		t.Fatal("Save accepted a non-positive quantity")
	}
	if err := s.Save("widget", 3); err != nil {
		t.Fatalf("Save rejected valid input: %v", err)
	}
}

func TestDeleteStillValidates(t *testing.T) {
	s := New()
	if err := s.Delete(""); err == nil {
		t.Fatal("Delete accepted an empty name")
	}
}
