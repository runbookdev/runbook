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

// Package cli implements the Cobra command tree for the runbook binary.
package cli

import (
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// Build-time version information injected via ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// New returns the root command for the runbook CLI.
func New() *cobra.Command {
	var noColor bool

	root := &cobra.Command{
		Use:   "runbook",
		Short: "Executable runbooks as code",
		Long: `runbook — executable runbooks as code.

Documentation and automation in a single .runbook file format.
Parse, validate, and execute operational runbooks with precondition
checks, sequential steps, automatic rollback, and full audit logging.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			cfg := loadConfig()
			if noColor || cfg.NoColor {
				color.NoColor = true
			}
		},
	}

	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable colored output")

	root.AddCommand(
		newRunCmd(),
		newValidateCmd(),
		newDryRunCmd(),
		newInitCmd(),
		newListTemplatesCmd(),
		newHistoryCmd(),
		newVersionCmd(),
		newDoctorCmd(),
	)

	return root
}
