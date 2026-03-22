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
	"path/filepath"
	"sort"
	"strings"

	"github.com/agnivade/levenshtein"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// templateRegistry holds the built-in templates available to "runbook init".
var templateRegistry = map[string]templateEntry{
	"deploy": {
		Description: "Service deployment with canary verification and rollback",
		File:        "deploy.runbook",
	},
	"rollback": {
		Description: "Manual rollback to a previous known-good version",
		File:        "rollback.runbook",
	},
	"failover": {
		Description: "Database failover — promote replica to primary",
		File:        "failover.runbook",
	},
	"cert-rotation": {
		Description: "TLS certificate renewal and deployment",
		File:        "cert-rotation.runbook",
	},
	"db-migration": {
		Description: "Database schema migration with backup and rollback",
		File:        "db-migration.runbook",
	},
	"incident-response": {
		Description: "P1 incident triage, mitigation, and recovery",
		File:        "incident-response.runbook",
	},
	"health-check": {
		Description: "System-wide health verification across all services",
		File:        "health-check.runbook",
	},
	"scale-up": {
		Description: "Horizontal scaling procedure for a service",
		File:        "scale-up.runbook",
	},
	"backup-restore": {
		Description: "Backup verification and restore test",
		File:        "backup-restore.runbook",
	},
	"secret-rotation": {
		Description: "Rotate API keys and credentials with zero downtime",
		File:        "secret-rotation.runbook",
	},
}

type templateEntry struct {
	Description string
	File        string
}

// minimalTemplate is the skeleton created when no --template is given.
const minimalTemplate = `---
name: My Runbook
version: 1.0.0
description: TODO — describe what this runbook does
environments: [staging, production]
timeout: 30m
---

# My Runbook

## Prerequisites

` + "```check name=\"pre-check\"" + `
echo "TODO: add precondition checks"
` + "```" + `

## Steps

` + "```step name=\"step-1\"" + `
echo "TODO: implement step 1"
` + "```" + `
`

func newInitCmd() *cobra.Command {
	var templateName string

	cmd := &cobra.Command{
		Use:   "init [filename]",
		Short: "Create a new .runbook file from a template",
		Long: `Create a new .runbook file. Without --template, generates a minimal
skeleton. With --template=<name>, uses a built-in template.

Use "runbook list-templates" to see available templates.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filename := "runbook.runbook"
			if len(args) > 0 {
				filename = args[0]
			}
			if !strings.HasSuffix(filename, ".runbook") {
				filename += ".runbook"
			}

			if _, err := os.Stat(filename); err == nil {
				return fmt.Errorf("%s already exists", filename)
			}

			var content []byte
			if templateName == "" {
				content = []byte(minimalTemplate)
			} else {
				entry, ok := templateRegistry[templateName]
				if !ok {
					msg := fmt.Sprintf("unknown template %q", templateName)
					if suggestion := suggestTemplate(templateName); suggestion != "" {
						msg += fmt.Sprintf("; did you mean %q?", suggestion)
					}
					msg += " (run 'runbook list-templates' to see available templates)"
					return fmt.Errorf("%s", msg)
				}
				// Look for template file in well-known locations.
				data, err := findTemplate(entry.File)
				if err != nil {
					return fmt.Errorf("loading template %q: %w", templateName, err)
				}
				content = data
			}

			if err := os.WriteFile(filename, content, 0o600); err != nil {
				return fmt.Errorf("writing %s: %w", filename, err)
			}

			green := color.New(color.FgGreen, color.Bold)
			green.Fprintf(os.Stderr, "Created %s\n", filename)
			return nil
		},
	}

	cmd.Flags().StringVar(&templateName, "template", "", "built-in template name (see list-templates)")

	return cmd
}

// findTemplate searches for a template file in well-known locations:
// 1. ./templates/<file>
// 2. <executable-dir>/../templates/<file>
// 3. ~/.runbook/templates/<file>
func findTemplate(file string) ([]byte, error) {
	candidates := []string{
		filepath.Join("templates", file),
	}

	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "..", "templates", file))
	}

	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".runbook", "templates", file))
	}

	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			return data, nil
		}
	}

	return nil, fmt.Errorf("template file %s not found (searched: %s)", file, strings.Join(candidates, ", "))
}

// suggestTemplate returns the closest template name using Levenshtein distance,
// or "" if no good match exists.
func suggestTemplate(target string) string {
	names := make([]string, 0, len(templateRegistry))
	for name := range templateRegistry {
		names = append(names, name)
	}
	sort.Strings(names)

	best := ""
	bestDist := len(target)/2 + 1
	for _, name := range names {
		d := levenshtein.ComputeDistance(target, name)
		if d < bestDist {
			bestDist = d
			best = name
		}
	}
	return best
}
