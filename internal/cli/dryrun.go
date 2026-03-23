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
	"os"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/runbookdev/runbook/internal/executor"
)

func newDryRunCmd() *cobra.Command {
	var (
		env     string
		vars    []string
		envFile string
		verbose bool
	)

	cmd := &cobra.Command{
		Use:   "dry-run <file>",
		Short: "Show the execution plan without running commands",
		Long:  `Alias for "runbook run --dry-run". Parses, validates, and resolves variables, then prints the plan.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := loadConfig()
			if env == "" {
				env = cfg.Env
			}
			if envFile == "" {
				envFile = cfg.EnvFile
			}

			result := executor.Run(context.Background(), executor.RunOptions{
				FilePath:       args[0],
				Env:            env,
				Vars:           parseVarFlags(vars),
				EnvFile:        envFile,
				DryRun:         true,
				Verbose:        verbose,
				NonInteractive: true,
			})

			if result.Error != "" {
				red := color.New(color.FgRed)
				red.Fprintf(os.Stderr, "[runbook] error: %s\n", result.Error)
			}
			os.Exit(result.Status.ExitCode())
			return nil
		},
	}

	cmd.Flags().StringVar(&env, "env", "", "target environment")
	cmd.Flags().StringArrayVar(&vars, "var", nil, "set a variable (repeatable, format: key=value)")
	cmd.Flags().StringVar(&envFile, "env-file", "", "path to a .env file")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "show debug-level resolution details")

	cmd.ValidArgsFunction = completeRunbookFiles
	_ = cmd.RegisterFlagCompletionFunc("env", completeEnvNames)
	_ = cmd.RegisterFlagCompletionFunc("var", completeVarNames)

	return cmd
}
