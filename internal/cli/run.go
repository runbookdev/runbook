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
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/runbookdev/runbook/internal/audit"
	"github.com/runbookdev/runbook/internal/executor"
)

func newRunCmd() *cobra.Command {
	var (
		env            string
		vars           []string
		envFile        string
		nonInteractive bool
		dryRun         bool
		verbose        bool
		auditDir       string
		strict         bool
	)

	cmd := &cobra.Command{
		Use:   "run <file>",
		Short: "Execute a runbook",
		Long: `Execute a .runbook file through its full lifecycle:
parse, validate, resolve variables, run checks, execute steps,
and roll back on failure. Every execution is recorded in the
local audit log.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := loadConfig()
			filePath := args[0]

			// CLI flags override config.
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

			// Open audit logger.
			var al *audit.Logger
			dbPath := auditDir
			if dbPath == "" {
				dbPath = cfg.AuditDir
			}
			if dbPath == "" {
				p, err := audit.DefaultDBPath()
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s cannot determine audit path: %v\n", warnPrefix(), err)
				} else {
					dbPath = p
				}
			}
			if dbPath != "" {
				var err error
				al, err = audit.Open(dbPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s cannot open audit log: %v\n", warnPrefix(), err)
				} else {
					defer al.Close()
					for _, w := range al.Warnings {
						fmt.Fprintf(os.Stderr, "%s %s\n", warnPrefix(), w)
					}
				}
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
			defer stop()

			result := executor.Run(ctx, executor.RunOptions{
				FilePath:       filePath,
				Env:            env,
				Vars:           parsedVars,
				EnvFile:        envFile,
				DryRun:         dryRun,
				NonInteractive: nonInteractive,
				Verbose:        verbose,
				Strict:         strict,
				Shell:          cfg.Shell,
				AuditLogger:    al,
			})

			printRunSummary(result, verbose)
			os.Exit(result.Status.ExitCode())
			return nil
		},
	}

	cmd.Flags().StringVar(&env, "env", "", "target environment (e.g. staging, production)")
	cmd.Flags().StringArrayVar(&vars, "var", nil, "set a variable (repeatable, format: key=value)")
	cmd.Flags().StringVar(&envFile, "env-file", "", "path to a .env file for variable resolution")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "skip all confirmation prompts")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the execution plan without running commands")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show detailed step output in the summary")
	cmd.Flags().StringVar(&auditDir, "audit-dir", "", "path to the audit database (default: ~/.runbook/audit/runbook.db)")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat shell metacharacter warnings as hard errors (exit code 3)")

	cmd.ValidArgsFunction = completeRunbookFiles
	_ = cmd.RegisterFlagCompletionFunc("env", completeEnvNames)
	_ = cmd.RegisterFlagCompletionFunc("var", completeVarNames)

	return cmd
}

// parseVarFlags converts --var key=value flags into a map.
func parseVarFlags(flags []string) map[string]string {
	if len(flags) == 0 {
		return nil
	}
	m := make(map[string]string, len(flags))
	for _, f := range flags {
		k, v, ok := strings.Cut(f, "=")
		if ok {
			m[k] = v
		}
	}
	return m
}

// printRunSummary formats the final execution result for the terminal.
func printRunSummary(r *executor.RunResult, verbose bool) {
	bold := color.New(color.Bold)
	green := color.New(color.FgGreen, color.Bold)
	red := color.New(color.FgRed, color.Bold)
	yellow := color.New(color.FgYellow, color.Bold)
	dim := color.New(color.Faint)

	fmt.Fprintln(os.Stderr)
	bold.Fprintln(os.Stderr, "── Summary ──────────────────────────────────────")

	if len(r.StepResults) > 0 {
		for i, sr := range r.StepResults {
			status := green.Sprint("✓")
			if sr.Status != executor.StatusSuccess {
				status = red.Sprint("✗")
			}
			dur := dim.Sprintf("(%s)", sr.Duration.Round(time.Millisecond))
			fmt.Fprintf(os.Stderr, "  %s [%d/%d] %s %s\n", status, i+1, len(r.StepResults), sr.StepName, dur)
			if verbose && sr.Status != executor.StatusSuccess {
				if sr.Stderr != "" {
					for _, line := range strings.Split(strings.TrimSpace(sr.Stderr), "\n") {
						dim.Fprintf(os.Stderr, "         %s\n", line)
					}
				}
			}
		}
	}

	if r.RollbackReport != nil && len(r.RollbackReport.Entries) > 0 {
		yellow.Fprintln(os.Stderr, "\n  Rollbacks:")
		for _, e := range r.RollbackReport.Entries {
			status := green.Sprint("✓")
			if e.Status != executor.RollbackSuccess {
				status = red.Sprint("✗")
			}
			fmt.Fprintf(os.Stderr, "    %s %s", status, e.Name)
			if e.Error != "" {
				dim.Fprintf(os.Stderr, " (%s)", e.Error)
			}
			fmt.Fprintln(os.Stderr)
		}
	}

	dur := dim.Sprintf("(%s)", r.Duration.Round(time.Millisecond))
	switch r.Status {
	case executor.RunSuccess:
		green.Fprintf(os.Stderr, "\n  Result: SUCCESS %s\n", dur)
	case executor.RunRolledBack:
		yellow.Fprintf(os.Stderr, "\n  Result: ROLLED BACK %s\n", dur)
	case executor.RunAborted:
		yellow.Fprintf(os.Stderr, "\n  Result: ABORTED %s\n", dur)
	default:
		red.Fprintf(os.Stderr, "\n  Result: FAILED (%s) %s\n", r.Status, dur)
		if r.Error != "" {
			red.Fprintf(os.Stderr, "  Error:  %s\n", r.Error)
		}
	}

	bold.Fprintln(os.Stderr, "─────────────────────────────────────────────────")
}

// warnPrefix returns a colored warning label.
func warnPrefix() string {
	return color.YellowString("[warning]")
}
