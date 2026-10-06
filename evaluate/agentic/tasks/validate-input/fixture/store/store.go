package store

import "example.com/store/internal/validate"

// Store is a tiny in-memory record store.
type Store struct {
	items map[string]int
}

// New returns an empty Store.
func New() *Store { return &Store{items: map[string]int{}} }

// Save records a named item with a quantity. It currently accepts anything.
func (s *Store) Save(name string, qty int) error {
	s.items[name] = qty
	return nil
}

// Delete removes an item. It validates its input with the shared helpers.
func (s *Store) Delete(name string) error {
	if err := validate.NonEmpty(name); err != nil {
		return err
	}
	delete(s.items, name)
	return nil
}

// Get returns the recorded quantity for name.
func (s *Store) Get(name string) int { return s.items[name] }
