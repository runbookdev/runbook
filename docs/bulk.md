# Bulk execution

`runbook bulk` executes many `.runbook` files in a single invocation. It is the right tool
when you need to:

- Run a list of runbooks against a single target (a "smoke suite", a "release fan-out")
- Run the same runbook against many targets (every tenant, every region, every environment)
- Do both at once: N files × M parameter bindings

Every job still goes through the full `runbook run` lifecycle — parse, validate, resolve
variables, run checks, execute steps, roll back on failure. The bulk layer only adds
coordination: concurrency, fail-fast vs. keep-going, prefixed output, and a final aggregate
report.

---

## Invocation

There are three ways to tell `runbook bulk` which files to run. They compose.

```bash
# Positional arguments
runbook bulk deploy-api.runbook deploy-web.runbook

# One or more globs (repeatable)
runbook bulk --glob 'deploys/*.runbook' --glob 'tests/smoke-*.runbook'

# Mix: positional files plus a glob
runbook bulk critical.runbook --glob 'deploys/*.runbook'
```

Positional files come first, glob expansions are appended in lexical order, and duplicates
are dropped (based on the absolute path).

Missing or non-regular-file paths fail the run up front, before any work starts, with a
consolidated list of every bad path so you can fix them in one pass.

---

## Concurrency

Two independent dials control how much work runs in parallel:

| Flag                   | What it caps                                                |
|------------------------|-------------------------------------------------------------|
| `-j`, `--max-runbooks` | How many **runbooks** run at once (the outer pool)          |
| `--max-parallel`       | How many **steps inside one runbook** run at once (the DAG) |

The two multiply. `--max-runbooks 4 --max-parallel 8` means up to 32 step goroutines in
flight. Both dials are capped at `256`; values above the cap are clamped with a warning.

Defaults are conservative — `--max-runbooks 1` (sequential) and `--max-parallel 0` (off).
Opt into parallelism explicitly for the use case you have.

```bash
# Independent runbooks, sequential step execution, 4 in parallel
runbook bulk --glob 'deploys/*.runbook' --max-runbooks 4

# A single runbook with internal DAG parallelism (use `runbook run`, not bulk)
runbook run deploy.runbook --max-parallel 8
```

When `--max-runbooks > 1`, `--non-interactive` is auto-forced so no worker can block on
`confirm:` prompts (runbook confirmations auto-approve in non-interactive mode).

---

## Failure handling

By default, the first failing runbook cancels the rest. Pending runs are marked `skipped`
in the report. Use `--keep-going` to run every file regardless of earlier failures — useful
for smoke suites where every test should report its own result.

```bash
# Stop on first failure (default)
runbook bulk --glob 'deploys/*.runbook'

# Run everything, even after a failure
runbook bulk --glob 'smoke/*.runbook' --keep-going
```

The process exit code is the **highest-severity** per-run exit code across all jobs:

| Exit | Meaning                      |
|------|------------------------------|
| `0`  | Every job succeeded          |
| `1`  | At least one step failed     |
| `2`  | At least one run rolled back |
| `3`  | Validation error somewhere   |
| `4`  | A precondition check failed  |
| `10` | User aborted                 |
| `20` | Internal error               |

---

## Output

With `--max-runbooks=1` (the default), each runbook streams its output through untouched.
The UX is identical to `runbook run`.

With `--max-runbooks>1`, every line of every runbook's stdout and stderr is prefixed with
a label so interleaved streams stay attributable:

```
[deploy-api.runbook] [runbook] running 3 steps
[deploy-web.runbook] [runbook] step [1/2] "build-image"
[deploy-api.runbook] [step-one] | API deployed
```

For matrix runs (see below), the label also embeds the binding so rows of the same file
are told apart:

```
[deploy[env=prod,region=us]] [runbook] step [1/1] "rollout"
[deploy[env=prod,region=eu]] [runbook] step [1/1] "rollout"
```

A final aggregate summary is always written. Choose the format with `--report`:

```bash
# Human-readable (default), to stderr
runbook bulk --glob 'deploys/*.runbook'

# JSON, to stdout — pipe to jq
runbook bulk --glob 'deploys/*.runbook' --report json | jq '.runs[] | select(.exit_code != 0)'

# Write the JSON side-report to a file regardless of --report
runbook bulk --glob 'deploys/*.runbook' --report text --report-file /tmp/bulk.json
```

The text report shows full file paths for plain runs and compact labels for matrix rows.
The JSON payload always carries both `file` (full path) and, for matrix rows, `label` +
`vars`.

---

## Matrix / parameter sweep

Matrix mode runs the same runbook N times with different variable bindings. It's how you
fan a single runbook out over a list of tenants, regions, services, etc.

### Inline axes

Use `--matrix-var key=v1,v2,v3` (repeatable). Each flag defines one axis; the Cartesian
product becomes the job list.

```bash
# 2 envs × 2 regions = 4 runs of deploy.runbook
runbook bulk deploy.runbook \
  --matrix-var env=staging,prod \
  --matrix-var region=us,eu
```

Axis values flow into the runbook as regular template variables, so `{{env}}` and
`{{region}}` resolve per-row.

### YAML matrix file

For longer sweeps or rules that don't fit on a single command line, use `--matrix
<file.yaml>`:

```yaml
# matrix.yaml
axes:
  env: [staging, prod]
  region: [us, eu]

# Drop specific combinations
exclude:
  - env: staging
    region: eu

# Add one-off rows on top of the product
include:
  - env: prod
    region: ap
    canary: "true"
```

```bash
runbook bulk deploy.runbook --matrix matrix.yaml
```

Expansion order is: Cartesian product of `axes` (first axis varies slowest, last fastest)
→ drop rows matching any `exclude` entry → append every `include` row verbatim.

Exclude entries are interpreted as **supersets**: a one-key entry drops every row whose
value for that key matches. So `exclude: [{env: staging}]` drops every `env=staging` row
regardless of `region`.

### Combining file + inline

The two layers compose — a file supplies baseline axes, inline `--matrix-var` flags add
more. This is useful for adding a one-off dimension without editing the checked-in file:

```bash
runbook bulk deploy.runbook \
  --matrix matrix.yaml \
  --matrix-var canary=on,off
```

Declaring the same axis key twice (whether across file + inline or twice inline) is a
hard error — otherwise the Cartesian product would silently collapse.

### Limits

- A single matrix may not expand to more than `256` rows. Oversized matrices fail at
  parse time with a clear error rather than queueing millions of jobs.
- Empty axes (zero values) fail at parse time.

---

## Audit logging

Every job in a bulk run gets its own row in the audit database, just like a plain
`runbook run`. The bulk layer shares a single `*audit.Logger` across all parallel
workers — SQLite's WAL journal and busy timeout handle write serialisation. Query
the audit log with `runbook history` to see the individual runs, including their
variable bindings (redacted where secret-like).

---

## Exit code at a glance

```bash
runbook bulk ...
echo "bulk exit: $?"
```

- `0` — every job succeeded
- non-zero — the **highest-severity** failure across all jobs (rolled-back beats
  step-failed beats check-failed, etc.)

Skipped jobs (cancelled by fail-fast before dispatch) contribute `0` and do not
affect the aggregate exit code.

---

## See also

- [`runbook run`](cli-reference.md#runbook-run) — single-file execution
- [Template variables](variables.md) — resolution order that matrix bindings feed into
- [Audit logging](audit.md) — reviewing what ran
