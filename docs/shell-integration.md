# Shell Integration

`runbook shell-init` outputs a shell snippet that wires up tab completion, a short alias, and two
helper functions. Once sourced in your shell profile, the integration is invisible — no subprocess
is spawned at startup.

## Setup

Add one line to your shell profile and restart your shell (or `source` the profile):

**Bash** (`~/.bashrc`):

```bash
eval "$(runbook shell-init)"
```

**Zsh** (`~/.zshrc`):

```bash
eval "$(runbook shell-init)"
```

**Fish** (`~/.config/fish/config.fish`):

```fish
runbook shell-init --shell fish | source
```

The `--shell` flag forces a specific target. By default the shell is auto-detected from `$SHELL`.

```bash
runbook shell-init --shell bash   # explicit bash
runbook shell-init --shell zsh    # explicit zsh
runbook shell-init --shell fish   # explicit fish
```

---

## What the snippet provides

### Tab completion

All `runbook` subcommands, flags, and `.runbook` file arguments complete with `<Tab>`.

The completion script is **inlined** into the snippet — it runs no subprocess at shell startup and
does not require `runbook` to be on `PATH` before your profile is loaded.

Smart completions included:

| Context                              | What completes                                     |
|--------------------------------------|----------------------------------------------------|
| `runbook run <Tab>`                  | `.runbook` files in the current directory          |
| `runbook run file.runbook --env <Tab>`| Environment names parsed from `.runbook` frontmatter |
| `runbook run file.runbook --var <Tab>`| Variable names (`{{name}}`) found in `.runbook` files |
| `runbook init --template <Tab>`      | Built-in template names                            |
| `runbook shell-init --shell <Tab>`   | `bash`, `zsh`, `fish`                              |

### `rb` alias

```bash
rb run deploy.runbook --env staging   # same as: runbook run …
rb dry-run deploy.runbook             # same as: runbook dry-run …
```

Fish uses an abbreviation (`abbr`) so the full command is visible in your history.

### `runbook-detect`

Prints a project summary for the current (or given) directory: project type, `.runbook` files
found, required tools and their availability, and declared environments.

```
$ runbook-detect
📂 Project: my-service (Go project)
📋 Runbooks: 2 found (deploy.runbook, rollback.runbook)
🔧 Tools: go ✓, golangci-lint ✓, kubectl ✓
🌍 Environments: staging, production
```

Pass a path to inspect a different directory:

```bash
runbook-detect ./services/api
```

Project types detected: Go, Node.js (npm/yarn/pnpm), Rust, Python, Docker, Docker Compose,
Makefile, Terraform, Kubernetes, GitHub Actions CI, Jenkins CI.

### `runbook-prompt-indicator`

A lightweight function that prints `[rb:N]` when the current directory contains `.runbook` files.
It performs only a glob count — no file parsing — so it is safe to call on every prompt.

It is **not enabled by default**. Uncomment the relevant lines from the generated snippet (or add
them manually) to prepend the indicator to your prompt.

**Bash / Zsh:**

```bash
export PS1='$(runbook-prompt-indicator)'"$PS1"      # bash
export PROMPT='$(runbook-prompt-indicator)'"$PROMPT" # zsh
```

**Fish** — copy the wrapper block from the snippet into your `config.fish`:

```fish
functions --copy fish_prompt __runbook_orig_fish_prompt 2>/dev/null
if not functions --query __runbook_orig_fish_prompt
  function __runbook_orig_fish_prompt; end
end
function fish_prompt
  echo -n (runbook-prompt-indicator)
  __runbook_orig_fish_prompt
end
```

With the indicator active, the prompt looks like:

```
[rb:2] ~/code/my-service $
```

---

## Raw completion scripts

If you are a package maintainer or need to integrate completions into a custom framework, use the
lower-level `runbook completion` subcommand to generate a raw completion script without the alias
or helper functions:

```bash
# Load once in the current session
source <(runbook completion bash)
source <(runbook completion zsh)

# Install for fish (persisted across sessions)
runbook completion fish > ~/.config/fish/completions/runbook.fish
```

---

## `runbook env` — non-shell alternative

`runbook env` provides the same project-environment information as `runbook-detect` but runs
as a standard CLI command — no shell sourcing required. It is suitable for CI pipelines and
editor integrations.

```bash
runbook env                  # human-readable summary
runbook env --json           # machine-readable JSON
runbook env --check-tools    # exit 1 if any required tool is missing
```

See [CLI reference — runbook env](cli-reference.md#runbook-env) for the full flag reference and
JSON output shape.

**CI pre-flight check example (GitHub Actions):**

```yaml
- name: Check required tools
  run: runbook env --check-tools
```

---

## Troubleshooting

**Completions not working after sourcing the profile**

Run `type runbook` to confirm `runbook` is on `PATH`. The inlined completion script references
the binary by name; if it cannot be found, completion silently does nothing.

**`runbook-detect` reports "unknown" project type**

The function inspects well-known marker files (`go.mod`, `package.json`, `Cargo.toml`, etc.) in
the target directory. If none match, the project type falls back to `"unknown"`. You can still
use all other runbook features normally.

**Fish: `abbr` is not expanded in non-interactive scripts**

Fish abbreviations are an interactive-only feature. Use `runbook` (the full command) in scripts.

**Regenerating after an upgrade**

Re-run `eval "$(runbook shell-init)"` or `runbook shell-init --shell fish | source` to pick up
any changes to completions or helper functions introduced by a new version.
