# Template Variables

Variables use `{{name}}` syntax and are substituted at runtime in:

- `check`, `step`, `rollback`, and `wait` block bodies
- The frontmatter `description` field

---

## Resolution order

When the same variable name appears in multiple sources, the highest-priority source wins:

| Priority    | Source                | How to set                            |
|-------------|-----------------------|---------------------------------------|
| 1 (highest) | CLI flags             | `--var region=us-east-1`              |
| 2           | Environment variables | `RUNBOOK_REGION=us-east-1`            |
| 3           | `.env` file           | `--env-file=.env.staging`             |
| 4 (lowest)  | Built-in variables    | Provided automatically by the runtime |

---

## CLI variables

Pass `--var key=value` on the command line. The flag is repeatable.

```bash
runbook run deploy.runbook --env production \
  --var service=api \
  --var version=2.4.0 \
  --var region=us-east-1
```

In the runbook:

```bash
kubectl set image deployment/{{service}} {{service}}={{image}}:{{version}}
```

---

## Environment variables

Any environment variable prefixed with `RUNBOOK_` is available as a template variable with the prefix stripped and the name lowercased.

```bash
export RUNBOOK_REGION=us-east-1
export RUNBOOK_IMAGE=my-registry/api

runbook run deploy.runbook --env production
```

Inside the runbook, `{{region}}` and `{{image}}` resolve to `us-east-1` and `my-registry/api`.

---

## .env file

Load variables from a `.env` file with `--env-file`:

```bash
runbook run deploy.runbook --env staging --env-file .env.staging
```

`.env` file format (standard `KEY=value` pairs):

```dotenv
SERVICE=api
VERSION=2.4.0
REGION=us-east-1
DB_HOST=staging-db.internal
```

> Keep `.env` files out of version control and ensure file permissions are `0600`.
> `runbook doctor` warns if your `.env` file is world-readable.

---

## Built-in variables

These are always available and require no configuration.

| Variable              | Value                                  |
|-----------------------|----------------------------------------|
| `{{env}}`             | Target environment passed via `--env`  |
| `{{runbook_name}}`    | `name` field from frontmatter          |
| `{{runbook_version}}` | `version` field from frontmatter       |
| `{{run_id}}`          | Unique UUID for this execution         |
| `{{timestamp}}`       | Execution start time in RFC 3339 (UTC) |
| `{{user}}`            | Current OS user (`$USER`)              |
| `{{hostname}}`        | Machine hostname                       |
| `{{cwd}}`             | Current working directory              |

**Usage examples:**

```bash
# Use run_id to isolate temporary files per execution
./scripts/setup.sh "{{run_id}}"

# Log execution metadata
echo "Running {{runbook_name}} v{{runbook_version}} at {{timestamp}} as {{user}}"

# Environment-aware paths
./scripts/migrate.sh --env={{env}}
```

---

## Undefined variables

If a variable is referenced in a block body but has no value from any source, the
executor substitutes an empty string and emits a warning. Use `runbook dry-run` to
preview resolved variable values before executing.
