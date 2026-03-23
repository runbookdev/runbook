# Example: Getting Started

A self-contained runbook you can run immediately — no Docker, no Kubernetes,
no cloud credentials, no variables to supply. It simulates a full deployment
workflow using only standard Unix shell tools and a temporary directory that
is created and cleaned up automatically.

## Run it now

**If you installed via Homebrew or `go install`:**

```bash
# Staging — no confirmation prompts
runbook run demo.runbook --env staging

# Production — prompts for approval before the deploy step
runbook run demo.runbook --env production
```

**If you are working from the source repo:**

```bash
# Build the binary once (output: bin/runbook)
make build

# Then run using the local binary
./bin/runbook run examples/getting-started/demo.runbook --env staging
./bin/runbook run examples/getting-started/demo.runbook --env production
```

Or without building, using `go run` directly from the repo root:

```bash
go run ./cmd/runbook run examples/getting-started/demo.runbook --env staging
```

That is all. The runbook creates a workspace under `/tmp/runbook-demo-<run-id>/`,
runs every step against it, and removes it when it finishes (or when rollback
completes if something fails).

## What you will see

```
[runbook] running 2 checks
[runbook] check [1/2] "tmp-writable"   ✓
[runbook] check [2/2] "env-recognised" ✓
[runbook] running 7 steps
[runbook] step [1/7] "setup-workspace"   — creates the simulated service at v0.9.0
[runbook] step [2/7] "backup-state"      — snapshots version + DB state to backup/
[runbook] step [3/7] "apply-migration"   — adds the payments table to the schema
[runbook] step [4/7] "deploy-version"    — upgrades the service to v1.0.0
[runbook] step [5/7] "smoke-test"        — 4 assertions against the workspace
[runbook] step [6/7] "extended-tests"    — staging-only integration checks
[runbook] step [7/7] "finalize"          — prints a summary and cleans up
```

## What is being demonstrated

| Feature                      | Where                                                                                           |
|------------------------------|-------------------------------------------------------------------------------------------------|
| `check` blocks               | `tmp-writable`, `env-recognised`                                                                |
| `step` blocks                | All 6 steps                                                                                     |
| `rollback` blocks            | `teardown-workspace`, `undo-migration`, `rollback-version`, `rollback-finalize`                 |
| `wait` block with `abort_if` | `health-soak` (15 s, 5 polls)                                                                   |
| `depends_on` chaining        | Steps 2–7 form a linear dependency chain                                                        |
| `confirm: production` gate   | `deploy-version`                                                                                |
| `kill_grace` attribute       | `deploy-version` (5 s graceful drain)                                                           |
| `env` filter                 | `extended-tests` runs in staging only                                                           |
| Built-in variables           | `{{runbook_name}}`, `{{runbook_version}}`, `{{run_id}}`, `{{env}}`, `{{user}}`, `{{timestamp}}` |

## Simulated workflow

The workspace at `/tmp/runbook-demo-<run-id>/` represents a live service:

```
/tmp/runbook-demo-<run-id>/
├── current-version     "0.9.0" → upgraded to "1.0.0" by deploy-version
├── db-state            schema without payments → schema with payments
├── service-status      "running"
├── migration.log       written by apply-migration
└── backup/
    ├── current-version.bak
    └── db-state.bak
```

## Rollback in action

To see the LIFO rollback stack fire, edit `deploy-version` to exit with an
error. The executor will run rollbacks in reverse order:

```
rollback-version    → re-pin current-version to 0.9.0
undo-migration      → restore db-state from backup, remove migration.log
teardown-workspace  → rm -rf the workspace directory
```

## Dry run

To inspect the execution plan without running anything:

```bash
runbook dry-run demo.runbook --env staging
```

## Health check

To verify the CLI and the runbook file are in good shape before executing:

```bash
runbook doctor demo.runbook
```
