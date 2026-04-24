<p align="center">
  <img src="assets/logo/logo-stacked.svg" alt="runbook" width="300" />
</p>

<p align="center">
  <a href="https://github.com/runbookdev/runbook/actions"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/runbookdev/runbook/ci.yml"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License" /></a>
  <a href="https://goreportcard.com/report/github.com/runbookdev/runbook"><img src="https://goreportcard.com/badge/github.com/runbookdev/runbook" alt="Go Report" /></a>
</p>

---

**Runbook** is executable operations documentation. A `.runbook` file is extended Markdown: the prose is the documentation a human reads during an incident, and typed fenced blocks (`check`, `step`, `rollback`, `wait`) are the procedure the system runs. Version it in Git, review it in PRs, test it in CI.

> Because the command and the explanation live in the same file, they cannot drift apart.

## Example

A self-contained demo ships at [`examples/getting-started/demo.runbook`](examples/getting-started/demo.runbook) and runs with standard Unix tools — no Docker, Kubernetes, or cloud credentials.

````markdown
---
name: Getting Started Demo
version: 1.0.0
environments: [staging, production]
timeout: 5m
---

# Getting Started Demo

This runbook simulates deploying **{{runbook_name}}** version
**{{runbook_version}}** to **{{env}}**.

```check name="tmp-writable"
test -w /tmp
```

```step name="deploy-version" depends_on="apply-migration" rollback="rollback-version"
  timeout: 30s
  confirm: production
  kill_grace: 5s
---
./scripts/deploy-version.sh "{{run_id}}" "{{runbook_version}}"
```

```wait name="health-soak" duration="15s"
  abort_if: error_rate > 0%
---
./scripts/health-soak.sh "{{run_id}}" "{{runbook_version}}"
```
````

Try it:

```bash
make build
./bin/runbook dry-run examples/getting-started/demo.runbook --env staging
./bin/runbook run     examples/getting-started/demo.runbook --env staging
```

## Install

### Homebrew

```bash
brew install runbookdev/tap/runbook
```

### Binary

Pre-built binaries for Linux, macOS, and Windows (amd64 / arm64) ship on every [release](https://github.com/runbookdev/runbook/releases), with SHA-256 checksums alongside.

```bash
# macOS / Linux
VERSION=$(curl -s https://api.github.com/repos/runbookdev/runbook/releases/latest | grep tag_name | cut -d '"' -f4)
OS=$(uname -s | tr A-Z a-z)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -sL "https://github.com/runbookdev/runbook/releases/download/${VERSION}/runbook_${VERSION#v}_${OS}_${ARCH}.tar.gz" | tar xz
sudo mv runbook /usr/local/bin/
```

```powershell
# Windows (PowerShell)
$VERSION = (Invoke-RestMethod https://api.github.com/repos/runbookdev/runbook/releases/latest).tag_name
$URL = "https://github.com/runbookdev/runbook/releases/download/$VERSION/runbook_$($VERSION.TrimStart('v'))_windows_amd64.zip"
Invoke-WebRequest -Uri $URL -OutFile runbook.zip
Expand-Archive runbook.zip -DestinationPath "$env:LOCALAPPDATA\runbook"
```

### Source

Requires **Go 1.26+** and a C compiler (CGO, for the embedded SQLite audit log).

```bash
git clone https://github.com/runbookdev/runbook.git && cd runbook
make build && sudo mv bin/runbook /usr/local/bin/
```

## Quick start

```bash
runbook init --template=deploy my-deploy.runbook
runbook dry-run my-deploy.runbook --env staging
runbook run     my-deploy.runbook --env staging --var service=api --var version=2.4.0

# Fan out: run many runbooks in parallel, or sweep one over a matrix
runbook bulk    --glob 'deploys/*.runbook' --max-runbooks 4
runbook bulk    my-deploy.runbook --matrix-var env=staging,prod --matrix-var region=us,eu

runbook history
```

## Documentation

| Guide                                          |                                                      |
|------------------------------------------------|------------------------------------------------------|
| [Getting started](docs/getting-started.md)     | Install, scaffold, run your first runbook            |
| [File format](docs/format.md)                  | Frontmatter, block types, syntax reference           |
| [CLI reference](docs/cli-reference.md)         | Commands, flags, exit codes                          |
| [Bulk execution](docs/bulk.md)                 | Run many runbooks or sweep one over a matrix         |
| [Shell integration](docs/shell-integration.md) | Completion, `rb` alias, prompt indicator             |
| [Project detection](docs/detect.md)            | Project types, environments, tool availability       |
| [Template variables](docs/variables.md)        | `{{variable}}` resolution and built-ins              |
| [Safety features](docs/safety.md)              | Rollback, timeouts, confirmation gates, signals      |
| [Security](docs/security.md)                   | Static analysis, secret redaction, secure temp files |
| [Audit logging](docs/audit.md)                 | Execution history and `runbook history`              |
| [Configuration](docs/configuration.md)         | `~/.runbook/config.yaml`                             |
| [Built-in templates](docs/templates.md)        | Production-ready starting points                     |

## More examples

- [`examples/getting-started`](examples/getting-started/) — checks, waits, rollbacks, confirmation gates
- [`examples/docker-compose-deploy`](examples/docker-compose-deploy/) — realistic Docker Compose deployment on a VPS or bare-metal host

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Bug reports, feature requests, and docs PRs welcome.

## License

Apache 2.0 — see [LICENSE](LICENSE).
