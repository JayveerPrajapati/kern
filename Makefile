BIN := bin
VERSION ?= dev
LDFLAGS := -X main.version=$(VERSION)
RELEASE_LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -buildvcs=false

.PHONY: all build build-treesitter test test-race vet lint bench install hooks release dist mcpb clean clean-artifacts

all: build

build:
	mkdir -p $(BIN)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern ./cmd/kern
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern-mcp ./cmd/kern-mcp
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern-server ./cmd/kern-server

# build-treesitter builds with tree-sitter support (requires CGO and Go 1.23+).
# Uses inotifywait/fswatch for file events and tree-sitter for precise parsing.
build-treesitter:
	mkdir -p $(BIN)
	CGO_ENABLED=1 go build -tags treesitter $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern ./cmd/kern
	CGO_ENABLED=1 go build -tags treesitter $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern-mcp ./cmd/kern-mcp
	CGO_ENABLED=1 go build -tags treesitter $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern-server ./cmd/kern-server

test:
	go test ./...

# test-race runs the test suite with the Go race detector. It is slower than
# `test` but catches data races in the event bus, gateway, stores, and loop.
# This is the deterministic-verification path for concurrency-sensitive code
# (Global Validation: go test -race ./...).
test-race:
	go test -race ./...

vet:
	go vet ./...

lint: vet
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }

bench:
	go test ./evaluate/bench/
	go run ./evaluate/bench

install: build
	mkdir -p $${HOME}/.local/bin
	cp $(BIN)/kern $(BIN)/kern-mcp $(BIN)/kern-server $${HOME}/.local/bin/

# opencode hooks: MCP config + auto-discovered plugin + agent rules
hooks: build
	cp $(BIN)/kern-mcp $${HOME}/.local/bin/
	mkdir -p $${HOME}/.config/opencode
	cp opencode.json .opencode/plugins/kern.ts $${HOME}/.config/opencode/ 2>/dev/null || true
	cp AGENTS.md $${HOME}/.config/opencode/AGENTS.md

# Cross-compile release tarballs into dist/ (used by the release workflow).
# Usage: make release VERSION=v1.0.0
# Binaries match release.yml: built with -tags sqlite (pure-Go, CGO_ENABLED=0 safe).
release: clean
	$(eval VERSION := $(if $(filter v%,$(VERSION)),$(VERSION),v$(VERSION)))
	mkdir -p $(BIN)
	@set -e; for target in "linux amd64" "linux arm64" "darwin amd64" "darwin arm64"; do \
		set -- $$target; os=$$1; arch=$$2; \
		echo "==> building kern-$$os-$$arch"; \
		mkdir -p $(BIN)/kern-$$os-$$arch; \
		GOOS=$$os GOARCH=$$arch go build -tags sqlite $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-$$os-$$arch/kern ./cmd/kern; \
		GOOS=$$os GOARCH=$$arch go build -tags sqlite $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-$$os-$$arch/kern-mcp ./cmd/kern-mcp; \
		GOOS=$$os GOARCH=$$arch go build -tags sqlite $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-$$os-$$arch/kern-server ./cmd/kern-server; \
		tar -C $(BIN) -czf $(BIN)/kern-$$os-$$arch.tar.gz kern-$$os-$$arch/; \
		rm -rf $(BIN)/kern-$$os-$$arch; \
	done; \
	mkdir -p $(BIN)/kern-windows-amd64; \
	GOOS=windows GOARCH=amd64 go build -tags sqlite $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-windows-amd64/kern.exe ./cmd/kern; \
	GOOS=windows GOARCH=amd64 go build -tags sqlite $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-windows-amd64/kern-mcp.exe ./cmd/kern-mcp; \
	GOOS=windows GOARCH=amd64 go build -tags sqlite $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-windows-amd64/kern-server.exe ./cmd/kern-server; \
	cd $(BIN) && zip -q -r kern-windows-amd64.zip kern-windows-amd64/ && rm -rf kern-windows-amd64
	mkdir -p $(BIN)/kern-windows-arm64; \
	GOOS=windows GOARCH=arm64 go build -tags sqlite $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-windows-arm64/kern.exe ./cmd/kern; \
	GOOS=windows GOARCH=arm64 go build -tags sqlite $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-windows-arm64/kern-mcp.exe ./cmd/kern-mcp; \
	GOOS=windows GOARCH=arm64 go build -tags sqlite $(GOFLAGS) -ldflags "$(RELEASE_LDFLAGS)" -o $(BIN)/kern-windows-arm64/kern-server.exe ./cmd/kern-server; \
	cd $(BIN) && zip -q -r kern-windows-arm64.zip kern-windows-arm64/ && rm -rf kern-windows-arm64
	@echo "release assets in $(BIN):"; ls $(BIN)/*.tar.gz $(BIN)/*.zip

dist: release

# Build a .mcpb bundle for MCP registry distribution (future releases).
# Usage: make mcpb VERSION=v0.1.0
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
	rm -f kern kern-mcp kern-server blueprint blueprint-mcp
	rm -f *.test
	rm -f bench
	rm -rf __pycache__
