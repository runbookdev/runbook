# runbook

Executable runbooks as code — documentation and automation in a single `.runbook` file format. Open-core commercial project: free OSS CLI + paid collaboration platform.

## Commands

- `make build` — Build the CLI binary to `./bin/runbook`
- `make test` — Run all tests with race detector (`go test -race ./...`)
- `make test-pkg PKG=parser` — Test a single package
- `make lint` — Run golangci-lint
- `make validate-templates` — Validate all .runbook files in `templates/`
- `make build-all` — Cross-compile for linux/darwin/windows (amd64+arm64)

## Architecture

- `cmd/runbook/` — main.go entry point
- `internal/parser/` — Lexer, block extractor, AST builder for .runbook files
- `internal/validator/` — 12 validation rules against the AST
- `internal/resolver/` — Template variable resolution (CLI flags > env vars > .env > built-ins)
- `internal/executor/` — Step execution, rollback engine, timeout management
- `internal/audit/` — SQLite-backed audit logger with secret redaction
- `internal/cli/` — Cobra command implementations (run, validate, dry-run, init, history)
- `internal/ast/` — Shared types: RunbookAST, StepNode, CheckNode, etc.
- `templates/` — Starter .runbook template files
- `docs/` — Reference documentation

## Code style

- Go standard formatting (`gofmt`)
- Use `internal/` for all packages — nothing is publicly importable
- Table-driven tests with testdata/ fixture files
- Error messages include file path and line number: `deploy.runbook:42: error: ...`
- Use Go interfaces at extension points (SecretProvider, Renderer)
- Wrap errors with `fmt.Errorf("context: %w", err)` — never bare returns

## The .runbook file format

Extended Markdown with typed fenced code blocks. Frontmatter is YAML (delimited by `---`). Body has four block types: `check`, `step`, `rollback`, `wait`. Template variables use `{{variable}}` syntax. See @docs/FORMAT.md for the full spec.

## Key design decisions

- Sequential execution only in Foundation Phase (DAG comes later)
- Rollback is best-effort, reverse-order (LIFO stack)
- Per-step timeouts use SIGTERM → 10s grace → SIGKILL
- Audit log is local SQLite at `~/.runbook/audit/runbook.db`
- Variables containing SECRET/PASSWORD/TOKEN/KEY/CREDENTIAL are auto-redacted in audit
- Exit codes: 0=success, 1=step-failed, 2=rolled-back, 3=validation-error, 4=check-failed, 10=aborted, 20=internal-error

## Important

- NEVER import packages from outside `internal/` — the public API is the CLI binary and file format only
- Always validate .runbook template files before committing (`make validate-templates`)
- Apache 2.0 licence — the commercial platform lives in a separate private repo
- The parser must handle nested code blocks (``` inside ```) gracefully
- Levenshtein distance for "did you mean?" suggestions on misspelled block references
- Add license headers to all new files
- Properly document every implementation with GoDoc comments
