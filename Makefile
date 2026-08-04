BIN := bin
VERSION ?= dev
LDFLAGS := -X main.version=$(VERSION)
GOFLAGS := -buildvcs=false

.PHONY: all build test vet install hooks release dist clean

all: build

build:
	mkdir -p $(BIN)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern ./cmd/kern
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN)/kern-mcp ./cmd/kern-mcp

test:
	go test ./...

vet:
	go vet ./...

install: build
	cp $(BIN)/kern $(BIN)/kern-mcp $${HOME}/.local/bin/

# opencode hooks: MCP config + auto-discovered plugin + agent rules
hooks: build
	cp $(BIN)/kern-mcp $${HOME}/.local/bin/
	mkdir -p $${HOME}/.config/opencode
	cp opencode.json .opencode/plugins/kern.ts $${HOME}/.config/opencode/ 2>/dev/null || true
	cp AGENTS.md $${HOME}/.config/opencode/AGENTS.md

# Cross-compile release tarballs into dist/ (used by the release workflow).
# Usage: make release VERSION=v1.0.0
release: clean
	$(eval VERSION := $(if $(filter v%,$(VERSION)),$(VERSION),v$(VERSION)))
	mkdir -p $(BIN)
	@set -e; for target in "linux amd64" "linux arm64" "darwin amd64" "darwin arm64"; do \
		set -- $$target; os=$$1; arch=$$2; \
		echo "==> building kern-$$os-$$arch"; \
		GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN)/kern-$$os-$$arch ./cmd/kern; \
		GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN)/kern-mcp-$$os-$$arch ./cmd/kern-mcp; \
		mkdir -p $(BIN)/kern-$$os-$$arch; \
		mv $(BIN)/kern-$$os-$$arch $(BIN)/kern-mcp-$$os-$$arch $(BIN)/kern-$$os-$$arch/; \
		tar -C $(BIN) -czf $(BIN)/kern-$$os-$$arch.tar.gz kern-$$os-$$arch/; \
		rm -rf $(BIN)/kern-$$os-$$arch; \
	done; \
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN)/kern-windows-amd64.exe ./cmd/kern; \
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -ldflags "-X main.version=$(VERSION)" -o $(BIN)/kern-mcp-windows-amd64.exe ./cmd/kern-mcp; \
	cd $(BIN) && zip -q kern-windows-amd64.zip kern-windows-amd64.exe kern-mcp-windows-amd64.exe && rm -f kern-windows-amd64.exe kern-mcp-windows-amd64.exe
	@echo "release assets in $(BIN):"; ls $(BIN)/*.tar.gz $(BIN)/*.zip

dist: release

clean:
	rm -rf $(BIN)
