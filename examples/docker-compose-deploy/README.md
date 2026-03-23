# Example: Docker Compose Deploy

A complete `.runbook` example that deploys a Docker Compose application on a
VPS or bare-metal server. It demonstrates every block type and most available
attributes.

## What it does

| Step                 | Description                                                                         | Rollback                                               |
|----------------------|-------------------------------------------------------------------------------------|--------------------------------------------------------|
| **Checks**           | Docker daemon running, compose file present, ≥ 5 GB disk, service healthy           | —                                                      |
| `pull-image`         | Pull the new image before stopping anything                                         | —                                                      |
| `backup-database`    | Dump and gzip the Postgres database                                                 | `rollback-backup` — restore from the dump              |
| `run-migrations`     | Run `migrate up` inside a throw-away container                                      | `rollback-migrations` — restore the pre-migration dump |
| `restart-containers` | `docker compose up -d --force-recreate`                                             | Re-create from the previous image                      |
| `health-soak`        | 60 s poll loop — fails if the service returns anything other than `ok`              | —                                                      |
| `smoke-test`         | Assert running version matches `{{version}}`; probe `/health`, `/ready`, `/metrics` | —                                                      |
| `prune-images`       | `docker image prune -f` to reclaim disk                                             | —                                                      |

## Features demonstrated

- **All block types** — `check`, `step`, `rollback`, `wait`
- **Step chaining** — `depends_on` creates an explicit execution order
- **Rollback stack** — `backup-database` and `run-migrations` both point to
  `restore-database`; `restart-containers` has its own `rollback-containers`
- **Confirmation gate** — `confirm: production` on the backup and restart steps
- **Per-step timeouts** — each step carries an appropriate upper bound
- **Environment filter** — the prune step only runs in `staging` (non-critical cleanup, skipped in production)
- **Wait block** — 60-second health soak with an `abort_if` guard
- **Template variables** — all deployment parameters are injected at runtime

## Variables

| Variable           | Description                           | Example                       |
|--------------------|---------------------------------------|-------------------------------|
| `{{service}}`      | Docker Compose service name           | `api`                         |
| `{{version}}`      | Image tag to deploy                   | `1.4.2`                       |
| `{{env}}`          | Target environment                    | `production`                  |
| `{{compose_file}}` | Absolute path to `docker-compose.yml` | `/opt/api/docker-compose.yml` |
| `{{app_url}}`      | Base URL for health/smoke checks      | `https://api.example.com`     |
| `{{db_container}}` | Running Postgres container name       | `api-db-1`                    |

## Usage

### Dry run (nothing executes)

```bash
runbook dry-run deploy.runbook \
  --env staging \
  --var service=api \
  --var version=1.4.2 \
  --var compose_file=/opt/api/docker-compose.yml \
  --var app_url=https://api.staging.example.com \
  --var db_container=api-db-1
```

### Staging deploy

```bash
runbook run deploy.runbook \
  --env staging \
  --var service=api \
  --var version=1.4.2 \
  --var compose_file=/opt/api/docker-compose.yml \
  --var app_url=https://api.staging.example.com \
  --var db_container=api-db-1
```

### Production deploy

```bash
runbook run deploy.runbook \
  --env production \
  --var service=api \
  --var version=1.4.2 \
  --var compose_file=/opt/api/docker-compose.yml \
  --var app_url=https://api.example.com \
  --var db_container=api-db-1
```

`confirm: production` steps will prompt for confirmation before the
database backup and the container restart.

### Check the audit log afterwards

```bash
runbook history
runbook history --run-id=<id shown above>
```

### Pre-flight check (no execution)

```bash
runbook doctor deploy.runbook
```

`doctor` verifies that `docker`, `curl`, and `jq` are all on `PATH` and
that the `.env` file (if present) has safe permissions.

## Prerequisites

- `runbook` CLI installed (`brew install runbookdev/tap/runbook`)
- `docker` and `docker compose` available on the machine where the runbook runs
- `curl` and `jq` installed
- The target compose file exists at the path supplied via `--var compose_file`
- The Postgres container named by `--var db_container` is running

## Adapting the example

| Change                  | What to modify                                                                       |
|-------------------------|--------------------------------------------------------------------------------------|
| Different database      | Replace the `pg_dumpall` / `psql` commands in `backup-database` / `restore-database` |
| Non-Postgres migrations | Replace the `migrate` invocation in `run-migrations`                                 |
| Multiple services       | Duplicate the `restart-containers` / `rollback-containers` pair                      |
| Slack notification      | Add a `step name="notify-slack"` at the end with a `curl` webhook call               |
| SSH to remote host      | Wrap commands in `ssh user@host '...'`                                               |
