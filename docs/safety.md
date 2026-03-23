# Safety Features

`runbook` is built around the idea that operational automation should be safe
by default. The following mechanisms protect against accidents, partial failures,
and runaway processes.

---

## Precondition checks

`check` blocks run before any step executes. If any check exits non-zero,
the entire runbook aborts — no steps run.

```markdown
```check name="cluster-healthy"
kubectl get nodes | grep -c Ready | test $(cat) -ge 3
```

```check name="no-active-incident"
./scripts/check-incident-status.sh
```
```

Use checks to verify prerequisites: required tools on `PATH`, service health,
sufficient disk space, no ongoing incidents.

---

## Confirmation gates

Add `confirm: <environment>` to a step to require interactive approval before it runs.

```markdown
```step name="restart-containers" rollback="rollback-containers"
  timeout: 120s
  confirm: production
---
./scripts/restart.sh "{{service}}"
```
```

When the step is reached in the named environment, execution pauses and prompts:

```
[runbook] step "restart-containers" requires confirmation (env: production)
  Proceed? [y]es / [n]o / [s]kip / [a]bort:
```

| Choice | Effect                                    |
|--------|-------------------------------------------|
| `y`    | Run the step                              |
| `n`    | Skip this step and continue with the next |
| `s`    | Same as `n`                               |
| `a`    | Abort the entire runbook                  |

Use `--non-interactive` to auto-answer yes (e.g. in CI where confirmation is handled upstream).

---

## Automatic rollback

When a step fails, `runbook` executes rollback blocks in LIFO order — the last
step that registered a rollback runs first, working backwards to undo completed work.

```markdown
```step name="migrate-db" rollback="undo-migrate"
  timeout: 300s
---
./scripts/migrate.sh --version={{version}}
```

```step name="deploy-app" rollback="undo-deploy"
  timeout: 120s
---
./scripts/deploy.sh {{version}}
```
```

If `deploy-app` fails, `undo-deploy` runs first, then `undo-migrate`.

Rollback is **best-effort**: if a rollback block itself fails, the failure is
logged and remaining rollbacks continue.

**Rollback output:**

```
[runbook] step "deploy-app" failed (exit code 1)
[rollback] starting rollback (2 blocks, trigger: step_failure)
[rollback] executing "undo-deploy" (1 of 2)
[rollback] "undo-deploy" succeeded (3s)
[rollback] executing "undo-migrate" (2 of 2)
[rollback] "undo-migrate" succeeded (5s)
[rollback] complete: 2 succeeded, 0 failed
```

---

## Dry-run mode

Preview the full execution plan without running anything.

```bash
runbook dry-run deploy.runbook --env production \
  --var service=api --var version=2.4.0
```

Dry-run shows:

- All frontmatter metadata
- Resolved variable values
- Each check with its resolved command
- Each step with its resolved command, timeout, rollback reference, and confirm status
- Each wait block with its duration and abort condition

---

## Per-step timeouts

Add `timeout` to a step to cap how long it can run.

```markdown
```step name="run-migrations"
  timeout: 300s
---
./scripts/migrate.sh
```
```

When the timeout is exceeded:

1. `SIGTERM` is sent to the step process and its entire process group
2. After `kill_grace` (default `10s`), `SIGKILL` is sent
3. The step is marked failed and rollback begins

Override the grace period per step with `kill_grace`:

```markdown
```step name="drain-connections"
  timeout: 60s
  kill_grace: 30s
---
./scripts/drain.sh
```
```

---

## Global timeout

Set `timeout` in frontmatter to cap total execution time across all steps.

```yaml
---
name: Deploy API
timeout: 30m
---
```

If the global timeout is exceeded, the current step is killed, and rollback begins for all completed steps.

---

## Environment filtering

Tag steps with `env` to restrict them to specific environments. Steps not matching the current `--env` are silently skipped.

```markdown
```step name="extended-tests"
  timeout: 15s
  env: [staging]
---
./scripts/extended-tests.sh
```
```

Use this to run extra validation in staging that you want to skip in production,
or to run production-only notification steps.

---

## Signal handling (Ctrl+C)

Press `Ctrl+C` during execution to interrupt. `runbook` pauses and prompts:

```
[runbook] interrupt received. Choose action:
  [r]ollback — stop and roll back completed steps (default)
  [c]ontinue — resume execution
  [q]uit     — stop immediately, no rollback
  Action [r/c/q] (timeout 10s):
```

If no input is received within 10 seconds, `runbook` defaults to **rollback**.

---

## Step dependency ordering

Use `depends_on` to declare explicit execution order between steps.

```markdown
```step name="run-migrations" depends_on="backup-database"
  timeout: 180s
---
./scripts/migrate.sh
```
```

`run-migrations` will not start until `backup-database` has completed successfully.
If the dependency step failed or was skipped, `run-migrations` is skipped too.

---

## Root user warning

When `runbook` detects it is running as root (`UID 0`), it emits an
unsuppressible warning before execution begins. Avoid running runbooks as root
unless strictly necessary.
