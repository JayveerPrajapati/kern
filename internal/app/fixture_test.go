package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fixtureDir is the on-disk standalone fixture repository. It is a
// real, buildable Go module that mirrors the UserService vertical slice, so the
// safe-change tests exercise a concrete repository rather than inline consts.
const fixtureDir = "testdata/fixtures/user_service"

// fixtureFiles are the files the standalone fixture repo must contain for the
// vertical slice to be considered a real, buildable source tree.
var fixtureFiles = []string{
	"go.mod",
	"main.go",
	"user.go",
	"cache.go",
	"tenant.go",
	"service.go",
	"user_test.go",
	".kern/boundaries.json",
}

// loadUserFixture resolves the standalone fixture repository and asserts every
// required fixture file is present. It is the single helper for tests that need
// a real on-disk UserService repo (T1).
func loadUserFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(fixtureDir)
	for _, f := range fixtureFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("fixture repo missing required file %q: %v", f, err)
		}
	}
	return dir
}

// TestUserServiceFixtureBuildable proves the standalone fixture repository is a
// real, buildable Go module with passing tests — not just a JSON spec. It runs
// `go build` and `go test` in the fixture directory.
// The build writes to a temp output file: building in-place would overwrite
// the committed binary artifact (the module is named user.service.fixture, so
// `go build ./...` drops its binary into the fixture dir), dirtying the tree on
// every test run.
func TestUserServiceFixtureBuildable(t *testing.T) {
	dir := loadUserFixture(t)

	build := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "fixture-build"), "./...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture repo does not build: %v\n%s", err, out)
	}

	test := exec.Command("go", "test", "./...")
	test.Dir = dir
	if out, err := test.CombinedOutput(); err != nil {
		t.Fatalf("fixture repo tests fail: %v\n%s", err, out)
	}
}
