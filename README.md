<p align="center">
  <img src="assets/logo/logo-stacked.svg" alt="runbook" width="300" />
</p>

<p align="center">
  <a href="https://github.com/runbookdev/runbook/actions"><img alt="GitHub Actions Workflow Status" src="https://img.shields.io/github/actions/workflow/status/runbookdev/runbook/ci.yml"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License" /></a>
  <a href="https://goreportcard.com/report/github.com/runbookdev/runbook"><img src="https://goreportcard.com/badge/github.com/runbookdev/runbook" alt="Go Report" /></a>
</p>

---

## The problem

Operations runbooks are broken everywhere. Wiki pages go stale within weeks. Shell scripts are executable but unreadable. Vendor tools are locked-in and expensive. Teams are forced to choose between documentation that nobody trusts and automation that nobody can read.

## The solution

A **`.runbook`** file is simultaneously:

- A **human-readable document** that explains what, why, and how
- An **executable program** with typed steps, preconditions, rollback logic, and environment awareness
- A **version-controlled artifact** that lives alongside your code, reviewed in PRs

```markdown
---
name: Deploy API to Production
version: 2.4.0
environments: [staging, production]
requires:
  tools: [kubectl, jq]
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

## Quick start

```bash
# Install
go install github.com/runbookdev/runbook@latest

# Create your first runbook
runbook init --template=deploy

# Validate it
runbook validate deploy.runbook

# Dry run (see what would happen)
runbook dry-run deploy.runbook --env=staging

# Execute
runbook run deploy.runbook --env=staging
```

## Features

- 📄 **Markdown-native** — renders beautifully on GitHub, works as documentation
- ▶️ **Executable** — run steps with rollback chains, timeouts, and confirmation gates
- 🔒 **Safe by default** — dry-run mode, precondition checks, environment-aware execution
- 🔍 **Observable** — every execution produces a structured SQLite audit log
- 📦 **Single binary** — zero dependencies, cross-platform (Linux, macOS, Windows)
- 🔧 **Git-native** — version-controlled, reviewed in PRs, tested in CI

## Documentation

Full documentation is available at [runbookdev.github.io/docs](https://runbookdev.github.io/docs).

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
