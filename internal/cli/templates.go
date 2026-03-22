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
	"sort"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newListTemplatesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list-templates",
		Aliases: []string{"templates"},
		Short:   "List available built-in templates",
		Long:    `List all built-in .runbook templates that can be used with "runbook init --template=<name>".`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			bold := color.New(color.Bold)
			bold.Fprintln(os.Stdout, "Available templates:")
			fmt.Fprintln(os.Stdout)

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

			names := make([]string, 0, len(templateRegistry))
			for name := range templateRegistry {
				names = append(names, name)
			}
			sort.Strings(names)

			for _, name := range names {
				entry := templateRegistry[name]
				fmt.Fprintf(w, "  %s\t%s\n", color.CyanString(name), entry.Description)
			}

			w.Flush()
			fmt.Fprintf(os.Stdout, "\nUsage: runbook init --template=<name> [filename]\n")
			return nil
		},
	}
}
