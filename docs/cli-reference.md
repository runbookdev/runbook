# CLI Reference

## runbook run

Execute a runbook file.

```
runbook run <file> [flags]
```

| Flag                 | Description                                                                 |
|----------------------|-----------------------------------------------------------------------------|
| `--env <name>`       | Target environment (e.g. `staging`, `production`)                           |
| `--var key=value`    | Set a template variable. Repeatable: `--var a=1 --var b=2`                  |
| `--env-file <path>`  | Load variables from a `.env` file                                           |
| `--non-interactive`  | Skip all confirmation prompts (auto-yes)                                    |
| `--dry-run`          | Show the execution plan without running anything                            |
| `--verbose`          | Show debug-level details: commands, timing, variable values                 |
| `--audit-dir <path>` | Custom path for the audit database (default: `~/.runbook/audit/runbook.db`) |
| `--no-color`         | Disable colored terminal output                                             |
| `--max-parallel <n>` | Maximum steps the DAG scheduler runs concurrently (default `0`/`1` = sequential; frontmatter `max_parallel` takes precedence when set) |

**Examples:**

```bash
# Basic run
runbook run deploy.runbook --env staging

# Pass variables
runbook run deploy.runbook --env production \
  --var service=api \
  --var version=2.4.0

# Load variables from a .env file
runbook run deploy.runbook --env staging --env-file .env.staging

# Non-interactive (CI/CD)
runbook run deploy.runbook --env production --non-interactive

# Preview only
runbook run deploy.runbook --env staging --dry-run

# Run independent branches in parallel (up to 4 at once)
runbook run deploy.runbook --env staging --max-parallel 4
```

---

## runbook dry-run

Preview the execution plan without running anything. Alias for `runbook run --dry-run`.

```
runbook dry-run <file> [flags]
```

Accepts the same flags as `runbook run`.

**Example:**

```bash
runbook dry-run deploy.runbook --env production \
  --var service=api --var version=2.4.0
```

---

## runbook validate

Parse and statically analyse a runbook file. Exits `0` if valid, `3` if there are errors.

```
runbook validate <file>
```

Runs all 12 validation rules, including security checks for credential patterns,
pipe-to-shell patterns, and plain-HTTP fetches. Suitable for CI.

**Example:**

```bash
runbook validate deploy.runbook && echo "OK"
```

---

## runbook init

Create a new `.runbook` file from scratch or from a built-in template.

```
runbook init [file] [flags]
```

| Flag                | Description                                    |
|---------------------|------------------------------------------------|
| `--template <name>` | Use a built-in template (see `list-templates`) |

**Examples:**

```bash
# Minimal skeleton
runbook init my-runbook.runbook

# From a template
runbook init --template=deploy my-deploy.runbook
runbook init --template=incident-response oncall.runbook
```

---

## runbook list-templates

Show all available built-in templates.

```
runbook list-templates
```

See [Built-in templates](templates.md) for descriptions of each template.

---

## runbook history

Query the local audit log.

```
runbook history [flags]
```

| Flag            | Description                                             |
|-----------------|---------------------------------------------------------|
| `--run-id <id>` | Show details for a specific run (prefix match accepted) |
| `--limit <n>`   | Number of recent runs to show (default: 20)             |

**Examples:**

```bash
# Show 20 most recent runs
runbook history

# Show 50 runs
runbook history --limit 50

# Show details for a specific run
runbook history --run-id a1b2c3
```

See [Audit logging](audit.md) for details on what is recorded.

---

## runbook doctor

Check the health of your runbook installation and, optionally, a specific runbook file.

```
runbook doctor [file] [flags]
```

| Flag                 | Description                         |
|----------------------|-------------------------------------|
| `--audit-dir <path>` | Path to the audit database to check |

Checks performed:

- Required tools on `PATH` (from frontmatter `requires.tools`)
- `.env` file permissions (warns if not `0600`)
- Audit database accessibility

**Example:**

```bash
runbook doctor deploy.runbook
```

---

## runbook env

Inspect the project environment without requiring shell hooks. Detects the project type, lists
`.runbook` files, reports required tools and their `PATH` availability, and lists all environments
declared across `.runbook` frontmatter.

```
runbook env [dir] [flags]
```

| Flag             | Description                                                              |
|------------------|--------------------------------------------------------------------------|
| `--json`         | Output machine-readable JSON                                             |
| `--check-tools`  | Exit `0` if all required tools are present, `1` if any are missing       |

**Examples:**

```bash
# Human-readable summary of the current directory
runbook env

# Inspect a specific directory
runbook env ./services/api

# Machine-readable JSON
runbook env --json

# CI pre-flight: fail if any required tool is missing
runbook env --check-tools
```

**JSON output shape:**

```json
{
  "project_type": "Go project",
  "runbooks": [
    {
      "file": "deploy.runbook",
      "name": "Deploy service",
      "environments": ["staging", "production"]
    }
  ],
  "tools": {
    "required": ["go", "golangci-lint"],
    "found":    ["go"],
    "missing":  ["golangci-lint"]
  },
  "environments": ["staging", "production"]
}
```

See [Shell integration](shell-integration.md) for the interactive `runbook-detect` shell function.

---

## runbook shell-init

Output a shell integration snippet that installs tab completion, the `rb` alias, and the
`runbook-detect` helper function. Source it in your shell profile.

```
runbook shell-init [flags]
```

| Flag            | Description                                                    |
|-----------------|----------------------------------------------------------------|
| `--shell <name>`| Target shell: `bash`, `zsh`, or `fish` (default: auto-detect) |

**Setup:**

```bash
# Bash (~/.bashrc)
eval "$(runbook shell-init)"

# Zsh (~/.zshrc)
eval "$(runbook shell-init)"

# Fish (~/.config/fish/config.fish)
runbook shell-init --shell fish | source
```

The snippet provides:

- **Tab completion** for all commands, flags, and `.runbook` files (inlined; no subprocess at
  shell startup)
- **`rb` alias** — shorthand for `runbook`
- **`runbook-detect`** — scans the current directory and prints a project summary
- **`runbook-prompt-indicator`** — optional prompt prefix showing the number of `.runbook` files

See [Shell integration](shell-integration.md) for full details and prompt customisation.

---

## runbook completion

Generate a raw shell completion script. Prefer `runbook shell-init` for interactive use; this
command is intended for package maintainers or custom integrations.

```
runbook completion [bash|zsh|fish]
```

This command is hidden from the default help output.

**Examples:**

```bash
# Load once in the current session
source <(runbook completion bash)
source <(runbook completion zsh)

# Persist for fish
runbook completion fish > ~/.config/fish/completions/runbook.fish
```

---

## runbook version

Print version, commit hash, build date, Go version, and platform.

```
runbook version
```

---

## Exit codes

| Code | Meaning                      |
|------|------------------------------|
| `0`  | Success                      |
| `1`  | Step failed                  |
| `2`  | Step failed and rollback ran |
| `3`  | Validation error             |
| `4`  | Check failed                 |
| `10` | Aborted by user              |
| `20` | Internal error               |
