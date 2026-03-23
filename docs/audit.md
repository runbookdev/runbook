# Audit Logging

Every runbook execution is recorded in a local SQLite database.
The audit log provides a complete history of what ran, when, by whom, and with what outcome.

---

## Location

Default path: `~/.runbook/audit/runbook.db`

Override with `--audit-dir`:

```bash
runbook run deploy.runbook --audit-dir /var/log/runbook/audit.db
```

Or set a default in `~/.runbook/config.yaml`:

```yaml
audit_dir: /var/log/runbook/audit.db
```

---

## What is recorded

For each execution `runbook` records:

**Run record:**

| Field           | Description                                          |
|-----------------|------------------------------------------------------|
| Run ID          | Unique UUID for this execution                       |
| Runbook name    | From frontmatter `name`                              |
| Runbook version | From frontmatter `version`                           |
| Environment     | Value of `--env`                                     |
| User            | OS user who invoked `runbook`                        |
| Hostname        | Machine hostname                                     |
| Start time      | UTC timestamp                                        |
| End time        | UTC timestamp                                        |
| Outcome         | `success`, `failed`, `rolled_back`, `aborted`        |
| Variables       | Key-value pairs passed to the run (secrets redacted) |

**Step record (per step):**

| Field      | Description                                   |
|------------|-----------------------------------------------|
| Step name  | Block `name` attribute                        |
| Start time | UTC timestamp                                 |
| End time   | UTC timestamp                                 |
| Exit code  | Process exit code                             |
| Outcome    | `success`, `failed`, `skipped`, `rolled_back` |
| Output     | Captured stdout/stderr                        |

---

## Secret redaction

Variables whose names contain `SECRET`, `PASSWORD`, `TOKEN`, `KEY`, or `CREDENTIAL`
(case-insensitive) are stored as `[REDACTED]` in the database. The plaintext value
is never written to disk.

---

## Querying the audit log

```bash
# Show the 20 most recent runs
runbook history

# Show more runs
runbook history --limit 50

# Show all steps for a specific run (prefix match on run ID)
runbook history --run-id a1b2c3
```

**Example output:**

```
RUN ID    RUNBOOK              ENV         USER    OUTCOME    STARTED
a1b2c3…   Deploy API           production  alice   success    2026-03-23 14:05:01
9f4e1d…   Deploy API           staging     bob     failed     2026-03-23 13:47:22
```

```
# runbook history --run-id a1b2c3

Run a1b2c3e4-…
  Runbook : Deploy API v2.4.0
  Env     : production
  User    : alice
  Host    : deploy-host.internal
  Started : 2026-03-23 14:05:01 UTC
  Ended   : 2026-03-23 14:05:48 UTC
  Outcome : success

Steps:
  ✓ migrate-db    12s   exit 0
  ✓ deploy-app    35s   exit 0
```

---

## Checking the database

`runbook doctor` verifies that the audit database is accessible and reports its size:

```bash
runbook doctor
runbook doctor --audit-dir /var/log/runbook/audit.db
```
