# Built-in Templates

`runbook` ships with 10 production-ready templates. Use them as starting points
for your own runbooks.

## List templates

```bash
runbook list-templates
```

## Create from a template

```bash
runbook init --template=<name> <output-file>
```

**Example:**

```bash
runbook init --template=deploy deploy-api.runbook
runbook init --template=incident-response oncall.runbook
```

---

## Template reference

### deploy

Service deployment with canary verification and rollback.

Covers: image pull, pre-deploy health check, rolling restart, canary soak, smoke test, rollback on any step failure.

```bash
runbook init --template=deploy my-deploy.runbook
```

---

### rollback

Manual rollback to a previous known-good version.

Covers: pre-rollback health check, image re-pin, container restart, verification.

```bash
runbook init --template=rollback my-rollback.runbook
```

---

### failover

Database failover — promote a replica to primary.

Covers: replica lag check, promotion, DNS/connection string update, application reconnect, verification.

```bash
runbook init --template=failover db-failover.runbook
```

---

### cert-rotation

TLS certificate renewal and deployment.

Covers: expiry check, certificate generation, staging verification, production deployment, rollback on failure.

```bash
runbook init --template=cert-rotation rotate-certs.runbook
```

---

### db-migration

Database schema migration with backup and rollback.

Covers: pre-migration backup, migration application, schema verification, rollback to backup on failure.

```bash
runbook init --template=db-migration migrate-schema.runbook
```

---

### incident-response

P1 incident triage, mitigation, and recovery.

Covers: alert acknowledgement, impact assessment, mitigation steps, communication updates, post-incident steps.

```bash
runbook init --template=incident-response incident.runbook
```

---

### health-check

System-wide health verification across all services.

Covers: database, cache, message queue, API endpoints, and dependency checks.

```bash
runbook init --template=health-check health.runbook
```

---

### scale-up

Horizontal scaling procedure for a service.

Covers: current capacity check, scale-out command, readiness verification, load balancer update.

```bash
runbook init --template=scale-up scale-api.runbook
```

---

### backup-restore

Backup verification and restore test.

Covers: backup integrity check, restore to staging, data verification, cleanup.

```bash
runbook init --template=backup-restore restore-test.runbook
```

---

### secret-rotation

Rotate API keys and credentials with zero downtime.

Covers: new secret generation, parallel deployment (old + new accepted), cutover, old secret revocation, verification.

```bash
runbook init --template=secret-rotation rotate-secrets.runbook
```

---

## Minimal skeleton

If you don't need a template, `runbook init` without `--template` creates a
minimal skeleton with frontmatter, one check block, and one step block:

```bash
runbook init my-runbook.runbook
```
