// Package duplication provides benchmark fixtures for the Phase 6 (Duplication
// Oracle) G6 test suite. Each fixture materializes a tiny Go repo containing an
// "existing" function and a "new" (staged) function, together with the expected
// duplication similarity bucket the DuplicationCheck should report.
package duplication

import (
	"os"
	"path/filepath"
	"testing"
)

// DupFixture holds a duplication benchmark fixture.
type DupFixture struct {
	Name           string // fixture name
	RepoDir        string // path to the materialized repo
	NewFile        string // relative path of the new (staged) file
	ExistingFile   string // relative path of the existing file
	ExpectedBucket string // "ignore" | "informational" | "warning" | "block-candidate"
	Description    string // human-readable description
}

func newDupDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func writeDupFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

// writeGoMod writes a minimal go.mod so kern can index the repo.
func writeGoMod(t *testing.T, dir string) {
	t.Helper()
	writeDupFile(t, dir, "go.mod", "module example.com/repo\n\ngo 1.23\n")
}

// ExactDuplicate returns a fixture whose new function is byte-identical to the
// existing function apart from the function name.
func ExactDuplicate(t *testing.T) DupFixture {
	t.Helper()
	dir := newDupDir(t)
	writeGoMod(t, dir)
	writeDupFile(t, dir, "shared/retry.go", `package shared

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

func RetryRequest(req *Request) error {
	for i := 0; i < 3; i++ {
		err := send(req)
		if err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("max retries")
}
`)
	writeDupFile(t, dir, "payments/retry.go", `package payments

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

func DoRetry(req *Request) error {
	for i := 0; i < 3; i++ {
		err := send(req)
		if err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("max retries")
}
`)
	return DupFixture{
		Name:           "exact-duplicate",
		RepoDir:        dir,
		NewFile:        "payments/retry.go",
		ExistingFile:   "shared/retry.go",
		ExpectedBucket: "block-candidate",
		Description:    "new function is byte-identical to existing (renamed only); kern pipeline caps identical records at 0.825 (control flow is not in the record)",
	}
}

// RenamedDuplicate returns a fixture whose new function implements the same
// algorithm as the existing one with every identifier renamed (function name,
// parameter, locals, helper, and error message wording).
func RenamedDuplicate(t *testing.T) DupFixture {
	t.Helper()
	dir := newDupDir(t)
	writeGoMod(t, dir)
	writeDupFile(t, dir, "shared/retry.go", `package shared

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

func RetryRequest(req *Request) error {
	for i := 0; i < 3; i++ {
		err := send(req)
		if err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("max retries")
}
`)
	writeDupFile(t, dir, "billing/retry.go", `package billing

import (
	"fmt"
	"time"
)

type Request struct{ URL string }

func transmit(r *Request) error { return nil }

func AttemptRetry(r *Request) error {
	for j := 0; j < 3; j++ {
		e := transmit(r)
		if e == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("exceeded retries")
}
`)
	return DupFixture{
		Name:           "renamed-duplicate",
		RepoDir:        dir,
		NewFile:        "billing/retry.go",
		ExistingFile:   "shared/retry.go",
		ExpectedBucket: "block-candidate",
		Description:    "same logic, all identifiers renamed (function, params, locals, helper, error message); kern pipeline scores this 0.585 (renamed calls drop the called-symbol overlap), just under the 0.60 threshold",
	}
}

// SlightlyRefactoredDuplicate returns a fixture whose new function keeps the
// same algorithm as the existing one but with minor structural changes
// (counter loop becomes a range loop, inline condition, extracted delay).
func SlightlyRefactoredDuplicate(t *testing.T) DupFixture {
	t.Helper()
	dir := newDupDir(t)
	writeGoMod(t, dir)
	writeDupFile(t, dir, "shared/retry.go", `package shared

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

func RetryRequest(req *Request) error {
	attempts := 3
	for i := 0; i < attempts; i++ {
		err := send(req)
		if err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("max retries")
}
`)
	writeDupFile(t, dir, "checkout/retry.go", `package checkout

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

func RetryRequest(req *Request) error {
	const attempts = 3
	delay := time.Second
	for range attempts {
		if err := send(req); err == nil {
			return nil
		}
		time.Sleep(delay)
	}
	return errors.New("max retries")
}
`)
	return DupFixture{
		Name:           "slightly-refactored-duplicate",
		RepoDir:        dir,
		NewFile:        "checkout/retry.go",
		ExistingFile:   "shared/retry.go",
		ExpectedBucket: "warning",
		Description:    "same algorithm with minor structural refactors (range loop, inline if, extracted delay); kern pipeline scores 0.820",
	}
}

// DifferentButSimilarAlgorithm returns a fixture whose new function shares the
// existing signature but implements a genuinely different algorithm
// (exponential backoff vs linear backoff).
func DifferentButSimilarAlgorithm(t *testing.T) DupFixture {
	t.Helper()
	dir := newDupDir(t)
	writeGoMod(t, dir)
	writeDupFile(t, dir, "shared/retry.go", `package shared

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

// RetryRequest retries with exponential backoff: 1s, 2s, 4s, capped at 30s.
func RetryRequest(req *Request) error {
	delay := time.Second
	for attempt := 0; attempt < 5; attempt++ {
		if err := send(req); err == nil {
			return nil
		}
		time.Sleep(delay)
		delay *= 2
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
	return errors.New("giving up after backoff retries")
}
`)
	writeDupFile(t, dir, "gateway/retry.go", `package gateway

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

// RetryRequest retries with linear backoff: a constant 1s between attempts.
func RetryRequest(req *Request) error {
	for attempt := 0; attempt < 5; attempt++ {
		if err := send(req); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("giving up after retries")
}
`)
	return DupFixture{
		Name:           "different-but-similar-algorithm",
		RepoDir:        dir,
		NewFile:        "gateway/retry.go",
		ExistingFile:   "shared/retry.go",
		ExpectedBucket: "informational",
		Description:    "same signature, genuinely different algorithm (exponential vs linear backoff); kern pipeline scores 0.779",
	}
}

// WrapperAroundExisting returns a fixture whose new function is a thin wrapper
// that logs and delegates to the existing function.
func WrapperAroundExisting(t *testing.T) DupFixture {
	t.Helper()
	dir := newDupDir(t)
	writeGoMod(t, dir)
	writeDupFile(t, dir, "shared/retry.go", `package shared

import (
	"errors"
	"time"
)

type Request struct{ URL string }

func send(req *Request) error { return nil }

func RetryRequest(req *Request) error {
	for i := 0; i < 3; i++ {
		err := send(req)
		if err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("max retries")
}
`)
	writeDupFile(t, dir, "api/retry.go", `package api

import (
	"log"

	"example.com/repo/shared"
)

func RetryWithLog(req *shared.Request) error {
	log.Println("retrying request")
	return shared.RetryRequest(req)
}
`)
	return DupFixture{
		Name:           "wrapper-around-existing",
		RepoDir:        dir,
		NewFile:        "api/retry.go",
		ExistingFile:   "shared/retry.go",
		ExpectedBucket: "warning",
		Description:    "new function is a thin wrapper that delegates to the existing function; kern pipeline scores 0.416 (different called symbols, small size)",
	}
}

// UnrelatedSameSignature returns a fixture whose new function has the same
// signature as the existing one but a completely different body
// (JSON parsing vs XOR encryption).
func UnrelatedSameSignature(t *testing.T) DupFixture {
	t.Helper()
	dir := newDupDir(t)
	writeGoMod(t, dir)
	writeDupFile(t, dir, "shared/process.go", `package shared

import (
	"encoding/json"
	"fmt"
)

type Event struct {
	ID   string
	Kind string
}

// Process parses an event payload as JSON.
func Process(data []byte) error {
	var ev Event
	if err := json.Unmarshal(data, &ev); err != nil {
		return err
	}
	if ev.ID == "" {
		return fmt.Errorf("event missing id")
	}
	return nil
}
`)
	writeDupFile(t, dir, "vault/process.go", `package vault

// Process encrypts the payload with a fixed XOR key.
func Process(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	key := byte(0x5a)
	for i := range data {
		data[i] ^= key
	}
	return nil
}
`)
	return DupFixture{
		Name:           "unrelated-same-signature",
		RepoDir:        dir,
		NewFile:        "vault/process.go",
		ExistingFile:   "shared/process.go",
		ExpectedBucket: "ignore",
		Description:    "same signature, completely different logic (JSON parse vs XOR encryption); kern pipeline scores 0.525",
	}
}

// GeneratedBoilerplate returns a fixture whose new functions are generated
// getter/setter boilerplate mirroring the existing ones but for different
// fields.
func GeneratedBoilerplate(t *testing.T) DupFixture {
	t.Helper()
	dir := newDupDir(t)
	writeGoMod(t, dir)
	writeDupFile(t, dir, "shared/model.go", `package shared

type Person struct {
	name string
	age  int
}

func (p *Person) GetName() string  { return p.name }
func (p *Person) SetName(v string) { p.name = v }
`)
	writeDupFile(t, dir, "users/model.go", `package users

type Account struct {
	email string
}

func (a *Account) GetEmail() string  { return a.email }
func (a *Account) SetEmail(v string) { a.email = v }
`)
	return DupFixture{
		Name:           "generated-boilerplate",
		RepoDir:        dir,
		NewFile:        "users/model.go",
		ExistingFile:   "shared/model.go",
		ExpectedBucket: "informational",
		Description:    "generated getter/setter boilerplate, structurally similar by nature; kern pipeline scores 0.540 (small-func penalty)",
	}
}
