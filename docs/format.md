# File Format Reference

A `.runbook` file is UTF-8 encoded text with three parts:

1. **Frontmatter** — YAML metadata between `---` delimiters
2. **Body** — Markdown prose that documents the procedure
3. **Executable blocks** — typed fenced code blocks (`check`, `step`, `rollback`, `wait`)

---

## Frontmatter

Frontmatter is YAML between the opening and closing `---` delimiters at the top of the file.
It must be valid YAML, must not exceed 64 KB, and must not contain fields other than those listed below.

```yaml
---
name: Deploy API to Production       # required — human-readable name
version: 2.4.0                       # optional — semver or any string
description: |                       # optional — multi-line description
  Deploys the API service with
  zero-downtime migration.
owners: [platform-team, alice]       # optional — list of owners / teams
environments: [staging, production]  # optional — allowed environments
requires:
  tools: [kubectl, jq, curl]         # optional — tools that must be on PATH
  permissions: [deploy-prod]         # optional — required permission labels
  approvals:                         # optional — required approvals per env
    production: [security-team]
timeout: 30m                         # optional — global execution timeout
trigger: on-call                     # optional — informational trigger label
severity: high                       # optional — informational severity label
---
```

### Frontmatter fields

| Field                  | Type              | Required | Description                                             |
|------------------------|-------------------|----------|---------------------------------------------------------|
| `name`                 | string            | yes      | Human-readable name shown in output and audit log       |
| `version`              | string            | no       | Version string displayed in dry-run and audit           |
| `description`          | string            | no       | Multi-line description of the runbook's purpose         |
| `owners`               | list of strings   | no       | Owners or team names (informational)                    |
| `environments`         | list of strings   | no       | If set, restricts execution to named environments       |
| `requires.tools`       | list of strings   | no       | Tool names checked on `PATH` before execution           |
| `requires.permissions` | list of strings   | no       | Permission labels (informational)                       |
| `requires.approvals`   | map of env → list | no       | Required approvals per environment (informational)      |
| `timeout`              | duration          | no       | Maximum total execution time (e.g. `30m`, `2h`, `300s`) |
| `trigger`              | string            | no       | Informational trigger label                             |
| `severity`             | string            | no       | Informational severity label                            |

---

## Block types

### check

A `check` block is a precondition. Every check runs before any step.
If any check exits non-zero, execution stops and nothing else runs.

````
```check name="<name>"
<shell command>
```
````

| Attribute | Required | Description                      |
|-----------|----------|----------------------------------|
| `name`    | yes      | Unique identifier for this check |

**Example:**

````markdown
```check name="cluster-healthy"
kubectl get nodes | grep -c Ready | test $(cat) -ge 3
echo "Cluster has enough ready nodes"
```
````

---

### step

A `step` block is an executable unit of work. Steps run in document order unless `depends_on` reorders them.

````
```step name="<name>" [rollback="<rollback-name>"] [depends_on="<step-name>"]
  [timeout: <duration>]
  [confirm: <environment>]
  [env: [<env1>, <env2>]]
  [kill_grace: <duration>]
---
<shell command>
```
````

| Attribute    | Required | Description                                                                             |
|--------------|----------|-----------------------------------------------------------------------------------------|
| `name`       | yes      | Unique identifier for this step                                                         |
| `rollback`   | no       | Name of the rollback block to execute if this step or a later one fails                 |
| `depends_on` | no       | Name of a preceding step that must succeed before this one runs                         |
| `timeout`    | no       | Maximum time for this step (e.g. `300s`, `5m`). Sends SIGTERM, waits 10 s, then SIGKILL |
| `confirm`    | no       | Environment name that triggers an interactive `[y/n/s/a]` prompt                        |
| `env`        | no       | Environments in which this step runs; silently skipped in all others                    |
| `kill_grace` | no       | Grace period between SIGTERM and SIGKILL for this step (default: `10s`)                 |

The step body begins after the `---` separator. If there are no attributes, `---` can be omitted.

**Example:**

````markdown
```step name="deploy-app" depends_on="migrate-db" rollback="rollback-app"
  timeout: 300s
  confirm: production
  env: [staging, production]
  kill_grace: 30s
---
kubectl set image deployment/api api={{image}}:{{version}}
kubectl rollout status deployment/api --timeout=5m
```
````

---

### rollback

A `rollback` block is a recovery handler referenced by a `step` via `rollback="<name>"`.
Rollback blocks execute in LIFO order when a step fails. Rollback is best-effort:
if a rollback block itself exits non-zero, the failure is logged and remaining rollbacks continue.

````
```rollback name="<name>"
<shell command>
```
````

| Attribute | Required | Description                                                    |
|-----------|----------|----------------------------------------------------------------|
| `name`    | yes      | Unique identifier, referenced by a step's `rollback` attribute |

**Example:**

````markdown
```rollback name="rollback-app"
kubectl set image deployment/api api={{image}}:{{previous_version}}
kubectl rollout status deployment/api --timeout=5m
```
````

---

### wait

A `wait` block pauses execution for a fixed duration, optionally running a monitoring command.
If the monitoring command exits non-zero, the wait aborts and triggers rollback.

````
```wait name="<name>" duration="<duration>"
  [abort_if: <condition string>]
---
<monitoring command (optional)>
```
````

| Attribute  | Required | Description                                                        |
|------------|----------|--------------------------------------------------------------------|
| `name`     | yes      | Unique identifier for this wait block                              |
| `duration` | yes      | Total wait time (e.g. `60s`, `5m`)                                 |
| `abort_if` | no       | Human-readable abort condition description shown in dry-run output |

**Example:**

````markdown
```wait name="canary-soak" duration="300s"
  abort_if: error_rate > 1%
---
./scripts/monitor-canary.sh --threshold=1%
```
````

---

## Body (Markdown prose)

Any text between blocks is documentation. Use standard Markdown — headings, paragraphs,
lists, links, images. Standard fenced code blocks (without a recognised type tag) are
rendered as documentation and never executed.

---

## Template variables

Use `{{variable_name}}` in block bodies and in the frontmatter `description` field.
See [variables reference](variables.md) for the full resolution order and built-in variables.

---

## Parser limits

| Limit                      | Value      |
|----------------------------|------------|
| Maximum file size          | 1 MB       |
| Maximum blocks             | 1,000      |
| Maximum frontmatter size   | 64 KB      |
| Encoding                   | UTF-8 only |
| Unknown frontmatter fields | rejected   |

---

## Quick reference

| Block      | Purpose                                          | Required attributes |
|------------|--------------------------------------------------|---------------------|
| `check`    | Precondition that must pass before any step runs | `name`              |
| `step`     | Executable unit of work                          | `name`              |
| `rollback` | Recovery handler executed on step failure (LIFO) | `name`              |
| `wait`     | Timed pause with optional monitoring and abort   | `name`, `duration`  |
