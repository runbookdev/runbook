<p align="center">
  <img src="assets/logo/logo-stacked.svg" alt="runbook" width="300" />
</p>

<p align="center">
  <a href="https://github.com/runbookdev/runbook/actions"><img alt="GitHub Actions Workflow Status" src="https://img.shields.io/github/actions/workflow/status/runbookdev/runbook/ci.yml"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License" /></a>
  <a href="https://goreportcard.com/report/github.com/runbookdev/runbook"><img src="https://goreportcard.com/badge/github.com/runbookdev/runbook" alt="Go Report" /></a>
</p>

---

## 😤 The problem

Operations runbooks are broken everywhere.

Wiki pages go stale within weeks. Shell scripts are executable but unreadable. Vendor tools are locked-in and expensive. Teams are forced to choose between **documentation that nobody trusts** and **automation that nobody can read**.

## 💡 The solution

A **`.runbook`** file is simultaneously:

- 📄 A **human-readable document** that explains what, why, and how
- ⚡ An **executable program** with typed steps, preconditions, rollback logic, and environment awareness
- 🔀 A **version-controlled artifact** that lives alongside your code, reviewed in PRs

```markdown
---
name: Deploy API to Production
version: 2.4.0
environments: [staging, production]
requires:
  tools: [kubectl, jq]
timeout: 30m
---

# Deploy API to Production

## Prerequisites

```check name="cluster-healthy"
kubectl get nodes | grep -c Ready | test $(cat) -ge 3
```

## Step 1: Run Migration

```step name="migrate-db" rollback="rollback-db"
  timeout: 300s
  confirm: production
---
./scripts/migrate.sh --env={{env}} --version={{version}}
```

```rollback name="rollback-db"
./scripts/migrate.sh --env={{env}} --rollback-to={{previous_version}}
```
```

## 🚀 Quick start

### Install

```bash
# Homebrew (macOS/Linux)
brew install runbookdev/tap/runbook

# Go install
go install github.com/runbookdev/runbook/cmd/runbook@latest

# Or download a binary from GitHub Releases
# https://github.com/runbookdev/runbook/releases
```

### Your first runbook in 60 seconds

```bash
# 1️⃣  Scaffold a runbook from a template
runbook init --template=deploy my-deploy.runbook

# 2️⃣  Validate it parses correctly
runbook validate my-deploy.runbook

# 3️⃣  Preview the execution plan (nothing runs)
runbook dry-run my-deploy.runbook --env=staging

# 4️⃣  Execute it for real
runbook run my-deploy.runbook --env=staging \
  --var service=api --var version=2.4.0

# 5️⃣  Check the audit log
runbook history
```

### Sample output

```
[runbook] running 1 checks
[runbook] check [1/1] "cluster-healthy"
[check:cluster-healthy] | 4 nodes ready
[runbook] running 2 steps
[runbook] step [1/2] "migrate-db"
[migrate-db] | Migrating to v2.4.0...
[migrate-db] | Migration complete
[runbook] step [2/2] "deploy-app"
[deploy-app] | deployment.apps/api updated
[deploy-app] | Rollout complete
[runbook] complete (47s)

── Summary ──────────────────────────────────────
  ✓ [1/2] migrate-db (12s)
  ✓ [2/2] deploy-app (35s)

  Result: SUCCESS (47s)
─────────────────────────────────────────────────
```

## ✨ Features

### 📝 File format

| Block      | Purpose                                            | Example                                          |
|------------|----------------------------------------------------|--------------------------------------------------|
| `check`    | Precondition that must pass before execution       | Verify cluster health, check no active incidents |
| `step`     | Executable unit of work with optional rollback     | Deploy a service, run a migration                |
| `rollback` | Recovery handler executed in LIFO order on failure | Undo a migration, roll back a deployment         |
| `wait`     | Timed pause with monitoring and abort conditions   | Canary soak period, connection drain             |

### 🔒 Safety built in

- **Precondition checks** — gates that must pass before any step runs
- **Confirmation gates** — prompt `[y]es / [n]o / [s]kip / [a]bort` for dangerous steps (e.g., `confirm: production`)
- **Dry-run mode** — preview the full execution plan without running anything
- **Per-step timeouts** — `SIGTERM` → 10s grace → `SIGKILL`, including child processes via process groups
- **Global timeout** — set `timeout: 30m` in frontmatter to cap total execution time
- **Environment filtering** — steps tagged with `env: [staging]` skip in production

### 🔄 Automatic rollback

When a step fails, `runbook` pops a LIFO stack of rollback blocks and executes them in reverse order. Rollback is **best-effort** — if a rollback block itself fails, the failure is logged and the remaining rollbacks continue.

```
[runbook] step "deploy" failed (exit code 1)
[rollback] starting rollback (2 blocks, trigger: step_failure)
[rollback] executing "undo-migrate" (1 of 2)
[rollback] "undo-migrate" succeeded (3s)
[rollback] executing "undo-setup" (2 of 2)
[rollback] "undo-setup" succeeded (1s)
[rollback] complete: 2 succeeded, 0 failed
```

### 🔍 Full audit trail

Every execution is recorded in a local SQLite database at `~/.runbook/audit/runbook.db`. Variables containing `SECRET`, `PASSWORD`, `TOKEN`, `KEY`, or `CREDENTIAL` are automatically redacted.

```bash
# Recent runs
runbook history

# Details for a specific run (prefix match on ID)
runbook history --run-id=a1b2c3

# Custom audit location
runbook run deploy.runbook --audit-dir=/var/log/runbook/audit.db
```

### 🔧 Template variables

Variables use `{{name}}` syntax and resolve in priority order:

| Priority    | Source                | Example                                |
|-------------|-----------------------|----------------------------------------|
| 1 (highest) | CLI flags             | `--var region=us-east-1`               |
| 2           | Environment variables | `RUNBOOK_REGION=us-east-1`             |
| 3           | `.env` file           | `--env-file=.env.staging`              |
| 4 (lowest)  | Built-in variables    | `{{env}}`, `{{timestamp}}`, `{{user}}` |

**Built-in variables:**

| Variable              | Value                           |
|-----------------------|---------------------------------|
| `{{env}}`             | Target environment from `--env` |
| `{{runbook_name}}`    | Name from frontmatter           |
| `{{runbook_version}}` | Version from frontmatter        |
| `{{run_id}}`          | Unique UUID for this execution  |
| `{{timestamp}}`       | UTC time in RFC 3339            |
| `{{user}}`            | Current OS user                 |
| `{{hostname}}`        | Machine hostname                |
| `{{cwd}}`             | Current working directory       |

### 🛑 Signal handling

Press `Ctrl+C` during execution and choose:

```
[runbook] interrupt received. Choose action:
  [r]ollback — stop and roll back completed steps (default)
  [c]ontinue — resume execution
  [q]uit     — stop immediately, no rollback
  Action [r/c/q] (timeout 10s):
```

Defaults to rollback after 10 seconds with no response.

## 📋 CLI reference

```
runbook run <file>           Execute a runbook
  --env <name>               Target environment (staging, production, ...)
  --var key=value            Set a variable (repeatable)
  --env-file <path>          Load variables from a .env file
  --non-interactive          Skip all confirmation prompts (auto-yes)
  --dry-run                  Show the plan without executing
  --verbose                  Show debug-level details (commands, timing)
  --audit-dir <path>         Custom audit database path
  --no-color                 Disable colored output

runbook validate <file>      Parse and validate, exit 0 if valid, 3 if errors

runbook dry-run <file>       Alias for run --dry-run

runbook init [file]          Create a .runbook file
  --template <name>          Use a built-in template (see list-templates)

runbook list-templates       Show available templates

runbook history              Query the audit log
  --run-id <id>              Show details for a specific run
  --limit <n>                Number of recent runs (default: 20)

runbook version              Print version, commit, and build date
```

### 🔢 Exit codes

| Code | Meaning          |
|------|------------------|
| `0`  | Success          |
| `1`  | Step failed      |
| `2`  | Rolled back      |
| `3`  | Validation error |
| `4`  | Check failed     |
| `10` | Aborted          |
| `20` | Internal error   |

## 📦 Built-in templates

Get started quickly with 10 production-ready templates:

```bash
runbook list-templates
```

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

```bash
# Create from a template
runbook init --template=deploy my-deploy.runbook
runbook init --template=incident-response oncall.runbook

# Or start with a minimal skeleton
runbook init my-runbook.runbook
```

## 🏗️ Architecture

```
.runbook file
    │
    ▼
┌──────────┐     ┌────────────┐     ┌────────────┐
│  Parser  │────▶│ Validator  │────▶│  Resolver  │
│          │     │ (12 rules) │     │ (variables)│
└──────────┘     └────────────┘     └────────────┘
                                          │
                                          ▼
                                    ┌────────────┐
                                    │  Executor  │
                                    │            │
                                    │ checks ──▶ │
                                    │ steps  ──▶ │──▶ audit log
                                    │ rollback ◀─│
                                    └────────────┘
```

| Package              | Responsibility                                    |
|----------------------|---------------------------------------------------|
| `internal/parser`    | Lexer, block extractor, AST builder               |
| `internal/validator` | 12 validation rules with did-you-mean suggestions |
| `internal/resolver`  | Template variable resolution with priority chain  |
| `internal/executor`  | Step execution, rollback engine, signal handling  |
| `internal/audit`     | SQLite audit logger with secret redaction         |
| `internal/cli`       | Cobra command tree, colored terminal output       |

## ⚙️ Configuration

Create `~/.runbook/config.yaml` to set defaults:

```yaml
# Default environment
env: staging

# Default .env file
env_file: .env.local

# Audit database location
audit_dir: ~/.runbook/audit/runbook.db

# Skip prompts
non_interactive: false

# Disable colors
no_color: false

# Override shell
shell: /bin/bash
```

CLI flags always override config file values.

## 🔨 Building from source

```bash
git clone https://github.com/runbookdev/runbook.git
cd runbook

# Build
make build

# Run tests
make test

# Lint
make lint

# Validate all templates
make validate-templates

# Cross-compile for all platforms
make build-all
```

Requires Go 1.26+ and CGO (for SQLite).

## 🤝 Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

- 🐛 **Bugs**: Open an issue with reproduction steps
- 💡 **Features**: Open a discussion first to align on design
- 📖 **Docs**: PRs for documentation improvements are always welcome
- 🧪 **Tests**: We use table-driven tests with `testdata/` fixtures

## 📄 License

Apache 2.0 — see [LICENSE](LICENSE) for details.
