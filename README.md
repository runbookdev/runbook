<p align="center">
  <img src="assets/logo/logo-stacked.svg" alt="runbook" width="300" />
</p>

<p align="center">
  <a href="https://github.com/runbookdev/runbook/actions"><img alt="GitHub Actions Workflow Status" src="https://img.shields.io/github/actions/workflow/status/runbookdev/runbook/ci.yml"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License" /></a>
  <a href="https://goreportcard.com/report/github.com/runbookdev/runbook"><img src="https://goreportcard.com/badge/github.com/runbookdev/runbook" alt="Go Report" /></a>
</p>

---

## 😵 Why Ops Runbooks Fail

Operations runbooks usually break in one of three ways:

- 📄 **Stale documentation**: wiki pages decay fast and become dangerous during incidents
- 🧩 **Unreadable automation**: scripts execute, but they do not explain intent, approvals, or rollback
- 🔒 **Vendor lock-in**: proprietary tools hide procedures outside Git, PR review, and CI

That leads to real pain:

- ⏱️ Slower incident response because procedures are outdated or missing
- 🧠 More tribal knowledge because new engineers cannot self-serve
- 🔁 Repeat incidents because fixes are never codified into the workflow
- 🧾 Weak audit trails for compliance-heavy operational work
- 🛠️ Duplicate maintenance across docs, scripts, and internal playbooks

### 🌍 Why now

- 🌱 **GitOps maturity**: infrastructure is already managed as code, but runbooks often are not
- 🚨 **Incident tooling maturity**: incident platforms improved, but the actual procedures still live in wikis
- 🏛️ **Compliance pressure**: SOC 2, ISO 27001, and DORA increasingly expect auditable operational workflows
- 🤖 **AI readiness**: structured, executable runbooks are a strong foundation for AI-assisted operations

## ✨ The solution

> **Documentation and automation are the same file.** When you update the command, you update the
> explanation in the same commit. They can never drift apart.

A **`.runbook`** file is extended Markdown with typed, fenced code blocks. The frontmatter defines
metadata, permissions, and approval rules. The Markdown body is the documentation. Specially-typed
code blocks (`check`, `step`, `rollback`, `wait`) are the executable units.

This means a single file is the document a human reads during an incident **and** the executable
procedure the system runs. It lives in your Git repo, is reviewed in PRs, and is tested in CI.

- 📘 A **human-readable document** that explains what, why, and how
- ⚙️ An **executable program** with typed steps, checks, waits, rollback logic, and environment awareness
- 🌿 A **version-controlled artifact** that lives alongside your code and evolves with it

## 🧪 Example: A Real `.runbook`

The repository ships with a self-contained example in [`examples/getting-started/demo.runbook`](examples/getting-started/demo.runbook).
It runs with standard Unix tools only, so you can try it without Docker, Kubernetes, or cloud credentials.

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
echo "/tmp is writable"
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

Try it locally:

```bash
# From the repo root
make build

# Run the self-contained demo
./bin/runbook run examples/getting-started/demo.runbook --env staging

# Preview the execution plan without running anything
./bin/runbook dry-run examples/getting-started/demo.runbook --env staging
```

What this example demonstrates:

- ✅ `check` blocks for prerequisites
- 🪜 `step` blocks with `depends_on`
- ♻️ `rollback` handlers for safe failure recovery
- ⏳ `wait` blocks with health polling
- 🛡️ `confirm: production` gates for sensitive actions
- 🌐 `env` filters and built-in template variables

## 📦 Installation

### Homebrew (macOS / Linux)

```bash
brew install runbookdev/tap/runbook
```

### Download binary

Pre-built binaries are published on every [GitHub release](https://github.com/runbookdev/runbook/releases)
for **Linux** (amd64, arm64), **macOS** (amd64, arm64), and **Windows** (amd64).

#### macOS / Linux

```bash
# Detect platform and download the latest release
VERSION=$(curl -s https://api.github.com/repos/runbookdev/runbook/releases/latest | grep tag_name | cut -d '"' -f4)
OS=$(uname -s | tr A-Z a-z)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

curl -sL "https://github.com/runbookdev/runbook/releases/download/${VERSION}/runbook_${VERSION#v}_${OS}_${ARCH}.tar.gz" | tar xz
sudo mv runbook /usr/local/bin/
```

#### Windows (PowerShell)

```powershell
# Download the latest release
$VERSION = (Invoke-RestMethod https://api.github.com/repos/runbookdev/runbook/releases/latest).tag_name
$URL = "https://github.com/runbookdev/runbook/releases/download/$VERSION/runbook_$($VERSION.TrimStart('v'))_windows_amd64.zip"

Invoke-WebRequest -Uri $URL -OutFile runbook.zip
Expand-Archive runbook.zip -DestinationPath "$env:LOCALAPPDATA\runbook"
# Add to PATH (current session)
$env:PATH += ";$env:LOCALAPPDATA\runbook"
```

> **Tip:** Add `$env:LOCALAPPDATA\runbook` to your system PATH for permanent access.

### Verify checksum

Every release includes a `checksums.txt` file (SHA-256). After downloading, verify the archive integrity:

```bash
sha256sum --check checksums.txt --ignore-missing
```

### Build from source

Requires **Go 1.26+** and a C compiler (CGO is needed for the embedded SQLite audit log).

```bash
git clone https://github.com/runbookdev/runbook.git
cd runbook
make build
sudo mv bin/runbook /usr/local/bin/
```

## ⚡ Quick Start

```bash
# Scaffold from a template
runbook init --template=deploy my-deploy.runbook

# Preview (nothing runs)
runbook dry-run my-deploy.runbook --env staging

# Execute
runbook run my-deploy.runbook --env staging --var service=api --var version=2.4.0

# Review the audit log
runbook history
```

## 🗂️ Documentation

Full documentation is in the [`docs`](docs/) folder:

|                                                |                                                                     |
|------------------------------------------------|---------------------------------------------------------------------|
| [Getting started](docs/getting-started.md)     | Install, scaffold, and run your first runbook                       |
| [File format](docs/FORMAT.md)                  | Frontmatter, block types, and syntax reference                      |
| [CLI reference](docs/cli-reference.md)         | All commands, flags, and exit codes                                 |
| [Shell integration](docs/shell-integration.md) | Tab completion, `rb` alias, `runbook-detect`, and prompt indicator  |
| [Project detection](docs/detect.md)            | How project types, environments, and tool availability are detected |
| [Template variables](docs/variables.md)        | `{{variable}}` resolution and built-ins                             |
| [Safety features](docs/safety.md)              | Rollback, timeouts, confirmation gates, signal handling             |
| [Security](docs/security.md)                   | Static analysis, secret redaction, secure temp files                |
| [Audit logging](docs/audit.md)                 | Execution history and `runbook history`                             |
| [Configuration](docs/configuration.md)         | `~/.runbook/config.yaml`                                            |
| [Built-in templates](docs/templates.md)        | 10 production-ready starting points                                 |

## 🧰 More Examples

- [`examples/getting-started`](examples/getting-started/) - self-contained demo with checks, waits, rollbacks, and confirmation gates
- [`examples/docker-compose-deploy`](examples/docker-compose-deploy/) - realistic Docker Compose deployment flow for a VPS or bare-metal host

## 🤝 Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Bug reports, feature discussions, and documentation
PRs are all welcome.

## 📄 License

Apache 2.0 — see [LICENSE](LICENSE) for details.
