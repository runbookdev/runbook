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

Operations runbooks are broken in every organisation. The pain manifests in three ways:

**Stale documentation** — Wiki-based runbooks (Confluence, Notion, Google Docs) decay within weeks.
During a 3am incident, the on-call engineer discovers half the steps reference services that no longer exist.

**Unreadable automation** — Script-based runbooks (bash, Makefiles) are executable but lack context,
skip explanation, have no rollback logic, and are dangerous to run without understanding every line.

**Vendor lock-in** — Playbook tools (PagerDuty Runbooks, Rundeck) are proprietary, expensive, and cannot
be version-controlled, reviewed in PRs, or composed with other tools.

The consequences are measurable and significant:

- Increased MTTR during incidents because procedures are outdated or missing
- Onboarding friction — new engineers cannot self-serve operational knowledge and depend on tribal knowledge
- Repeated incidents from the same root cause because post-incident improvements are never codified
- Compliance risk — audit trails of operational actions are incomplete or nonexistent
- Engineering time wasted maintaining duplicate sources of truth (wiki + scripts)

### Why now

Several converging trends make this the right moment:

- **GitOps maturity** — teams already version-control infrastructure (Terraform, Kubernetes manifests). Runbooks are the last major operational artifact not yet managed as code.
- **Incident management evolution** — tools like PagerDuty, Incident.io, and FireHydrant have professionalised incident response, but the actual procedures remain in wikis.
- **Compliance tightening** — SOC 2, ISO 27001, and DORA regulations increasingly require auditable operational procedures. Manual wiki-based processes fail audits.
- **AI readiness** — structured, executable runbooks are the ideal substrate for AI-assisted operations.

## 💡 The solution

> **Documentation and automation are the same file.** When you update the command, you update the
> explanation in the same commit. They can never drift apart.

A **`.runbook`** file is extended Markdown with typed, fenced code blocks. The frontmatter defines
metadata, permissions, and approval rules. The Markdown body is the documentation. Specially-typed
code blocks (`check`, `step`, `rollback`, `wait`) are the executable units.

This means a single file is the document a human reads during an incident **and** the executable
procedure the system runs. It lives in your Git repo, is reviewed in PRs, and is tested in CI.

- 📄 A **human-readable document** that explains what, why, and how
- ⚡ An **executable program** with typed steps, preconditions, rollback logic, and environment awareness
- 🔀 A **version-controlled artifact** that lives alongside your code, reviewed in PRs

## 📦 Installation

### Homebrew (macOS / Linux)

```bash
brew install runbookdev/tap/runbook
```

### Download binary

Pre-built binaries are published on every [GitHub release](https://github.com/runbookdev/runbook/releases)
for **Linux** (amd64, arm64), **macOS** (amd64, arm64), and **Windows** (amd64).

**macOS / Linux**

```bash
# Detect platform and download the latest release
VERSION=$(curl -s https://api.github.com/repos/runbookdev/runbook/releases/latest | grep tag_name | cut -d '"' -f4)
OS=$(uname -s | tr A-Z a-z)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

curl -sL "https://github.com/runbookdev/runbook/releases/download/${VERSION}/runbook_${VERSION#v}_${OS}_${ARCH}.tar.gz" | tar xz
sudo mv runbook /usr/local/bin/
```

**Windows (PowerShell)**

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

## 🚀 Quick start

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

## 📖 Documentation

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

## 🤝 Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Bug reports, feature discussions, and documentation
PRs are all welcome.

## 📄 License

Apache 2.0 — see [LICENSE](LICENSE) for details.
