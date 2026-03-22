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
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/runbookdev/runbook/internal/executor"
	"github.com/runbookdev/runbook/internal/parser"
	"github.com/runbookdev/runbook/internal/validator"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <file>",
		Short: "Validate a .runbook file",
		Long: `Parse and validate a .runbook file, reporting all errors and warnings.
Exits with code 0 if valid, 3 if there are errors.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]

			content, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("%s: %w", filePath, err)
			}

			tree, err := parser.Parse(filePath, string(content))
			if err != nil {
				// Parser errors already include file:line context.
				return err
			}

			errs := validator.Validate(tree)

			red := color.New(color.FgRed)
			yellow := color.New(color.FgYellow)
			green := color.New(color.FgGreen, color.Bold)

			var errorCount, warnCount int
			for _, ve := range errs {
				prefix := filePath
				if ve.Line > 0 {
					prefix = fmt.Sprintf("%s:%d", filePath, ve.Line)
				}
				if ve.Severity == validator.Error {
					errorCount++
					red.Fprintf(os.Stderr, "  %s: error: %s\n", prefix, ve.Message)
				} else {
					warnCount++
					yellow.Fprintf(os.Stderr, "  %s: warning: %s\n", prefix, ve.Message)
				}
			}

			if errorCount > 0 {
				fmt.Fprintf(os.Stderr, "\n%s: %d error(s), %d warning(s)\n",
					red.Sprint("INVALID"), errorCount, warnCount)
				os.Exit(executor.RunValidationError.ExitCode())
			}

			if warnCount > 0 {
				fmt.Fprintf(os.Stderr, "\n%s with %d warning(s)\n",
					green.Sprint("VALID"), warnCount)
			} else {
				green.Fprintln(os.Stderr, "VALID")
			}

			return nil
		},
	}
}
