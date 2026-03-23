# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## 2026-03-23

### Added

#### Core execution engine

- **Step executor** with full process-group management — `SIGTERM` → 10s grace → `SIGKILL` cascade covers child processes on Linux and macOS
- **Automatic rollback** — LIFO stack of `rollback` blocks executed in reverse order on step failure; best-effort (failures are logged and remaining rollbacks continue)
- **Dry-run mode** — previews the full execution plan without running any commands
- **Per-step timeouts** and a **global timeout** (`timeout:` frontmatter field) that caps total execution time
- **Signal handling** — `Ctrl+C` presents an interactive prompt: `[r]ollback / [c]ontinue / [q]uit`, with a 10-second auto-rollback default
- **Environment filtering** — steps tagged with `env:` are skipped when the target environment does not match
- **Confirmation gates** — steps with `confirm: <env>` prompt `[y]es / [n]o / [s]kip / [a]bort` before executing
- **Non-interactive mode** (`--non-interactive`) — auto-accepts all confirmation prompts for use in CI

#### File format

- **`.runbook` format** — extended Markdown with typed, fenced code blocks and YAML frontmatter
- Four block types: `check` (precondition), `step` (executable unit), `rollback` (recovery handler), `wait` (timed pause)
- Frontmatter fields: `name`, `version`, `environments`, `requires.tools`, `timeout`

#### Template variable resolution

- `{{name}}` syntax resolved via a four-level priority chain: CLI flags → environment variables → `.env` file → built-in variables
- Built-in variables: `{{env}}`, `{{runbook_name}}`, `{{runbook_version}}`, `{{run_id}}`, `{{timestamp}}`, `{{user}}`, `{{hostname}}`, `{{cwd}}`
- `--var key=value` flag (repeatable) and `--env-file <path>` for loading variables from a file

#### Parser

- Lexer and AST builder for `.runbook` files
- **Parser hardening** — files are rejected if they: exceed 1 MB, contain non-UTF-8 bytes, define more than 1,000 blocks, include unknown frontmatter fields, or have frontmatter larger than 64 KB

#### Validator

- 12 static-analysis validation rules with did-you-mean suggestions
- **Security rules** — detects credential patterns in commands, plain-HTTP `wget` fetches, pipe-to-shell patterns, and `.env` files not listed in `.gitignore`

#### Security capabilities (`runbook doctor`)

- New `doctor` command for health-checking the local installation and runbook files
- **`.env` permission checks** — warns when a `.env` file is world-readable (not `0600`)
- **Secret redaction** in audit logs — variables whose names contain `SECRET`, `PASSWORD`, `TOKEN`, `KEY`, or `CREDENTIAL` are automatically redacted before storage
- **Secure temp files** — generated script files are created with `0600` permissions and cleaned up after execution, even on timeout or kill
- **Root user warning** — emits a warning when the executor detects it is running as root
- **Orphan process cleanup** — dedicated Linux and non-Linux implementations ensure no child processes leak after step completion

#### Audit trail

- Full audit log persisted to a local SQLite database at `~/.runbook/audit/runbook.db`
- `runbook history` command — list recent runs with `--limit` and inspect a specific run with `--run-id` (prefix match)
- `--audit-dir` flag on `runbook run` for a custom audit database path

#### CLI commands

- `runbook run <file>` — execute a runbook (`--env`, `--var`, `--env-file`, `--non-interactive`, `--dry-run`, `--verbose`, `--audit-dir`, `--no-color`)
- `runbook validate <file>` — parse and validate; exits `0` on success, `3` on validation errors
- `runbook dry-run <file>` — alias for `run --dry-run`
- `runbook init [file]` — scaffold a new `.runbook` file from a template or minimal skeleton (`--template`)
- `runbook list-templates` — list all available built-in templates
- `runbook history` — query the audit log
- `runbook doctor [file]` — health-check the installation
- `runbook version` — print version, commit, build date, Go version, and platform

#### Exit codes

| Code | Meaning          |
|------|------------------|
| `0`  | Success          |
| `1`  | Step failed      |
| `2`  | Rolled back      |
| `3`  | Validation error |
| `4`  | Check failed     |
| `10` | Aborted          |
| `20` | Internal error   |

#### Built-in templates

10 production-ready templates scaffoldable via `runbook init --template=<name>`:

| Template            | Description                                              |
|---------------------|----------------------------------------------------------|
| `deploy`            | Service deployment with canary verification and rollback |
| `rollback`          | Manual rollback to a previous known-good version         |
| `failover`          | Database failover — promote replica to primary           |
| `cert-rotation`     | TLS certificate renewal and deployment                   |
| `db-migration`      | Database schema migration with backup and rollback       |
| `incident-response` | P1 incident triage, mitigation, and recovery             |
| `health-check`      | System-wide health verification across all services      |
| `scale-up`          | Horizontal scaling procedure for a service               |
| `backup-restore`    | Backup verification and restore test                     |
| `secret-rotation`   | Rotate API keys and credentials with zero downtime       |

#### Distribution

- Homebrew tap (`runbookdev/tap/runbook`) for macOS and Linux
- `go install github.com/runbookdev/runbook/cmd/runbook@latest`
- GitHub Releases with pre-built binaries (cross-compiled via Zig toolchain)
- GoReleaser configuration for automated release pipeline

#### Examples

- `examples/getting-started/` — annotated walkthrough runbook covering checks, steps, rollbacks, waits, variables, and confirmation gates
- `examples/docker-compose-deploy/` — real-world Docker Compose service deployment with health checks, migrations, and rollback

#### Configuration

- `~/.runbook/config.yaml` for persistent defaults (`env`, `env_file`, `audit_dir`, `non_interactive`, `no_color`, `shell`)
- CLI flags always override config file values

### Changed

- Refactored internal package structure: `parser`, `validator`, `resolver`, `executor`, `audit`, `cli`
- Improved CI pipeline with lint, test, and multi-platform build steps
- Updated Go toolchain requirement to Go 1.26

