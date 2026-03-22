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

package ast

// RunbookAST is the top-level abstract syntax tree for a parsed .runbook file.
type RunbookAST struct {
	Metadata       Metadata
	Checks         []CheckNode
	Steps          []StepNode
	Rollbacks      []RollbackNode
	Waits          []WaitNode
	FilePath       string
	RawFrontmatter string
}

// Metadata holds the YAML frontmatter fields.
type Metadata struct {
	Name         string       `yaml:"name"`
	Version      string       `yaml:"version"`
	Description  string       `yaml:"description"`
	Owners       []string     `yaml:"owners"`
	Environments []string     `yaml:"environments"`
	Requires     Requirements `yaml:"requires"`
	Timeout      string       `yaml:"timeout"`
	Trigger      string       `yaml:"trigger"`
	Severity     string       `yaml:"severity"`
}

// Requirements describes tools, permissions, and approvals needed to run.
type Requirements struct {
	Tools       []string            `yaml:"tools"`
	Permissions []string            `yaml:"permissions"`
	Approvals   map[string][]string `yaml:"approvals"`
}

// CheckNode represents a precondition block that must pass before execution.
type CheckNode struct {
	Name            string
	Command         string
	OriginalCommand string
	Line            int
}

// StepNode represents an executable unit of work.
type StepNode struct {
	Name            string
	Command         string
	OriginalCommand string
	Rollback        string
	DependsOn       string
	Timeout         string
	Confirm         string
	Env             []string
	Line            int
}

// RollbackNode represents a recovery handler executed on failure.
type RollbackNode struct {
	Name            string
	Command         string
	OriginalCommand string
	Line            int
}

// WaitNode represents a timed pause for monitoring.
type WaitNode struct {
	Name            string
	Duration        string
	Command         string
	OriginalCommand string
	AbortIf         string
	Line            int
}
