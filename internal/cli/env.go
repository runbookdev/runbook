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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/runbookdev/runbook/internal/detect"
)

func newEnvCmd() *cobra.Command {
	var jsonFlag bool
	var checkToolsFlag bool

	cmd := &cobra.Command{
		Use:   "env [dir]",
		Short: "Inspect project environment (type, runbooks, tools, environments)",
		Long: `Inspect the project environment without requiring shell hooks.

Detects the project type, lists .runbook files found in the directory,
reports required tools and their PATH availability, and lists all
environments declared across .runbook frontmatter.

Examples:
  runbook env                  # Human-readable summary
  runbook env --json           # Machine-readable JSON output
  runbook env --check-tools    # Exit 0 if all required tools present, 1 if any missing

The --check-tools flag is designed for CI pre-flight checks:

  # GitHub Actions example:
  - run: runbook env --check-tools`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}

			info := detect.DetectProject(dir)

			switch {
			case checkToolsFlag:
				return runEnvCheckTools(cmd, info)
			case jsonFlag:
				return runEnvJSON(cmd, info)
			default:
				return runEnvHuman(cmd, dir, info)
			}
		},
	}

	cmd.Flags().BoolVar(&jsonFlag, "json", false, "output machine-readable JSON")
	cmd.Flags().BoolVar(&checkToolsFlag, "check-tools", false, "exit 0 if all required tools present, 1 if any missing")

	return cmd
}

// ── JSON output ───────────────────────────────────────────────────────────────

type envJSONOutput struct {
	ProjectType  string           `json:"project_type"`
	Runbooks     []envRunbookJSON `json:"runbooks"`
	Tools        envToolsJSON     `json:"tools"`
	Environments []string         `json:"environments"`
}

type envRunbookJSON struct {
	File         string   `json:"file"`
	Name         string   `json:"name"`
	Environments []string `json:"environments"`
}

type envToolsJSON struct {
	Required []string `json:"required"`
	Found    []string `json:"found"`
	Missing  []string `json:"missing"`
}

func runEnvJSON(cmd *cobra.Command, info detect.ProjectInfo) error {
	runbooks := make([]envRunbookJSON, len(info.RunbookFiles))
	for i, rb := range info.RunbookFiles {
		envs := rb.Environments
		if envs == nil {
			envs = []string{}
		}
		runbooks[i] = envRunbookJSON{
			File:         rb.File,
			Name:         rb.Name,
			Environments: envs,
		}
	}
	envs := info.Environments
	if envs == nil {
		envs = []string{}
	}

	out := envJSONOutput{
		ProjectType: info.ProjectType,
		Runbooks:    runbooks,
		Tools: envToolsJSON{
			Required: nonNil(info.Tools.Required),
			Found:    nonNil(info.Tools.Found),
			Missing:  nonNil(info.Tools.Missing),
		},
		Environments: envs,
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// ── Human-readable output ─────────────────────────────────────────────────────

func runEnvHuman(cmd *cobra.Command, dir string, info detect.ProjectInfo) error {
	sw := &shellWriter{w: cmd.OutOrStdout()}

	bold := color.New(color.Bold)
	green := color.New(color.FgGreen)
	red := color.New(color.FgRed)
	cyan := color.New(color.FgCyan)

	name := dirBaseName(dir)

	sw.f("%s %s (%s)\n", bold.Sprint("📂 Project:"), name, info.DisplayName())

	if len(info.RunbookFiles) == 0 {
		sw.f("%s none found\n", bold.Sprint("📋 Runbooks:"))
	} else {
		names := make([]string, len(info.RunbookFiles))
		for i, rb := range info.RunbookFiles {
			names[i] = rb.File
		}
		sw.f("%s %d found (%s)\n",
			bold.Sprint("📋 Runbooks:"),
			len(info.RunbookFiles),
			strings.Join(names, ", "),
		)
	}

	if len(info.Tools.Required) > 0 {
		foundSet := make(map[string]bool, len(info.Tools.Found))
		for _, t := range info.Tools.Found {
			foundSet[t] = true
		}
		parts := make([]string, 0, len(info.Tools.Required))
		for _, t := range info.Tools.Required {
			if foundSet[t] {
				parts = append(parts, green.Sprint(t)+" ✓")
			} else {
				parts = append(parts, red.Sprint(t)+" ✗")
			}
		}
		sw.f("%s %s\n", bold.Sprint("🔧 Tools:"), strings.Join(parts, ", "))
	}

	if len(info.Environments) > 0 {
		sw.f("%s %s\n", bold.Sprint("🌍 Environments:"), cyan.Sprint(strings.Join(info.Environments, ", ")))
	}

	return sw.err
}

// ── --check-tools ─────────────────────────────────────────────────────────────

func runEnvCheckTools(cmd *cobra.Command, info detect.ProjectInfo) error {
	if len(info.Tools.Missing) == 0 {
		if len(info.Tools.Required) == 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no required tools declared in .runbook files")
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "all required tools present: %s\n",
				strings.Join(info.Tools.Required, ", "))
		}
		return nil
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "missing required tools: %s\n",
		strings.Join(info.Tools.Missing, ", "))
	os.Exit(1)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// nonNil returns s if non-nil, otherwise an empty slice (so JSON encodes [] not null).
func nonNil(s []string) []string {
	if s != nil {
		return s
	}
	return []string{}
}

// dirBaseName resolves the display name for a directory path argument.
func dirBaseName(dir string) string {
	if dir == "." {
		if wd, err := os.Getwd(); err == nil {
			return filepath.Base(wd)
		}
		return "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Base(dir)
	}
	return filepath.Base(abs)
}
