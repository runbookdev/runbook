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

// Package ast defines the abstract-syntax-tree types produced by the parser
// and consumed by the validator, resolver, and executor.
package ast

// Block-type identifiers used throughout the parser, validator, resolver, and
// executor when referring to the kind of a fenced block. These are the
// canonical spellings that appear after the opening fence (``` check,
// ``` step, etc.) and as BlockType values in audit records.
const (
	// BlockTypeCheck identifies a precondition block.
	BlockTypeCheck = "check"
	// BlockTypeStep identifies an executable step block.
	BlockTypeStep = "step"
	// BlockTypeRollback identifies a recovery handler block.
	BlockTypeRollback = "rollback"
	// BlockTypeWait identifies a timed-pause block.
	BlockTypeWait = "wait"
)

// RunbookAST is the top-level abstract syntax tree for a parsed .runbook file.
type RunbookAST struct {
	// Metadata holds the parsed YAML frontmatter.
	Metadata Metadata
	// Checks are all precondition blocks, in document order.
	Checks []CheckNode
	// Steps are all executable step blocks, in document order.
	Steps []StepNode
	// Rollbacks are all rollback handler blocks, in document order.
	Rollbacks []RollbackNode
	// Waits are all timed-pause blocks, in document order.
	Waits []WaitNode
	// FilePath is the source path the AST was parsed from.
	FilePath string
	// RawFrontmatter is the frontmatter YAML, unparsed, for duplicate-key detection.
	RawFrontmatter string
	// ParseWarnings contains non-fatal issues detected during parsing, such as
	// lines that exceed the recommended length limit.
	ParseWarnings []string
	// ResolvedSecrets holds the subset of resolved variables whose names match
	// a secret pattern. Populated by the resolver; used for redaction.
	ResolvedSecrets map[string]string
}

// Metadata holds the YAML frontmatter fields.
type Metadata struct {
	// Name is the runbook's human-readable identifier.
	Name string `yaml:"name"`
	// Version is an optional semantic version string.
	Version string `yaml:"version"`
	// Description is free-form prose shown in listings.
	Description string `yaml:"description"`
	// Owners lists the humans or teams responsible for the runbook.
	Owners []string `yaml:"owners"`
	// Environments declares which targets the runbook is valid for.
	Environments []string `yaml:"environments"`
	// Requires declares tools, permissions, and approvals the runbook needs.
	Requires Requirements `yaml:"requires"`
	// Timeout is the global execution deadline, parsed as time.Duration.
	Timeout string `yaml:"timeout"`
	// Trigger is a free-form description of what initiates the runbook.
	Trigger string `yaml:"trigger"`
	// Severity classifies the operation (e.g. "low", "high", "critical").
	Severity string `yaml:"severity"`
	// MaxParallel caps the number of steps the DAG scheduler will run
	// concurrently. Zero means "use the caller's default" (sequential
	// unless the CLI flag overrides). Values >1 enable parallel execution
	// of independent branches in the dependency graph.
	MaxParallel int `yaml:"max_parallel"`
}

// Requirements describes tools, permissions, and approvals needed to run.
type Requirements struct {
	// Tools lists executables that must be present on PATH.
	Tools []string `yaml:"tools"`
	// Permissions describes non-tool capabilities the runbook expects.
	Permissions []string `yaml:"permissions"`
	// Approvals maps environment names to the sign-offs required.
	Approvals map[string][]string `yaml:"approvals"`
}

// CheckNode represents a precondition block that must pass before execution.
type CheckNode struct {
	// Name is the unique identifier of this block.
	Name string
	// Command is the shell script to run, after variable substitution.
	Command string
	// OriginalCommand is the command as written, before substitution.
	OriginalCommand string
	// Line is the 1-based source line of the opening fence.
	Line int
}

// StepNode represents an executable unit of work.
type StepNode struct {
	// Name is the unique identifier of this block.
	Name string
	// Command is the shell script to run, after variable substitution.
	Command string
	// OriginalCommand is the command as written, before substitution.
	OriginalCommand string
	// Rollback is the name of a RollbackNode to execute on failure.
	Rollback string
	// DependsOn is a comma-separated list of step names this step waits for.
	DependsOn string
	// Timeout is the per-step deadline, parsed as time.Duration.
	Timeout string
	// KillGrace is the time to wait between SIGTERM and SIGKILL on timeout.
	// Example: "30s". Empty means use the executor default (10s).
	KillGrace string
	// Confirm gates execution on user confirmation; may be "always" or an env name.
	Confirm string
	// Env restricts execution to the listed environments. Empty = all environments.
	Env []string
	// Line is the 1-based source line of the opening fence.
	Line int
}

// RollbackNode represents a recovery handler executed on failure.
type RollbackNode struct {
	// Name is the unique identifier of this block.
	Name string
	// Command is the shell script to run, after variable substitution.
	Command string
	// OriginalCommand is the command as written, before substitution.
	OriginalCommand string
	// Line is the 1-based source line of the opening fence.
	Line int
}

// WaitNode represents a timed pause for monitoring.
type WaitNode struct {
	// Name is the unique identifier of this block.
	Name string
	// Duration is how long to wait, parsed as time.Duration.
	Duration string
	// Command is an optional probe script evaluated while waiting.
	Command string
	// OriginalCommand is the command as written, before substitution.
	OriginalCommand string
	// AbortIf is a predicate that, when true, cuts the wait short.
	AbortIf string
	// Line is the 1-based source line of the opening fence.
	Line int
}
