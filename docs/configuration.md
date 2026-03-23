# Configuration

`runbook` reads a configuration file at `~/.runbook/config.yaml`.
CLI flags always override config file values.

---

## config.yaml reference

```yaml
# Default target environment.
# Equivalent to always passing --env <value>.
env: staging

# Default .env file to load variables from.
# Equivalent to always passing --env-file <path>.
env_file: .env.local

# Path to the SQLite audit database.
# Default: ~/.runbook/audit/runbook.db
audit_dir: ~/.runbook/audit/runbook.db

# Skip all confirmation prompts (auto-yes).
# Equivalent to always passing --non-interactive.
# Set to true for CI/CD environments.
non_interactive: false

# Disable colored terminal output.
# Equivalent to always passing --no-color.
no_color: false

# Shell used to execute block bodies.
# Default: /bin/bash
shell: /bin/bash
```

---

## Precedence

Settings are applied in this order (highest wins):

1. CLI flags (`--env`, `--var`, `--non-interactive`, etc.)
2. `~/.runbook/config.yaml`
3. Built-in defaults

---

## Common setups

### CI / CD

```yaml
# ~/.runbook/config.yaml
non_interactive: true
no_color: true
audit_dir: /var/log/runbook/audit.db
```

### Local development with a staging default

```yaml
# ~/.runbook/config.yaml
env: staging
env_file: .env.local
```

### Custom shell

```yaml
# ~/.runbook/config.yaml
shell: /bin/zsh
```
