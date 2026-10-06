// Package validate holds the shared input-validation helpers.
package validate

import "errors"

// NonEmpty rejects empty strings.
func NonEmpty(s string) error {
	if s == "" {
		return errors.New("empty value")
	}
	return nil
}

// Positive rejects zero and negative numbers.
func Positive(n int) error {
	if n <= 0 {
		return errors.New("non-positive value")
	}
	return nil
}
