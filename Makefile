BINARY    := runbook
PKG       := github.com/runbookdev/runbook
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build build-all release-dry-run test test-pkg lint validate-templates clean

build:
	CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/runbook

build-all:
	CGO_ENABLED=1 GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64        ./cmd/runbook
	CGO_ENABLED=1 GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64        ./cmd/runbook
	CGO_ENABLED=1 GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-amd64       ./cmd/runbook
	CGO_ENABLED=1 GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64       ./cmd/runbook
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-amd64.exe  ./cmd/runbook

release-dry-run:
	@which goreleaser > /dev/null 2>&1 || { echo "installing goreleaser..."; go install github.com/goreleaser/goreleaser/v2@latest; }
	PATH="$(CURDIR)/scripts:$$PATH" goreleaser build --snapshot --clean

test:
	CGO_ENABLED=1 go test -race -count=1 ./...

test-pkg:
	CGO_ENABLED=1 go test -race -count=1 -v ./internal/$(PKG)/...

lint:
	CGO_ENABLED=1 golangci-lint run ./...

validate-templates: build
	@echo "Validating .runbook templates..."
	@FAIL=0; for f in templates/*.runbook; do \
		if ./bin/runbook validate "$$f" > /dev/null 2>&1; then \
			echo "  ✓ $$f"; \
		else \
			echo "  ✗ $$f"; \
			./bin/runbook validate "$$f" 2>&1 | sed 's/^/    /'; \
			FAIL=1; \
		fi; \
	done; \
	if [ "$$FAIL" = "1" ]; then echo "Template validation failed"; exit 1; fi

clean:
	rm -rf bin/ dist/
