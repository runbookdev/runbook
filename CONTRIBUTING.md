# Contributing to runbook

Thank you for your interest in contributing! Here's how to get started.

## Development setup

```bash
# Prerequisites: Go 1.22+, golangci-lint
git clone https://github.com/runbookdev/runbook.git
cd runbook
make build
make test
```

## Making changes

1. Fork the repo and create a branch from `main`
2. Write your code and add tests
3. Run `make test` and `make lint`
4. Update documentation if applicable
5. Submit a pull request

## Code style

- Standard Go formatting (`gofmt`)
- Table-driven tests
- Wrap errors with context: `fmt.Errorf("parsing step: %w", err)`
- Keep functions short and focused

## Reporting bugs

Use the [bug report template](https://github.com/runbookdev/runbook/issues/new?template=bug_report.yml).

## Questions?

Use [GitHub Discussions](https://github.com/runbookdev/runbook/discussions) for questions and ideas.
