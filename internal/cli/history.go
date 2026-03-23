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
	"strings"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/runbookdev/runbook/internal/audit"
	"github.com/spf13/cobra"
)

func newHistoryCmd() *cobra.Command {
	var (
		runID    string
		limit    int
		auditDir string
	)

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Query the audit log",
		Long: `Show recent runbook executions from the audit log.
Use --run-id to show details for a specific run.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := loadConfig()
			dbPath := auditDir
			if dbPath == "" {
				dbPath = cfg.AuditDir
			}
			if dbPath == "" {
				p, err := audit.DefaultDBPath()
				if err != nil {
					return fmt.Errorf("cannot determine audit path: %w", err)
				}
				dbPath = p
			}

			al, err := audit.Open(dbPath)
			if err != nil {
				return fmt.Errorf("opening audit log: %w", err)
			}
			defer al.Close()
			for _, w := range al.Warnings {
				fmt.Fprintf(os.Stderr, "[warning] %s\n", w)
			}

			if runID != "" {
				return showRunDetail(al, runID)
			}
			return showRunList(al, limit)
		},
	}

	cmd.Flags().StringVar(&runID, "run-id", "", "show details for a specific run ID")
	cmd.Flags().IntVar(&limit, "limit", 20, "number of recent runs to show")
	cmd.Flags().StringVar(&auditDir, "audit-dir", "", "path to the audit database")

	return cmd
}

func showRunList(al *audit.Logger, limit int) error {
	runs, err := al.ListRuns(limit)
	if err != nil {
		return fmt.Errorf("listing runs: %w", err)
	}

	if len(runs) == 0 {
		fmt.Fprintln(os.Stdout, "No runs found.")
		return nil
	}

	bold := color.New(color.Bold)
	bold.Fprintln(os.Stdout, "Recent runs:")
	fmt.Fprintln(os.Stdout)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n",
		bold.Sprint("ID"), bold.Sprint("NAME"), bold.Sprint("ENV"),
		bold.Sprint("STATUS"), bold.Sprint("DURATION"), bold.Sprint("STARTED"))

	for _, r := range runs {
		id := r.ID
		if len(id) > 8 {
			id = id[:8]
		}

		duration := ""
		if r.FinishedAt != nil {
			duration = r.FinishedAt.Sub(r.StartedAt).Round(time.Millisecond).String()
		} else {
			duration = "running"
		}

		started := r.StartedAt.Local().Format("2006-01-02 15:04:05")
		status := colorStatus(r.Status)

		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n",
			id, r.Name, r.Environment, status, duration, started)
	}

	w.Flush()
	return nil
}

func showRunDetail(al *audit.Logger, runID string) error {
	run, steps, err := al.GetRun(runID)
	if err != nil {
		return fmt.Errorf("querying run: %w", err)
	}

	bold := color.New(color.Bold)
	dim := color.New(color.Faint)

	bold.Fprintf(os.Stdout, "Run %s\n", run.ID)
	fmt.Fprintf(os.Stdout, "  Runbook:     %s\n", run.Runbook)
	fmt.Fprintf(os.Stdout, "  Name:        %s (v%s)\n", run.Name, run.Version)
	fmt.Fprintf(os.Stdout, "  Environment: %s\n", run.Environment)
	fmt.Fprintf(os.Stdout, "  Status:      %s\n", colorStatus(run.Status))
	fmt.Fprintf(os.Stdout, "  User:        %s@%s\n", run.User, run.Hostname)
	fmt.Fprintf(os.Stdout, "  Started:     %s\n", run.StartedAt.Local().Format(time.RFC3339))
	if run.FinishedAt != nil {
		fmt.Fprintf(os.Stdout, "  Finished:    %s\n", run.FinishedAt.Local().Format(time.RFC3339))
		fmt.Fprintf(os.Stdout, "  Duration:    %s\n", run.FinishedAt.Sub(run.StartedAt).Round(time.Millisecond))
	}

	if len(run.Variables) > 0 {
		fmt.Fprintln(os.Stdout)
		bold.Fprintln(os.Stdout, "Variables:")
		for k, v := range run.Variables {
			fmt.Fprintf(os.Stdout, "  %s = %s\n", k, v)
		}
	}

	if len(steps) > 0 {
		fmt.Fprintln(os.Stdout)
		bold.Fprintln(os.Stdout, "Steps:")

		for _, s := range steps {
			duration := s.FinishedAt.Sub(s.StartedAt).Round(time.Millisecond)
			status := colorStatus(s.Status)
			fmt.Fprintf(os.Stdout, "  [%s] %s %s %s\n",
				s.BlockType, s.StepName, status, dim.Sprintf("(%s)", duration))
			if s.Stderr != "" {
				for _, line := range strings.Split(strings.TrimSpace(s.Stderr), "\n") {
					dim.Fprintf(os.Stdout, "    %s\n", line)
				}
			}
		}
	}

	return nil
}

func colorStatus(status string) string {
	switch status {
	case "success":
		return color.GreenString(status)
	case "failed", "step_failed", "check_failed", "internal_error":
		return color.RedString(status)
	case "rolled_back", "aborted":
		return color.YellowString(status)
	case "running":
		return color.CyanString(status)
	default:
		return status
	}
}
