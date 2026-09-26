package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestUpdateE2E drives the Stage A release-channel policy end to end against
// a local file:// fixture: real kern binaries (built and ldflags-stamped by
// the test), the repo's install.sh, and fake release tarballs served from
// KERN_BASE_URL. KERN_INSTALL_SCRIPT_URL points `kern update`'s curl at the
// local install.sh, so NOTHING touches the network.
//
// Covered rows:
//   - local-build refuse — the #561 repro (hash-stamped binary must refuse,
//     never be silently overwritten by a "fresh install")
//   - older -> proceed (normal update path, zero behavior change)
//   - newer -> refuse downgrade (preflight deny, --pin guidance)
//   - pin-downgrade proceeds (kern update --pin <older-tag> consents)
//   - force overrides (KERN_FORCE=1 through both the Go path and install.sh)
//   - old-binary fallback shim (a binary without the hidden subcommand)
//   - stable channel resolves the newest 3-component tag (--channel stable
//     forwarded as KERN_CHANNEL; the 4-component hotfix is excluded)
//   - a channel resolving to an OLDER tag still requires --pin consent
//
// Expensive (multiple go builds + install.sh runs): skipped under -short.
func TestUpdateE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("update e2e: builds real binaries and runs install.sh against a file:// fixture")
	}
	root := repoRoot(t)
	for _, bin := range []string{"go", "curl", "sh", "tar", "sed", "cut", "grep", "awk"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("update e2e: %s not available: %v", bin, err)
		}
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptURL := "file://" + filepath.Join(root, "install.sh")

	// buildAll compiles kern, kern-mcp and kern-server into dir, each
	// stamped with ver via the Makefile-style ldflags pair.
	buildAll := func(dir, ver string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build",
			"-ldflags", fmt.Sprintf("-X main.version=%s -X github.com/JayveerPrajapati/kern/internal/version.Version=%s", ver, ver),
			"-o", dir+"/", "./cmd/kern", "./cmd/kern-mcp", "./cmd/kern-server")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", ver, err, out)
		}
	}

	// fixtureTag packages the three binaries in src into the fake release
	// tarball releases/download/<tag>/kern-<os>-<arch>.tar.gz (archive-root
	// layout, matching >= v0.9.5.2 releases).
	fixtureTag := func(tag, src string) {
		t.Helper()
		rel := filepath.Join(base, "releases", "download", tag)
		if err := os.MkdirAll(rel, 0o755); err != nil {
			t.Fatal(err)
		}
		platform := runtime.GOOS + "-" + runtime.GOARCH
		cmd := exec.Command("tar", "-czf", filepath.Join(rel, "kern-"+platform+".tar.gz"),
			"-C", src, "kern", "kern-mcp", "kern-server")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tar fixture %s: %v\n%s", tag, err, out)
		}
	}

	// Shared build artifacts (each stamp built once).
	dirHash := filepath.Join(base, "build-hash")
	dir952 := filepath.Join(base, "build-952")
	dir99 := filepath.Join(base, "build-99")
	dir991 := filepath.Join(base, "build-991")
	buildAll(dirHash, "deadbeef")
	buildAll(dir952, "v0.9.5.2")
	buildAll(dir99, "v0.9.9")
	buildAll(dir991, "v0.9.9.1")
	fixtureTag("v0.9.9.1", dir991)
	fixtureTag("v0.9.9", dir99)
	fixtureTag("v0.9.5.2", dir952)

	// Channel fixture: the releases-LIST endpoint served from the file://
	// API root. curl strips the "?per_page=100" query from a file:// URL
	// and opens the file literally named "releases" — the newest-first
	// release order below lets pick_highest's stable filter choose v0.9.9
	// (v0.9.9.1 is a 4-component hotfix and must be excluded). The list
	// endpoint and the /releases/latest endpoint cannot share a file://
	// root (releases would have to be both a file and a directory), so the
	// channel cases exercise stable/regex only — the latest channel is
	// today's unchanged /releases/latest behavior.
	apiRel := filepath.Join(base, "api", "releases")
	if err := os.MkdirAll(filepath.Dir(apiRel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apiRel, []byte(`[{"tag_name":"v0.9.9.1"},{"tag_name":"v0.9.9"},{"tag_name":"v0.9.5.2"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	// envFor builds the child environment: isolated HOME, fixture URLs, the
	// install prefix and any per-case extras (e.g. KERN_VERSION). Keys we
	// override are DROPPED from the inherited environment first: on Unix a
	// duplicated variable in the execve array resolves to the FIRST
	// occurrence (libc getenv), so appending "HOME=..." after
	// os.Environ()'s real HOME would silently shadow the isolation — the
	// installer's agent-wiring step would touch the real home configs.
	envFor := func(prefix string, extra ...string) []string {
		drop := map[string]bool{
			"HOME": true, "XDG_CACHE_HOME": true,
			"KERN_INSTALL_DIR": true, "KERN_BASE_URL": true,
			"KERN_INSTALL_SCRIPT_URL": true, "KERN_NO_PATH": true,
			"KERN_VERSION": true, "KERN_FORCE": true, "KERN_PIN": true,
			"KERN_CHANNEL": true, "KERN_API_URL": true,
		}
		var env []string
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			if !drop[k] {
				env = append(env, kv)
			}
		}
		env = append(env,
			"HOME="+home,
			"KERN_INSTALL_DIR="+prefix,
			"KERN_BASE_URL=file://"+base,
			"KERN_API_URL=file://"+filepath.Join(base, "api"),
			"KERN_INSTALL_SCRIPT_URL="+scriptURL,
			"KERN_NO_PATH=1",
			"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		)
		return append(env, extra...)
	}

	// runSh runs the repo's install.sh with the given operation.
	runSh := func(env []string, op string) (string, error) {
		cmd := exec.Command("sh", filepath.Join(root, "install.sh"), op)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// runKern runs the binary installed at prefix.
	runKern := func(prefix string, env []string, args ...string) (string, error) {
		cmd := exec.Command(filepath.Join(prefix, "kern"), args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	exitCode := func(err error) int {
		if err == nil {
			return 0
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return -1
	}

	install := func(prefix, srcBin string) {
		t.Helper()
		if err := os.MkdirAll(prefix, 0o755); err != nil {
			t.Fatal(err)
		}
		copyFile(t, filepath.Join(srcBin, "kern"), filepath.Join(prefix, "kern"))
	}

	// fakeOldBinary writes a deliberately dumb stand-in for a PRE-policy
	// kern binary: answers `version` with the given line, exits 2 for
	// everything else (an old binary has no `update --preflight`).
	fakeOldBinary := func(prefix, versionLine string) {
		t.Helper()
		if err := os.MkdirAll(prefix, 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\ncase \"$1\" in\nversion) echo \"kern " + versionLine + "\" ;;\n*) exit 2 ;;\nesac\n"
		p := filepath.Join(prefix, "kern")
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	installedVersion := func(prefix string, env []string) string {
		t.Helper()
		out, err := runKern(prefix, env, "version")
		if err != nil {
			t.Fatalf("kern version: %v\n%s", err, out)
		}
		return out
	}

	// ---- Case 1: local-build refuse (the #561 repro, MUST pass) ----
	t.Run("local-build-refuse-561", func(t *testing.T) {
		prefix := filepath.Join(base, "p1")
		install(prefix, dirHash)
		env := envFor(prefix, "KERN_VERSION=v0.9.9.1")

		// (a) preflight verdict: exit 3 with local-build guidance.
		out, err := runKern(prefix, env, "update", "--preflight", "v0.9.9.1")
		if code := exitCode(err); code != 3 {
			t.Fatalf("preflight(local) exit = %d, want 3\n%s", code, out)
		}
		for _, want := range []string{"local build", "make build && make install", "--force"} {
			if !strings.Contains(out, want) {
				t.Errorf("preflight output missing %q:\n%s", want, out)
			}
		}

		// (b) full install.sh upgrade must abort and NOT overwrite.
		out, err = runSh(env, "upgrade")
		if err == nil {
			t.Fatalf("install.sh upgrade over a local build succeeded, want refusal\n%s", out)
		}
		if !strings.Contains(out, "local build") && !strings.Contains(out, "refus") {
			t.Errorf("install.sh output missing refusal:\n%s", out)
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "deadbeef") {
			t.Fatalf("local binary was overwritten: version now %q", v)
		}

		// (c) `kern update` main path: refuses BEFORE the network.
		out, err = runKern(prefix, env, "update")
		if code := exitCode(err); code != 3 {
			t.Fatalf("kern update(local) exit = %d, want 3\n%s", code, out)
		}
		for _, want := range []string{"local build", "make build && make install"} {
			if !strings.Contains(out, want) {
				t.Errorf("kern update output missing %q:\n%s", want, out)
			}
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "deadbeef") {
			t.Fatalf("local binary was overwritten: version now %q", v)
		}
	})

	// ---- Case 2: older -> proceed (normal update path, no behavior change) ----
	t.Run("older-upgrade-proceeds", func(t *testing.T) {
		prefix := filepath.Join(base, "p2")
		install(prefix, dir952)
		env := envFor(prefix, "KERN_VERSION=v0.9.9.1")
		out, err := runSh(env, "upgrade")
		if err != nil {
			t.Fatalf("install.sh upgrade failed, want success\n%s", out)
		}
		if !strings.Contains(out, "preflight: allow") {
			t.Errorf("output missing preflight allow:\n%s", out)
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "kern v0.9.9.1") {
			t.Fatalf("installed version = %q, want v0.9.9.1", v)
		}
	})

	// ---- Case 3: newer -> refuse downgrade ----
	t.Run("newer-refuses-downgrade", func(t *testing.T) {
		prefix := filepath.Join(base, "p3")
		install(prefix, dir991)
		env := envFor(prefix, "KERN_VERSION=v0.9.5.2")
		out, err := runSh(env, "upgrade")
		if err == nil {
			t.Fatalf("install.sh downgrade succeeded, want refusal\n%s", out)
		}
		if !strings.Contains(out, "downgrade") || !strings.Contains(out, "--pin") {
			t.Errorf("output missing downgrade/--pin guidance:\n%s", out)
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "v0.9.9.1") {
			t.Fatalf("binary changed after refused downgrade: %q", v)
		}
	})

	// ---- Case 4: pin-downgrade proceeds (the deliberate-downgrade path) ----
	t.Run("pin-downgrade-proceeds", func(t *testing.T) {
		prefix := filepath.Join(base, "p4")
		install(prefix, dir991)
		env := envFor(prefix) // KERN_VERSION/KERN_PIN are forwarded by the Go side
		out, err := runKern(prefix, env, "update", "--pin", "v0.9.5.2")
		if err != nil {
			t.Fatalf("kern update --pin v0.9.5.2 failed, want deliberate downgrade\n%s", out)
		}
		if !strings.Contains(out, "deliberate pin downgrade") {
			t.Errorf("output missing pin-consent preflight line:\n%s", out)
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "kern v0.9.5.2") {
			t.Fatalf("installed version = %q, want v0.9.5.2", v)
		}
	})

	// ---- Case 5: force overrides (KERN_FORCE=1) ----
	t.Run("force-overrides", func(t *testing.T) {
		prefix := filepath.Join(base, "p5")
		install(prefix, dirHash)
		env := envFor(prefix)
		// (a) install.sh honors KERN_FORCE=1 directly: the preflight's
		// local-build deny (exit 3) is overridden and the install proceeds.
		out, err := runSh(envFor(prefix, "KERN_VERSION=v0.9.9.1", "KERN_FORCE=1"), "upgrade")
		if err != nil {
			t.Fatalf("install.sh + KERN_FORCE=1 failed, want override\n%s", out)
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "kern v0.9.9.1") {
			t.Fatalf("installed version = %q, want v0.9.9.1", v)
		}
		// (b) `kern update --force --pin` from a local build: the Go guard
		// AND install.sh's preflight both deny the local build; --force
		// overrides at both layers and KERN_FORCE=1 is forwarded.
		install(prefix, dirHash) // put the local build back
		out, err = runKern(prefix, env, "update", "--force", "--pin", "v0.9.9.1")
		if err != nil {
			t.Fatalf("kern update --force failed, want override\n%s", out)
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "kern v0.9.9.1") {
			t.Fatalf("installed version = %q, want v0.9.9.1", v)
		}
		// HOME isolation check: install.sh's wire step runs `kern setup
		// --detect --global`; its config artifacts must land under the
		// isolated HOME — a leak to the real home would be a test hazard.
		found := false
		for _, rel := range []string{".claude.json", ".config/opencode/opencode.json", ".codex/config.toml", ".gemini/settings.json"} {
			if _, err := os.Stat(filepath.Join(home, rel)); err == nil {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no agent config artifacts under isolated HOME %s — HOME isolation may have leaked to the real home", home)
		}
	})

	// ---- Case 6: old-binary fallback shim (no preflight subcommand) ----
	t.Run("old-binary-shim", func(t *testing.T) {
		// (a) old release-shaped binary: shim's numeric tuple compare allows
		// the newer target and the install proceeds.
		prefix := filepath.Join(base, "p6a")
		fakeOldBinary(prefix, "v0.9.5.2")
		env := envFor(prefix, "KERN_VERSION=v0.9.9.1")
		out, err := runSh(env, "upgrade")
		if err != nil {
			t.Fatalf("install.sh via shim failed, want allow\n%s", out)
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "kern v0.9.9.1") {
			t.Fatalf("installed version = %q, want v0.9.9.1", v)
		}
		// (b) old local-build-shaped binary: current unreadable -> shim
		// refuses (the curl|sh #561 protection on pre-policy binaries).
		prefix = filepath.Join(base, "p6b")
		fakeOldBinary(prefix, "deadbeef")
		env = envFor(prefix, "KERN_VERSION=v0.9.9.1")
		out, err = runSh(env, "upgrade")
		if err == nil {
			t.Fatalf("install.sh over old local-shaped binary succeeded, want refusal\n%s", out)
		}
		if !strings.Contains(out, "local build") && !strings.Contains(out, "unidentifiable") {
			t.Errorf("output missing local-build refusal:\n%s", out)
		}
		// (c) ...unless KERN_FORCE=1.
		env = envFor(prefix, "KERN_VERSION=v0.9.9.1", "KERN_FORCE=1")
		out, err = runSh(env, "upgrade")
		if err != nil {
			t.Fatalf("install.sh + KERN_FORCE=1 via shim failed\n%s", out)
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "kern v0.9.9.1") {
			t.Fatalf("installed version = %q, want v0.9.9.1", v)
		}
	})

	// ---- Case 7: stable channel resolves the newest 3-component tag ----
	t.Run("channel-stable-picks-max-3component", func(t *testing.T) {
		prefix := filepath.Join(base, "p7")
		install(prefix, dir952)
		env := envFor(prefix)
		// The fixture list holds v0.9.9.1 (hotfix), v0.9.9 and v0.9.5.2;
		// the stable channel must skip the 4-component hotfix and pick
		// v0.9.9. Full path: --channel reaches the installer as
		// KERN_CHANNEL, get_version resolves it against the file:// API
		// fixture, and the preflight allows the newer target.
		out, err := runKern(prefix, env, "update", "--channel", "stable")
		if err != nil {
			t.Fatalf("kern update --channel stable failed, want v0.9.9\n%s", out)
		}
		if !strings.Contains(out, "Target release: v0.9.9") || strings.Contains(out, "Target release: v0.9.9.1") {
			t.Errorf("channel stable resolved the wrong target:\n%s", out)
		}
		v := installedVersion(prefix, env)
		if !strings.Contains(v, "kern v0.9.9") || strings.Contains(v, "kern v0.9.9.1") {
			t.Fatalf("installed version = %q, want v0.9.9 (not the v0.9.9.1 hotfix)", v)
		}
	})

	// ---- Case 8: a channel resolving to an OLDER tag still requires --pin ----
	t.Run("channel-older-still-needs-pin", func(t *testing.T) {
		prefix := filepath.Join(base, "p8")
		install(prefix, dir991) // installed v0.9.9.1 > the stable target v0.9.9
		env := envFor(prefix, "KERN_CHANNEL=stable")
		out, err := runSh(env, "upgrade")
		if err == nil {
			t.Fatalf("install.sh stable-channel downgrade succeeded, want refusal\n%s", out)
		}
		if !strings.Contains(out, "newer than v0.9.9") || !strings.Contains(out, "--pin") {
			t.Errorf("output missing downgrade/--pin guidance:\n%s", out)
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "v0.9.9.1") {
			t.Fatalf("binary changed after refused channel downgrade: %q", v)
		}
		// The deliberate path still exists: --pin v0.9.9 consents.
		out, err = runKern(prefix, env, "update", "--pin", "v0.9.9")
		if err != nil {
			t.Fatalf("kern update --pin v0.9.9 failed, want deliberate downgrade\n%s", out)
		}
		if v := installedVersion(prefix, env); !strings.Contains(v, "kern v0.9.9") {
			t.Fatalf("installed version = %q, want v0.9.9", v)
		}
	})
}

// repoRoot walks up from the package directory to the go.mod owner.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above " + dir)
		}
		dir = parent
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
