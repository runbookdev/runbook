# Project Detection

runbook automatically detects the type of project in a directory, discovers `.runbook` files,
aggregates the environments they declare, and checks whether their required tools are on `PATH`.
This information is surfaced through two interfaces:

- **`runbook env`** — a standard CLI command, suitable for CI and scripting
- **`runbook-detect`** — a shell function installed by `runbook shell-init`, designed for
  interactive terminal use

Both share the same detection logic from the `internal/detect` package.

---

## How project-type detection works

Detection examines the top-level contents of the target directory and returns the type identifier
for the **first matching rule** in the following priority order:

| Marker file / directory       | Type identifier   | Display name        |
|-------------------------------|-------------------|---------------------|
| `go.mod`                      | `go`              | Go project          |
| `package.json`                | `nodejs`          | Node.js project     |
| `Cargo.toml`                  | `rust`            | Rust project        |
| `pyproject.toml`              | `python`          | Python project      |
| `requirements.txt`            | `python`          | Python project      |
| `Dockerfile`                  | `docker`          | Docker project      |
| `docker-compose.yml`          | `docker-compose`  | Docker Compose      |
| `docker-compose.yaml`         | `docker-compose`  | Docker Compose      |
| `Makefile`                    | `make`            | Make-based build    |
| Any `*.tf` file at top level  | `terraform`       | Terraform project   |
| `kubernetes/` directory       | `kubernetes`      | Kubernetes project  |
| `k8s/` directory              | `kubernetes`      | Kubernetes project  |
| `.github/workflows/` directory| `github-actions`  | GitHub Actions CI   |
| `Jenkinsfile`                 | `jenkins`         | Jenkins CI          |
| _(none of the above)_         | `unknown`         | unknown             |

Only the first match is used. For example, a repository that has both `go.mod` and a `Makefile`
is classified as `go`, not `make`.

---

## How `.runbook` files are scanned

Detection globs `*.runbook` in the target directory and parses the **YAML frontmatter only** from
each file (the parser stops at the second `---` delimiter). The Markdown body is never read, so
scanning is fast even for large runbook files.

From each file the scanner extracts:

- `name:` — the human-readable runbook title
- `environments:` — the list of environment names the runbook applies to
- `requires.tools:` — the list of tool names the runbook depends on

Results are aggregated across all `.runbook` files:

- **Environments** — deduplicated union, sorted lexicographically
- **Required tools** — deduplicated union, declaration order preserved

Files that cannot be read (permission errors, I/O failures) are silently skipped so that a single
unreadable file does not abort the scan.

---

## Tool availability checking

After collecting the deduplicated list of required tools, detection checks each one with
`exec.LookPath` — the same resolution semantics as the shell's `command -v`. The result is a
[ToolReport](../internal/detect/detect.go) with three fields:

| Field      | Contents                                      |
|------------|-----------------------------------------------|
| `Required` | Full list of tools declared across all runbooks |
| `Found`    | Subset whose binary was found in `PATH`       |
| `Missing`  | Subset whose binary was **not** found in `PATH` |

All three slices are always non-nil, so they encode correctly as JSON arrays (never `null`).

---

## Using detection from the CLI

### Human-readable summary

```bash
runbook env              # current directory
runbook env ./path/to    # specific directory
```

Example output:

```
📂 Project: my-service (Go project)
📋 Runbooks: 2 found (deploy.runbook, rollback.runbook)
🔧 Tools: go ✓, golangci-lint ✓, kubectl ✗
🌍 Environments: production, staging
```

### Machine-readable JSON

```bash
runbook env --json
```

```json
{
  "project_type": "go",
  "runbooks": [
    {
      "file": "deploy.runbook",
      "name": "Deploy service",
      "environments": ["staging", "production"]
    },
    {
      "file": "rollback.runbook",
      "name": "Rollback service",
      "environments": ["production"]
    }
  ],
  "tools": {
    "required": ["go", "golangci-lint", "kubectl"],
    "found":    ["go", "golangci-lint"],
    "missing":  ["kubectl"]
  },
  "environments": ["production", "staging"]
}
```

### CI pre-flight check

`--check-tools` exits `0` if all required tools are present, `1` if any are missing. It is
designed to be dropped into CI pipelines with no additional scripting:

```bash
runbook env --check-tools
```

```yaml
# GitHub Actions
- name: Verify required tools
  run: runbook env --check-tools
```

On failure the missing tools are printed to stderr:

```
missing required tools: kubectl
```

---

## Using detection from the shell (`runbook-detect`)

After running `eval "$(runbook shell-init)"`, the `runbook-detect` shell function is available
in your interactive shell. It produces the same summary as `runbook env` but is optimised for
terminal use and requires no subprocess at startup.

```bash
runbook-detect            # inspect current directory
runbook-detect ./svc/api  # inspect a different directory
```

See [Shell integration](shell-integration.md) for setup instructions.

---

## Detection scope

Detection is always **single-directory**. Only files at the top level of the given directory are
examined — subdirectories are not recursed into. This keeps detection fast and predictable in
monorepos: run `runbook env ./services/api` to inspect a specific service directory.
