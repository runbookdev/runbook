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

// Package detect provides project-environment detection for the runbook CLI.
//
// It is used by both the "runbook env" command and the embedded shell functions
// (runbook-detect / runbook-prompt-indicator) produced by "runbook shell-init".
//
// # Overview
//
// Detection is a two-phase operation performed by [DetectProject]:
//
//  1. Project-type detection — inspects well-known marker files and directories
//     at the top level of the target directory and returns a short identifier
//     such as "go", "nodejs", or "terraform".
//
//  2. Runbook scan — globs for *.runbook files, parses only their YAML
//     frontmatter (stops at the second --- delimiter), and aggregates the
//     environments and required tools declared across all files.
//
// The tool-availability step is handled separately by [CheckTools], which
// calls exec.LookPath for each tool name. This makes it easy to call
// CheckTools in isolation for CI pre-flight checks.
//
// # Project-type priority
//
// When multiple marker files are present the first matching rule wins.
// The evaluation order is:
//
//	go.mod → go
//	package.json → nodejs
//	Cargo.toml → rust
//	pyproject.toml | requirements.txt → python
//	Dockerfile → docker
//	docker-compose.yml | docker-compose.yaml → docker-compose
//	Makefile → make
//	*.tf (any top-level file) → terraform
//	kubernetes/ | k8s/ directory → kubernetes
//	.github/workflows/ directory → github-actions
//	Jenkinsfile → jenkins
//	(none of the above) → unknown
//
// # Usage
//
//	info := detect.DetectProject(".")
//	fmt.Println(info.DisplayName())          // e.g. "Go project"
//	fmt.Println(info.Tools.Missing)          // tools declared but absent from PATH
//
// To check tool availability independently:
//
//	report := detect.CheckTools([]string{"kubectl", "helm"})
//	if len(report.Missing) > 0 {
//	    log.Fatalf("missing: %v", report.Missing)
//	}
package detect

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/runbookdev/runbook/internal/ast"
)

// ProjectInfo holds the result of a project-environment scan produced by
// [DetectProject]. All slice fields are non-nil; callers do not need to
// guard against nil before ranging.
type ProjectInfo struct {
	// ProjectType is a short identifier such as "go", "nodejs", "rust", etc.
	// Use [ProjectInfo.DisplayName] for the human-readable label.
	ProjectType string
	// RunbookFiles is the ordered list of *.runbook files found in the
	// scanned directory. The order follows the filesystem glob (typically
	// lexicographic on most platforms).
	RunbookFiles []RunbookInfo
	// Tools aggregates requires.tools from all .runbook files and reports
	// which ones are available in PATH. See [ToolReport] for details.
	Tools ToolReport
	// Environments is the deduplicated, sorted union of all environments
	// declared across all .runbook files (from frontmatter environments: fields).
	Environments []string
}

// DisplayName returns the human-readable label for the project type.
// Examples: "Go project", "Node.js project", "Terraform project".
// If the project type is not in the built-in table, the raw ProjectType
// string is returned unchanged.
func (p ProjectInfo) DisplayName() string {
	if name, ok := projectTypeNames[p.ProjectType]; ok {
		return name
	}
	return p.ProjectType
}

// RunbookInfo holds the frontmatter fields extracted from a single .runbook
// file that are relevant to environment detection. Only frontmatter is parsed;
// the Markdown body is not read.
type RunbookInfo struct {
	File         string   // basename of the file (e.g. "deploy.runbook")
	Name         string   // value of the name: frontmatter field
	Environments []string // value of the environments: frontmatter field; nil when absent
}

// ToolReport summarises required-tool availability for a set of .runbook files.
// It is produced by [CheckTools] and embedded in [ProjectInfo.Tools].
// All three slices are always non-nil (never JSON-encoded as null).
type ToolReport struct {
	Required []string // union of requires.tools across all scanned .runbook files
	Found    []string // subset of Required that exec.LookPath resolved in PATH
	Missing  []string // subset of Required that exec.LookPath could not resolve
}

// projectTypeNames maps short identifiers to display labels.
var projectTypeNames = map[string]string{
	"go":             "Go project",
	"nodejs":         "Node.js project",
	"rust":           "Rust project",
	"python":         "Python project",
	"docker":         "Docker project",
	"docker-compose": "Docker Compose",
	"make":           "Make-based build",
	"terraform":      "Terraform project",
	"kubernetes":     "Kubernetes project",
	"github-actions": "GitHub Actions CI",
	"jenkins":        "Jenkins CI",
	"unknown":        "unknown",
}

// DetectProject scans dir and returns a [ProjectInfo] summarising the
// project type, discovered .runbook files, declared environments, and
// required-tool availability.
//
// Detection is intentionally lightweight:
//   - Project type is determined by stat-ing marker files/directories only.
//   - Frontmatter is parsed with a fast line scanner that stops at the second
//     --- delimiter, so even very large .runbook files are read minimally.
//   - Tool availability is checked with exec.LookPath (no subprocess is
//     spawned).
//
// dir may be an absolute or relative path. Use "." for the current directory.
// Errors reading individual .runbook files are silently skipped so that a
// single unreadable file does not abort the scan.
func DetectProject(dir string) ProjectInfo {
	projectType := detectProjectType(dir)
	runbooks, allEnvs, rbTools := scanRunbooks(dir)
	tools := CheckTools(rbTools)

	return ProjectInfo{
		ProjectType:  projectType,
		RunbookFiles: runbooks,
		Tools:        tools,
		Environments: allEnvs,
	}
}

// CheckTools checks which tools in the provided list are available in PATH
// and returns a [ToolReport]. Availability is determined using exec.LookPath,
// which follows the same resolution semantics as the shell.
//
// The three slices in the returned report ([ToolReport.Required],
// [ToolReport.Found], [ToolReport.Missing]) are never nil, so callers can
// safely range over them or encode them as JSON without a nil check.
//
// Passing an empty or nil slice returns a report with three empty slices.
func CheckTools(tools []string) ToolReport {
	report := ToolReport{
		Required: tools,
		Found:    []string{},
		Missing:  []string{},
	}
	if report.Required == nil {
		report.Required = []string{}
	}
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err == nil {
			report.Found = append(report.Found, tool)
		} else {
			report.Missing = append(report.Missing, tool)
		}
	}
	return report
}

// detectProjectType returns the short project-type identifier for dir by
// inspecting well-known marker files and directories. The first matching
// rule in the priority list wins; see the package doc for the full order.
func detectProjectType(dir string) string {
	has := func(rel string) bool {
		_, err := os.Stat(filepath.Join(dir, rel))
		return err == nil
	}

	switch {
	case has("go.mod"):
		return "go"
	case has("package.json"):
		return "nodejs"
	case has("Cargo.toml"):
		return "rust"
	case has("pyproject.toml") || has("requirements.txt"):
		return "python"
	case has("Dockerfile"):
		return "docker"
	case has("docker-compose.yml") || has("docker-compose.yaml"):
		return "docker-compose"
	case has("Makefile"):
		return "make"
	}

	// Terraform: any *.tf file at top level.
	if tfFiles, _ := filepath.Glob(filepath.Join(dir, "*.tf")); len(tfFiles) > 0 {
		return "terraform"
	}
	// Kubernetes: well-known subdirectory names.
	if has("kubernetes") || has("k8s") {
		return "kubernetes"
	}
	// GitHub Actions: workflow directory present.
	if has(".github/workflows") {
		return "github-actions"
	}
	if has("Jenkinsfile") {
		return "jenkins"
	}
	return "unknown"
}

// scanRunbooks globs dir for *.runbook files and parses their frontmatter.
// It returns:
//   - the slice of [RunbookInfo] (one entry per file, in glob order)
//   - a sorted, deduplicated union of all declared environments
//   - a deduplicated union of all required tools (declaration order preserved)
//
// Files that cannot be read are silently skipped.
func scanRunbooks(dir string) ([]RunbookInfo, []string, []string) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.runbook"))

	var infos []RunbookInfo
	seenEnvs := make(map[string]bool)
	seenTools := make(map[string]bool)
	var allEnvs []string
	var allTools []string

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		meta := parseFrontmatter(string(data))
		envs := meta.Environments
		if envs == nil {
			envs = []string{}
		}
		infos = append(infos, RunbookInfo{
			File:         filepath.Base(f),
			Name:         meta.Name,
			Environments: envs,
		})
		for _, e := range meta.Environments {
			if !seenEnvs[e] {
				seenEnvs[e] = true
				allEnvs = append(allEnvs, e)
			}
		}
		for _, t := range meta.Requires.Tools {
			if !seenTools[t] {
				seenTools[t] = true
				allTools = append(allTools, t)
			}
		}
	}

	sort.Strings(allEnvs)
	return infos, allEnvs, allTools
}

// parseFrontmatter extracts and unmarshals YAML frontmatter from .runbook
// content. It stops at the second --- delimiter, so only the frontmatter
// block is decoded — the Markdown body is never read.
// Returns a zero-value [ast.Metadata] if no valid frontmatter is present.
func parseFrontmatter(content string) ast.Metadata {
	const delim = "---"
	if !strings.HasPrefix(content, delim) {
		return ast.Metadata{}
	}
	rest := content[len(delim):]
	rest = strings.TrimPrefix(strings.TrimPrefix(rest, "\r\n"), "\n")
	body, _, found := strings.Cut(rest, "\n---")
	if !found {
		return ast.Metadata{}
	}
	var meta ast.Metadata
	_ = yaml.Unmarshal([]byte(body), &meta)
	return meta
}
