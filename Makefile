BINARY    := runbook
PKG       := github.com/runbookdev/runbook
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Append .exe on Windows so `go build -o` produces an executable the
# OS will actually run. Using $(OS) (set by Windows itself) avoids
# shelling out to `go env GOEXE`, which keeps this working under any
# make variant on the CI image.
ifeq ($(OS),Windows_NT)
    GOEXE := .exe
else
    GOEXE :=
endif
BIN       := bin/$(BINARY)$(GOEXE)

.PHONY: build build-all release-dry-run test test-pkg fmt vet lint check validate-templates validate-bulk clean

build:
	CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/runbook

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

# Usage: make test-pkg P=bulk  (runs ./internal/bulk/...)
test-pkg:
	CGO_ENABLED=1 go test -race -count=1 -v ./internal/$(P)/...

fmt:
	gofmt -s -w .

vet:
	CGO_ENABLED=1 go vet ./...

lint:
	CGO_ENABLED=1 golangci-lint run ./...

# Pre-commit sanity: formatting, static analysis, race-enabled tests.
check: vet lint test

validate-templates: build
	@echo "Validating .runbook templates..."
	@FAIL=0; for f in templates/*.runbook; do \
		if ./$(BIN) validate "$$f" > /dev/null 2>&1; then \
			echo "  ✓ $$f"; \
		else \
			echo "  ✗ $$f"; \
			./$(BIN) validate "$$f" 2>&1 | sed 's/^/    /'; \
			FAIL=1; \
		fi; \
	done; \
	if [ "$$FAIL" = "1" ]; then echo "Template validation failed"; exit 1; fi

# Smoke-test the bulk command end-to-end against the built binary.
# Runs every example .runbook in parallel; non-zero exit means a
# runbook regressed or the bulk wiring broke.
validate-bulk: build
	@echo "Running bulk smoke test on example runbooks..."
	@./$(BIN) bulk --glob 'examples/**/*.runbook' --max-runbooks 2 --keep-going --report text --audit-dir /tmp/runbook-bulk-smoke.db

clean:
	rm -rf bin/ dist/ /tmp/runbook-bulk-smoke.db
