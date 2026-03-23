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
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/runbookdev/runbook/internal/ast"
)

// completionVarPattern matches {{variable_name}} placeholders in runbook content.
var completionVarPattern = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)

// newCompletionCmd returns a hidden "completion" command with bash, zsh, and fish subcommands.
func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "completion [bash|zsh|fish]",
		Short:  "Generate shell completion scripts",
		Hidden: true,
		Long: `Generate a shell completion script for runbook.

Bash:
  source <(runbook completion bash)
  # To persist across sessions, add the above line to ~/.bashrc.

Zsh:
  source <(runbook completion zsh)
  # To persist across sessions, add the above line to ~/.zshrc.

Fish:
  runbook completion fish > ~/.config/fish/completions/runbook.fish`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:                "bash",
			Short:              "Generate bash completion script",
			DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return cmd.Root().GenBashCompletionV2(os.Stdout, true)
			},
		},
		&cobra.Command{
			Use:                "zsh",
			Short:              "Generate zsh completion script",
			DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return cmd.Root().GenZshCompletion(os.Stdout)
			},
		},
		&cobra.Command{
			Use:                "fish",
			Short:              "Generate fish completion script",
			DisableFlagParsing: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			},
		},
	)

	return cmd
}

// completeRunbookFiles is a ValidArgsFunction that restricts completion to *.runbook files.
func completeRunbookFiles(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return []string{"runbook"}, cobra.ShellCompDirectiveFilterFileExt
}

// completeEnvNames completes environment names extracted from *.runbook files in the current directory.
func completeEnvNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return envNamesFromRunbooks("."), cobra.ShellCompDirectiveNoFileComp
}

// completeVarNames completes variable names (as "name=") found in *.runbook files in the current directory.
func completeVarNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	names := varNamesFromRunbooks(".")
	result := make([]string, len(names))
	for i, n := range names {
		result[i] = n + "="
	}
	return result, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp
}

// completeTemplateNames completes built-in template names for the init command.
func completeTemplateNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	names := make([]string, 0, len(templateRegistry))
	for name := range templateRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

// envNamesFromRunbooks scans *.runbook files in dir, parses environments: from frontmatter,
// and returns a deduplicated sorted list.
func envNamesFromRunbooks(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.runbook"))
	seen := make(map[string]bool)
	var result []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		meta, err := parseFrontmatterForCompletion(string(data))
		if err != nil {
			continue
		}
		for _, env := range meta.Environments {
			if !seen[env] {
				seen[env] = true
				result = append(result, env)
			}
		}
	}
	sort.Strings(result)
	return result
}

// varNamesFromRunbooks scans *.runbook files in dir for {{variable_name}} placeholders
// and returns a deduplicated sorted list of variable names.
func varNamesFromRunbooks(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.runbook"))
	seen := make(map[string]bool)
	var result []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, match := range completionVarPattern.FindAllStringSubmatch(string(data), -1) {
			name := match[1]
			if !seen[name] {
				seen[name] = true
				result = append(result, name)
			}
		}
	}
	sort.Strings(result)
	return result
}

// parseFrontmatterForCompletion extracts YAML frontmatter from content and unmarshals
// only the fields needed for completion (environments). Errors are silently ignored
// because best-effort completion is always preferable to no completion.
func parseFrontmatterForCompletion(content string) (ast.Metadata, error) {
	const delim = "---"
	if !strings.HasPrefix(content, delim) {
		return ast.Metadata{}, nil
	}
	rest := content[len(delim):]
	// Trim the newline immediately following the opening delimiter.
	rest = strings.TrimPrefix(strings.TrimPrefix(rest, "\r\n"), "\n")
	body, _, found := strings.Cut(rest, "\n---")
	if !found {
		return ast.Metadata{}, nil
	}
	var meta ast.Metadata
	// Use lenient (non-strict) unmarshalling so unknown fields do not cause errors.
	_ = yaml.Unmarshal([]byte(body), &meta)
	return meta, nil
}
