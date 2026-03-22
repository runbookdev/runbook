# .runbook File Format Specification

Version: 1.0 (Foundation Phase)

See the full design document for complete specification details.

## Overview

A `.runbook` file is UTF-8 encoded text with:
- **Frontmatter**: YAML between `---` delimiters
- **Body**: Markdown with typed fenced code blocks (`check`, `step`, `rollback`, `wait`)
- **Variables**: `{{variable}}` template syntax resolved at runtime

## Quick Reference

| Block Type | Purpose                     | Required Attributes |
|------------|-----------------------------|---------------------|
| `check`    | Precondition that must pass | `name`              |
| `step`     | Executable unit of work     | `name`              |
| `rollback` | Recovery handler on failure | `name`              |
| `wait`     | Timed pause for monitoring  | `name`, `duration`  |
