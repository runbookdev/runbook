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
