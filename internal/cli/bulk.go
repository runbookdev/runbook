// Copyright 2026 runbook authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/runbookdev/runbook/internal/audit"
	"github.com/runbookdev/runbook/internal/bulk"
	"github.com/runbookdev/runbook/internal/executor"
)

func newBulkCmd() *cobra.Command {
	var (
		env            string
		vars           []string
		envFile        string
		nonInteractive bool
		dryRun         bool
		verbose        bool
		auditDir       string
		strict         bool
		maxParallel    int
		maxRunbooks    int
		keepGoing      bool
		reportFormat   string
		reportFile     string
		globs          []string
		matrixFile     string
		matrixVars     []string
	)

	cmd := &cobra.Command{
		Use:   "bulk <file>...",
		Short: "Execute multiple runbooks in one invocation",
		Long: `Execute multiple .runbook files in a single invocation.

Files may be listed positionally, expanded from --glob patterns, or
both. Each file runs through the same lifecycle as ` + "`runbook run`" + ` —
parse, validate, resolve variables, run checks, execute steps, roll
back on failure — but outer concurrency is capped by --max-runbooks,
and a final aggregate summary is written at the end.

When --max-runbooks > 1 each runbook's output is prefixed with its
file name so interleaved streams stay attributable, and
--non-interactive is forced on so parallel workers never block on a
confirmation prompt.

Examples:
  runbook bulk deploy-api.runbook deploy-web.runbook
  runbook bulk --glob 'deploys/*.runbook' --max-runbooks 4
  runbook bulk --glob 'smoke/*.runbook' --keep-going --report json
  runbook bulk deploy.runbook --matrix-var env=staging,prod --matrix-var region=us,eu
  runbook bulk deploy.runbook --matrix matrix.yaml --max-runbooks 4`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, _ := loadConfig()

			files, err := collectBulkFiles(args, globs)
			if err != nil {
				return err
			}

			if len(files) == 0 {
				return fmt.Errorf("no runbook files to execute (pass files positionally or via --glob)")
			}

			// Parse report format and matrix inputs up front so a typo
			// fails fast instead of after dispatching every runbook.
			format, err := parseReportFormat(reportFormat)
			if err != nil {
				return err
			}

			bindings, err := buildMatrixBindings(matrixFile, matrixVars)
			if err != nil {
				return err
			}

			jobs := bulk.BuildJobs(files, bindings)

			if env == "" {
				env = cfg.Env
			}

			if envFile == "" {
				envFile = cfg.EnvFile
			}

			if !nonInteractive {
				nonInteractive = cfg.NonInteractive
			}

			parsedVars := parseVarFlags(vars)

			// Open a single audit logger, shared across every run.
			// SQLite WAL + *sql.DB's connection pool serialise writes
			// safely under concurrent use, so no extra mutex is needed
			// at the coordinator layer.
			var al *audit.Logger
			dbPath := auditDir
			if dbPath == "" {
				dbPath = cfg.AuditDir
			}

			if dbPath == "" {
				if p, derr := audit.DefaultDBPath(); derr != nil {
					_, _ = fmt.Fprintf(os.Stderr, "%s cannot determine audit path: %v\n", warnPrefix(), derr)
				} else {
					dbPath = p
				}
			}

			if dbPath != "" {
				logger, openErr := audit.Open(dbPath)
				if openErr != nil {
					_, _ = fmt.Fprintf(os.Stderr, "%s cannot open audit log: %v\n", warnPrefix(), openErr)
				} else {
					al = logger
					defer al.Close()
					for _, w := range al.Warnings {
						_, _ = fmt.Fprintf(os.Stderr, "%s %s\n", warnPrefix(), w)
					}
				}
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
			defer stop()

			opts := bulk.Options{
				Jobs:        jobs,
				MaxRunbooks: maxRunbooks,
				FailFast:    !keepGoing,
				Template: executor.RunOptions{
					Env:            env,
					Vars:           parsedVars,
					EnvFile:        envFile,
					DryRun:         dryRun,
					NonInteractive: nonInteractive,
					Verbose:        verbose,
					Strict:         strict,
					Shell:          cfg.Shell,
					AuditLogger:    al,
					MaxParallel:    maxParallel,
				},
				Stdout:       os.Stdout,
				Stderr:       os.Stderr,
				ReportStderr: os.Stderr,
			}

			result, runErr := bulk.Run(ctx, opts)
			if runErr != nil {
				return runErr
			}

			// JSON reports go to stdout so downstream tooling can
			// pipe them to `jq` or similar without per-tool stderr
			// capture; the text report stays on stderr so it doesn't
			// contaminate stdout pipelines. Mirrors the convention
			// already used by `runbook env --json`.
			reportSink := io.Writer(os.Stderr)
			if format == bulk.ReportJSON {
				reportSink = os.Stdout
			}
			if err := bulk.WriteReport(reportSink, result, format); err != nil {
				return fmt.Errorf("writing report: %w", err)
			}

			if reportFile != "" {
				if err := writeReportFile(reportFile, result); err != nil {
					return fmt.Errorf("writing report file: %w", err)
				}
			}

			os.Exit(result.ExitCode())
			return nil
		},
	}

	cmd.Flags().StringVar(&env, "env", "", "target environment (e.g. staging, production)")
	cmd.Flags().StringArrayVar(&vars, "var", nil, "set a variable (repeatable, format: key=value)")
	cmd.Flags().StringVar(&envFile, "env-file", "", "path to a .env file for variable resolution")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "skip all confirmation prompts (forced when --max-runbooks>1)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the execution plan without running commands")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show detailed step output in the summary")
	cmd.Flags().StringVar(&auditDir, "audit-dir", "", "path to the audit database (default: ~/.runbook/audit/runbook.db)")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat shell metacharacter warnings as hard errors (exit code 3)")
	cmd.Flags().IntVar(&maxParallel, "max-parallel", 0, "max steps the DAG scheduler runs in parallel within a single runbook")
	cmd.Flags().IntVarP(&maxRunbooks, "max-runbooks", "j", 1, "max runbooks executed concurrently (1 = sequential)")
	cmd.Flags().BoolVar(&keepGoing, "keep-going", false, "continue dispatching remaining runbooks after a failure (default: fail fast)")
	cmd.Flags().StringVar(&reportFormat, "report", "text", "final summary format (text|json)")
	cmd.Flags().StringVar(&reportFile, "report-file", "", "also write the JSON report to this file path")
	cmd.Flags().StringArrayVar(&globs, "glob", nil, "add every .runbook file matching this glob (repeatable)")
	cmd.Flags().StringVar(&matrixFile, "matrix", "", "YAML matrix file: run each file once per axis combination")
	cmd.Flags().StringArrayVar(&matrixVars, "matrix-var", nil, "inline matrix axis (repeatable, format: key=v1,v2,v3)")

	return cmd
}

// buildMatrixBindings merges --matrix (YAML file) and --matrix-var
// flags into a single Matrix and expands it. Both sources are allowed
// on the same invocation — inline --matrix-var axes are appended to
// whatever the file declared, which lets operators layer a quick
// one-off dimension onto a checked-in matrix file. Returns (nil, nil)
// when neither flag is set (the phase-1 no-matrix case).
func buildMatrixBindings(matrixPath string, matrixVars []string) ([]bulk.Binding, error) {
	if matrixPath == "" && len(matrixVars) == 0 {
		return nil, nil
	}

	var m bulk.Matrix
	if matrixPath != "" {
		parsed, err := bulk.ParseMatrixFile(matrixPath)
		if err != nil {
			return nil, err
		}
		m = parsed
	}

	for _, raw := range matrixVars {
		axis, err := bulk.ParseMatrixVar(raw)
		if err != nil {
			return nil, err
		}
		m.Axes = append(m.Axes, axis)
	}
	return m.Expand()
}

// collectBulkFiles merges positional args with every --glob expansion,
// deduplicating while preserving first-seen order, and verifies each
// entry exists as a regular file. Returns an error listing all missing
// paths at once so the user can fix them in a single pass.
func collectBulkFiles(args, globs []string) ([]string, error) {
	var ordered []string
	seen := make(map[string]bool)

	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		ordered = append(ordered, p)
	}

	for _, a := range args {
		add(a)
	}

	for _, pattern := range globs {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", pattern, err)
		}
		// Glob returns matches in lexical order; keep that stable so
		// repeated invocations produce identical report ordering.
		sort.Strings(matches)
		for _, m := range matches {
			add(m)
		}
	}

	var missing []string
	for _, p := range ordered {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			missing = append(missing, p)
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("file(s) not found or not a regular file: %v", missing)
	}
	return ordered, nil
}

// parseReportFormat validates the --report flag value.
func parseReportFormat(s string) (bulk.ReportFormat, error) {
	switch s {
	case "", "text":
		return bulk.ReportText, nil
	case "json":
		return bulk.ReportJSON, nil
	default:
		return "", fmt.Errorf("invalid --report value %q (want: text|json)", s)
	}
}

// writeReportFile serialises the bulk result as JSON into path. The
// file is always JSON regardless of --report so tooling can consume
// it unconditionally; --report controls only the on-stderr summary.
// Created with 0o600 because the report embeds file paths and error
// messages that may leak project layout or failure context.
func writeReportFile(path string, result *bulk.BulkResult) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return bulk.WriteReport(f, result, bulk.ReportJSON)
}
