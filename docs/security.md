# Security

`runbook` includes multiple layers of defence to prevent accidental credential
exposure, dangerous command patterns, and unsafe file handling.

---

## Static analysis (validator)

`runbook validate <file>` runs 12 rules against your runbook before execution.
Four of those rules are security-specific:

| Rule                       | What it detects                                                                             |
|----------------------------|---------------------------------------------------------------------------------------------|
| Credential patterns        | Variable names or literal strings resembling passwords, tokens, or API keys in block bodies |
| Plain-HTTP fetches         | `wget http://...` without TLS                                                               |
| Pipe-to-shell              | `curl ... \| sh`, `wget ... \| bash`, and similar patterns                                  |
| `.env` not in `.gitignore` | When `--env-file` points to a file that is not listed in `.gitignore`                       |

These checks run automatically on every `runbook run` and `runbook dry-run`.
They also run standalone via `runbook validate`:

```bash
runbook validate deploy.runbook
# exit 0 = valid
# exit 3 = validation errors (printed to stderr)
```

---

## Secret redaction in audit logs

Variables whose names contain any of the following substrings are automatically
redacted to `[REDACTED]` before being written to the audit database:

- `SECRET`
- `PASSWORD`
- `TOKEN`
- `KEY`
- `CREDENTIAL`

Matching is case-insensitive. The redaction happens before any data reaches
disk — the plaintext value is never stored.

```bash
# This variable value is never written to the audit log
runbook run deploy.runbook --var API_TOKEN=abc123
```

---

## Secure temporary files

When a block body is written to a temporary script file for execution:

- The file is created with **`0600` permissions** (owner read/write only)
- The file is **deleted immediately after the step completes**, even if the step times out or is killed

---

## `.env` file permission check

`runbook doctor` warns when a `.env` file passed via `--env-file` is not
restricted to owner-only access (`0600`).

```bash
runbook doctor deploy.runbook --env-file .env.production
# warns if .env.production is world-readable
```

---

## Parser hardening

The parser enforces strict input limits before any execution occurs:

| Limit                      | Value                                 |
|----------------------------|---------------------------------------|
| Maximum file size          | 1 MB                                  |
| Maximum blocks             | 1,000                                 |
| Maximum frontmatter size   | 64 KB                                 |
| Encoding                   | UTF-8 only (non-UTF-8 bytes rejected) |
| Unknown frontmatter fields | Rejected with an error                |

---

## Process isolation

Each step runs in its own process group. When a step is killed (timeout or
signal), `runbook` sends `SIGTERM` and then `SIGKILL` to the **entire process
group**, ensuring child processes started by the step script are also cleaned up.

On Linux, orphan process detection provides an additional layer of cleanup.

---

## Root user warning

An unsuppressible warning is printed when `runbook` is invoked as `root` (UID 0):

```
WARNING: running as root. Proceed with care.
```
