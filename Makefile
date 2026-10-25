BIN := bin
# Default VERSION to the repo HEAD short hash so every `make build` produces
# a parity-checkable binary (north-star NS-7); an explicit VERSION (release
# tag) overrides it. Both the legacy main.version and the shared
# internal/version.Version are stamped.
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
# -trimpath (a go-build flag, not an ld flag) keeps build-machine paths out
# of the binaries (LOW-15: 610 embedded /Users/... paths leaked provenance
# with no runtime dependence); -s -w in the LDFLAGS drop the symbol table
# and DWARF for the same reason.
GOFLAGS := -buildvcs=false -trimpath
LDFLAGS := -s -w -X main.version=$(VERSION) -X github.com/JayveerPrajapati/kern/internal/version.Version=$(VERSION)
RELEASE_LDFLAGS := -s -w -X main.version=$(VERSION) -X github.com/JayveerPrajapati/kern/internal/version.Version=$(VERSION)
# Checksum tool for the release manifest: prefer sha256sum (Linux), fall back
# to shasum -a 256 (macOS). Both emit "<hash>  <filename>" — the two-space
# separator that install.sh greps for when verifying a download.
SHA256SUM := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo "shasum -a 256")

.PHONY: all build build-purego test test-touched test-race vet lint bench bench-gate install install-purego hooks plugin-sync release dist release-smoke mcpb clean clean-artifacts help

all: build

# help lists every target declared in .PHONY — it cannot drift from the
# target list because it IS the target list (CONTRIBUTING.md points here).
help:
	@echo "kern Makefile targets:"
	@sed -n 's/^.PHONY: //p' $(MAKEFILE_LIST) | tr ' ' '\n' | sed '/^$$/d' | sed 's/^/  make /'

# build is the DEFAULT build: tree-sitter is compiled in (hard CGO — the
# grammar bindings compile C sources via `import "C"`). CGO_ENABLED=1 is
# explicit so the default stays deterministic on machines where CGO would
# otherwise be auto-disabled.
build:
	mkdir -p $(BIN)
	CGO_ENABLED=1 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern ./cmd/kern
	CGO_ENABLED=1 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern-mcp ./cmd/kern-mcp

# build-purego is the documented pure-Go opt-out: -tags notreesitter swaps
# the tree-sitter grammars for regex-heuristic extraction, so no C toolchain
# is needed. Release tarballs are built this way (see `release` below).
build-purego:
	mkdir -p $(BIN)
	CGO_ENABLED=0 go build -tags notreesitter $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern ./cmd/kern
	CGO_ENABLED=0 go build -tags notreesitter $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern-mcp ./cmd/kern-mcp

test:
	go test ./...
# test-short runs the fast CI path (skips the deep nightly integration
# tests: resilience real-execution scenarios, long index builds).
test-short:
	go test -short ./...
# test-touched runs ONLY the packages your current changes touch (working
# tree vs HEAD, or the last commit when clean), plus the gate packages that
# pin cross-package files — the plugin/AGENTS.md lesson (687ec36): those
# files are read by internal/setup AND internal/architecture, so touching
# them must run both. This is the iteration loop; `make test-short` stays
# the pre-merge ritual.
test-touched:
	@pkgs=$$(./scripts/touched-packages.sh); \
	if [ -n "$$pkgs" ]; then \
		echo "go test -count=1 $$pkgs"; \
		go test -count=1 $$pkgs; \
	else \
		echo "no touched packages — nothing to test"; \
	fi
# cover runs the short suite with coverage and prints the per-package
# summary. CI uploads coverage.out to Codecov; this is the local equivalent.
cover:
	go test -short -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out | tail -1

# test-race runs the test suite with the Go race detector. It is slower than
# `test` but catches data races in the event bus, gateway, stores, and loop.
# This is the deterministic-verification path for concurrency-sensitive code
# (Global Validation: go test -race ./...).
test-race:
	go test -race ./...

vet:
	go vet ./...

lint: vet
	@test -z "$$(gofmt -l . | grep -v '^\.kern/')" || { echo "gofmt needed on:"; gofmt -l . | grep -v '^\.kern/'; exit 1; }

bench:
	go test ./evaluate/bench/
	go run ./evaluate/bench
# bench-latency runs the deterministic cold/warm + query latency harness
# (`kern bench`) and publishes its JSON to .kern/bench.json — the document
# the web console /benchmarks page renders. Zero network by design.
bench-latency:
	go run ./cmd/kern bench --root .
# bench-gate is the benchmark regression gate (perfection plan B1): the
# same harness with --check, verifying the run against the versioned
# thresholds in cmd/kern/bench_thresholds.json. Fails (exit 1) on hard
# violations: the warm-load/query ceilings, the cold-vs-warm speedup floor
# (a warm Load that falls through to a full Build collapses it), or a
# zero-result query (wiring regression). Ceilings are deliberately generous
# so only real regressions fail, never machine noise.
bench-gate:
	go run ./cmd/kern bench --root . --check

install: build
	mkdir -p $${HOME}/.local/bin
	install -m 755 $(BIN)/kern $(BIN)/kern-mcp $${HOME}/.local/bin/
# macOS Gatekeeper SIGKILLs adhoc-signed binaries carrying the
# com.apple.provenance xattr on first launch (exit 137, empty output).
# Re-sign after copy and strip both quarantine and provenance xattrs so
# agents can launch kern-mcp without a Gatekeeper kill.
ifeq ($(shell uname -s),Darwin)
	codesign --force --sign - $${HOME}/.local/bin/kern $${HOME}/.local/bin/kern-mcp 2>/dev/null || true
	xattr -dr com.apple.quarantine $${HOME}/.local/bin/kern $${HOME}/.local/bin/kern-mcp 2>/dev/null || true
	xattr -dr com.apple.provenance $${HOME}/.local/bin/kern $${HOME}/.local/bin/kern-mcp 2>/dev/null || true
endif

install-purego: build-purego
	mkdir -p $${HOME}/.local/bin
	install -m 755 $(BIN)/kern $(BIN)/kern-mcp $${HOME}/.local/bin/
ifeq ($(shell uname -s),Darwin)
	codesign --force --sign - $${HOME}/.local/bin/kern $${HOME}/.local/bin/kern-mcp 2>/dev/null || true
	xattr -dr com.apple.quarantine $${HOME}/.local/bin/kern $${HOME}/.local/bin/kern-mcp 2>/dev/null || true
	xattr -dr com.apple.provenance $${HOME}/.local/bin/kern $${HOME}/.local/bin/kern-mcp 2>/dev/null || true
endif

# opencode hooks: MCP server + config + auto-discovered plugin + agent rules.
# This is the opencode-only convenience; `kern setup --global` (or
# `kern setup --detect` in a project) is the full path — it wires every
# detected agent (Claude, Codex, Cursor, ...), not just opencode.
hooks: build
	mkdir -p $${HOME}/.local/bin
	cp $(BIN)/kern-mcp $${HOME}/.local/bin/
# Same macOS Gatekeeper handling as `make install` (see the note there):
# re-sign after copy and strip quarantine/provenance xattrs.
ifeq ($(shell uname -s),Darwin)
	codesign --force --sign - $${HOME}/.local/bin/kern-mcp 2>/dev/null || true
	xattr -dr com.apple.quarantine $${HOME}/.local/bin/kern-mcp 2>/dev/null || true
	xattr -dr com.apple.provenance $${HOME}/.local/bin/kern-mcp 2>/dev/null || true
endif
	mkdir -p $${HOME}/.config/opencode
	cp opencode.json .opencode/plugins/kern.ts $${HOME}/.config/opencode/ 2>/dev/null || true
	cp AGENTS.md $${HOME}/.config/opencode/AGENTS.md

# Cross-compile release tarballs into dist/ (used by the release workflow).
# Usage: make release VERSION=v1.0.0
# Binaries match release.yml: built with -tags sqlite,notreesitter (pure-Go,
# CGO_ENABLED=0 safe). Tree-sitter is hard-CGO, so the release MUST opt out
# with notreesitter — cross-compiling the grammar C sources per target would
# need per-target C toolchains.
release: clean
	$(eval VERSION := $(if $(filter v%,$(VERSION)),$(VERSION),v$(VERSION)))
	mkdir -p $(BIN)
	@set -e; for target in "linux amd64" "linux arm64" "darwin amd64" "darwin arm64"; do \
		set -- $$target; os=$$1; arch=$$2; \
		echo "==> building kern-$$os-$$arch"; \
		mkdir -p $(BIN)/kern-$$os-$$arch; \
		GOOS=$$os GOARCH=$$arch go build -tags sqlite,notreesitter $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-$$os-$$arch/kern ./cmd/kern; \
		GOOS=$$os GOARCH=$$arch go build -tags sqlite,notreesitter $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-$$os-$$arch/kern-mcp ./cmd/kern-mcp; \
		tar -C $(BIN)/kern-$$os-$$arch -czf $(BIN)/kern-$$os-$$arch.tar.gz .; \
		rm -rf $(BIN)/kern-$$os-$$arch; \
	done; \
	mkdir -p $(BIN)/kern-windows-amd64; \
	GOOS=windows GOARCH=amd64 go build -tags sqlite,notreesitter $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-windows-amd64/kern.exe ./cmd/kern; \
	GOOS=windows GOARCH=amd64 go build -tags sqlite,notreesitter $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-windows-amd64/kern-mcp.exe ./cmd/kern-mcp; \
	cd $(BIN)/kern-windows-amd64 && zip -q -r ../kern-windows-amd64.zip . && cd .. && rm -rf kern-windows-amd64
	mkdir -p $(BIN)/kern-windows-arm64; \
	GOOS=windows GOARCH=arm64 go build -tags sqlite,notreesitter $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-windows-arm64/kern.exe ./cmd/kern; \
	GOOS=windows GOARCH=arm64 go build -tags sqlite,notreesitter $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-windows-arm64/kern-mcp.exe ./cmd/kern-mcp; \
	cd $(BIN)/kern-windows-arm64 && zip -q -r ../kern-windows-arm64.zip . && cd .. && rm -rf kern-windows-arm64
	# Checksum manifest for every release archive (same format the release
	# workflow ships): shasum -a 256 (macOS) and sha256sum (Linux) both emit
	# "<hash>  <filename>" — the two-space separator install.sh greps for.
	# Verify with: cd $(BIN) && shasum -a 256 -c SHA256SUMS
	cd $(BIN) && $(SHA256SUM) kern-*.tar.gz kern-*.zip > SHA256SUMS
	@echo "release assets in $(BIN):"; ls $(BIN)/*.tar.gz $(BIN)/*.zip

dist: release

# release-smoke verifies the PACKAGED artifacts (plan B3), not the dev
# binary: SHA256SUMS verification, current-platform archive extraction,
# version stamps, a live MCP stdio handshake (default surface 29 tools,
# KERN_MCP_FULL=1 31, kern_arch/kern_graph full-only), a real index+search
# run on this repo, and `kern doctor` with zero [fail] findings. Run it
# after `make release VERSION=<tag>`; point SMOKE_DIR at a downloaded
# release directory to smoke a published release the same way.
release-smoke:
	./scripts/release-smoke.sh --dir $(BIN) --repo . $(if $(VERSION),--version $(VERSION),)

# Build a .mcpb bundle for MCP registry distribution (future releases).
# Usage: make mcpb VERSION=v0.1.0
# plugin-sync copies the canonical .opencode/plugins/kern.ts into the
# embedded asset (go:embed in internal/agentwire/shared.go) so `kern setup`
# deploys identical bytes everywhere. Run it after editing the plugin, then
# append the new sha256 to internal/pluginhash's shipped registry (the
# registry test prints it). --check verifies parity without writing, for CI.
plugin-sync: build
	./$(BIN)/kern gen-plugin --root .
mcpb: build
	@mkdir -p dist
	@echo '{"manifest":{"name":"kern","version":"$(VERSION)","transport":{"type":"stdio"},"command":"kern","args":["mcp"]}}' > dist/manifest.json
	@cp $(BIN)/kern dist/
	@cd dist && zip -j kern-$(VERSION).mcpb manifest.json kern
	@echo "Built dist/kern-$(VERSION).mcpb"
	@echo "SHA256: $$(openssl dgst -sha256 dist/kern-$(VERSION).mcpb | awk '{print $$2}')"
	@rm dist/manifest.json dist/kern

clean:
	rm -rf $(BIN)
	rm -rf dist

# clean-artifacts removes stray build/test binaries and bytecode caches left
# at the repo root by ad-hoc builds (go build without -o, go test -c, pip
# imports). All targets are gitignored, so this only reclaims disk space.
clean-artifacts:
	rm -f kern kern-mcp
	rm -f *.test
	rm -f bench
	rm -rf __pycache__
