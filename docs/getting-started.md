# Getting Started

## Install

### Homebrew (macOS / Linux)

```bash
brew install runbookdev/tap/runbook
```

### Go install

```bash
go install github.com/runbookdev/runbook/cmd/runbook@latest
```

### Binary download

Download a pre-built binary from the [GitHub Releases](https://github.com/runbookdev/runbook/releases) page and place it on your `PATH`.

### Build from source

```bash
git clone https://github.com/runbookdev/runbook.git
cd runbook
make build          # produces ./bin/runbook
```

Requires Go 1.26+ and CGO enabled (used by the SQLite audit logger).

---

## Your first runbook in 60 seconds

```bash
# 1. Scaffold a runbook from a built-in template
runbook init --template=deploy my-deploy.runbook

# 2. Validate it parses correctly (exit 0 = valid, exit 3 = errors)
runbook validate my-deploy.runbook

# 3. Preview the execution plan — nothing actually runs
runbook dry-run my-deploy.runbook --env=staging

# 4. Execute it for real
runbook run my-deploy.runbook --env=staging \
  --var service=api --var version=2.4.0

# 5. Review the audit log
runbook history
```

---

## Try the self-contained demo

The repository ships with a demo runbook that runs without any external
services or credentials — just standard Unix tools and a temp directory.

```bash
cd examples/getting-started

# Staging run — no confirmation prompts
runbook run demo.runbook --env staging

# Production run — shows the confirmation gate for the deploy step
runbook run demo.runbook --env production
```

The demo exercises every feature: check blocks, step blocks with rollback,
a `wait` block with health polling, `depends_on` chaining, `confirm` gates,
environment filters, `kill_grace`, and built-in template variables.

---

## What a .runbook file looks like

````markdown
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

## Step 2: Deploy

```step name="deploy-app" depends_on="migrate-db"
  timeout: 300s
---
kubectl set image deployment/api api={{image}}:{{version}}
kubectl rollout status deployment/api
```
````

The Markdown body is the documentation a human reads. The typed code blocks
(`check`, `step`, `rollback`, `wait`) are the executable units.

---

## Next steps

- [File format reference](format.md) — full syntax for all block types and frontmatter fields
- [CLI reference](cli-reference.md) — all commands, flags, and exit codes
- [Shell integration](shell-integration.md) — tab completion, `rb` alias, `runbook-detect`, and prompt indicator
- [Project detection](detect.md) — how project types, environments, and tool availability are detected
- [Template variables](variables.md) — `{{variable}}` resolution and built-ins
- [Safety features](safety.md) — rollback, timeouts, confirmation gates, signal handling
- [Security](security.md) — static analysis, secret redaction, secure temp files
- [Audit logging](audit.md) — execution history and the SQLite database
- [Configuration](configuration.md) — `~/.runbook/config.yaml`
- [Built-in templates](templates.md) — 10 production-ready starting points
