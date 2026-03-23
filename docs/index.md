# runbook documentation

`runbook` unifies operational documentation with executable automation in a single file.

A `.runbook` file is extended Markdown — the prose is the documentation a human reads,
and the typed code blocks (`check`, `step`, `rollback`, `wait`) are the executable units.
One file, one commit, one review. Documentation and automation can never drift apart.

---

## Guides

|                                       |                                                                            |
|---------------------------------------|----------------------------------------------------------------------------|
| [Getting started](getting-started.md) | Install, scaffold, validate, and run your first runbook                    |
| [File format](FORMAT.md)              | Full syntax reference for frontmatter, block types, and template variables |
| [CLI reference](cli-reference.md)     | All commands, flags, and exit codes                                        |
| [Template variables](variables.md)    | `{{variable}}` resolution order and built-in variables                     |
| [Configuration](configuration.md)     | `~/.runbook/config.yaml` options                                           |

## Safety and operations

|                              |                                                                              |
|------------------------------|------------------------------------------------------------------------------|
| [Safety features](safety.md) | Precondition checks, rollback, timeouts, confirmation gates, signal handling |
| [Security](security.md)      | Static analysis, secret redaction, secure temp files, parser limits          |
| [Audit logging](audit.md)    | Execution history, the SQLite database, and querying with `runbook history`  |

## Reference

|                                    |                                     |
|------------------------------------|-------------------------------------|
| [Built-in templates](templates.md) | 10 production-ready starting points |

---

## Quick start

```bash
# Install
brew install runbookdev/tap/runbook

# Scaffold from a template
runbook init --template=deploy my-deploy.runbook

# Validate
runbook validate my-deploy.runbook

# Preview
runbook dry-run my-deploy.runbook --env staging

# Run
runbook run my-deploy.runbook --env staging --var service=api --var version=2.4.0

# Review history
runbook history
```
