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

package validator

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/agnivade/levenshtein"
	rbast "github.com/runbookdev/runbook/internal/ast"
	"gopkg.in/yaml.v3"
)

// Severity indicates whether a validation issue is an error or warning.
type Severity int

const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// ValidationError represents a single validation issue found in a runbook.
type ValidationError struct {
	Severity Severity
	Message  string
	Line     int
}

func (e ValidationError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s: line %d: %s", e.Severity, e.Line, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Severity, e.Message)
}

// varPattern matches {{identifier}} template variables.
var varPattern = regexp.MustCompile(`\{\{(.*?)\}\}`)

// identPattern matches a valid Go-style identifier.
var identPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Validate runs all validation rules against the AST and returns any issues found.
func Validate(ast *rbast.RunbookAST) []ValidationError {
	var errs []ValidationError

	errs = append(errs, v1UniqueNames(ast)...)
	errs = append(errs, v2RollbackRefs(ast)...)
	errs = append(errs, v3UnusedRollbacks(ast)...)
	errs = append(errs, v4FrontmatterName(ast)...)
	errs = append(errs, v5EnvFilters(ast)...)
	errs = append(errs, v6StepTimeouts(ast)...)
	errs = append(errs, v7TemplateVars(ast)...)
	errs = append(errs, v8RollbackCycles(ast)...)
	errs = append(errs, v9DependsOnRefs(ast)...)
	errs = append(errs, v10RequiredTools(ast)...)
	errs = append(errs, v11NonEmptyCommands(ast)...)
	errs = append(errs, v12DuplicateYAMLKeys(ast)...)

	return errs
}

// HasErrors returns true if any validation error has Error severity.
func HasErrors(errs []ValidationError) bool {
	for _, e := range errs {
		if e.Severity == Error {
			return true
		}
	}
	return false
}

// v1UniqueNames checks that all block names are unique across all block types.
func v1UniqueNames(ast *rbast.RunbookAST) []ValidationError {
	type entry struct {
		blockType string
		line      int
	}
	seen := make(map[string]entry)
	var errs []ValidationError

	record := func(name, blockType string, line int) {
		if prev, ok := seen[name]; ok {
			errs = append(errs, ValidationError{
				Severity: Error,
				Message:  fmt.Sprintf("duplicate block name %q (first defined as %s at line %d)", name, prev.blockType, prev.line),
				Line:     line,
			})
		} else {
			seen[name] = entry{blockType: blockType, line: line}
		}
	}

	for _, c := range ast.Checks {
		record(c.Name, "check", c.Line)
	}
	for _, s := range ast.Steps {
		record(s.Name, "step", s.Line)
	}
	for _, r := range ast.Rollbacks {
		record(r.Name, "rollback", r.Line)
	}
	for _, w := range ast.Waits {
		record(w.Name, "wait", w.Line)
	}
	return errs
}

// v2RollbackRefs checks that every step's rollback attribute references an existing rollback block.
func v2RollbackRefs(ast *rbast.RunbookAST) []ValidationError {
	rollbackNames := make(map[string]bool)
	for _, r := range ast.Rollbacks {
		rollbackNames[r.Name] = true
	}

	var errs []ValidationError
	for _, s := range ast.Steps {
		if s.Rollback == "" {
			continue
		}
		if !rollbackNames[s.Rollback] {
			msg := fmt.Sprintf("step %q references non-existent rollback %q", s.Name, s.Rollback)
			if suggestion := suggestName(s.Rollback, rollbackNames); suggestion != "" {
				msg += fmt.Sprintf("; did you mean %q?", suggestion)
			}
			errs = append(errs, ValidationError{
				Severity: Error,
				Message:  msg,
				Line:     s.Line,
			})
		}
	}
	return errs
}

// v3UnusedRollbacks warns if a rollback block is never referenced by any step.
func v3UnusedRollbacks(ast *rbast.RunbookAST) []ValidationError {
	referenced := make(map[string]bool)
	for _, s := range ast.Steps {
		if s.Rollback != "" {
			referenced[s.Rollback] = true
		}
	}

	var errs []ValidationError
	for _, r := range ast.Rollbacks {
		if !referenced[r.Name] {
			errs = append(errs, ValidationError{
				Severity: Warning,
				Message:  fmt.Sprintf("rollback %q is never referenced by any step", r.Name),
				Line:     r.Line,
			})
		}
	}
	return errs
}

// v4FrontmatterName checks that the frontmatter name field is present and non-empty.
func v4FrontmatterName(ast *rbast.RunbookAST) []ValidationError {
	if strings.TrimSpace(ast.Metadata.Name) == "" {
		return []ValidationError{{
			Severity: Error,
			Message:  "frontmatter 'name' field is required and must not be empty",
			Line:     0,
		}}
	}
	return nil
}

// v5EnvFilters warns if step env filters reference environments not declared in frontmatter.
func v5EnvFilters(ast *rbast.RunbookAST) []ValidationError {
	declared := make(map[string]bool)
	for _, e := range ast.Metadata.Environments {
		declared[e] = true
	}

	if len(declared) == 0 {
		return nil
	}

	var errs []ValidationError
	for _, s := range ast.Steps {
		for _, env := range s.Env {
			if !declared[env] {
				errs = append(errs, ValidationError{
					Severity: Warning,
					Message:  fmt.Sprintf("step %q references environment %q not declared in frontmatter environments", s.Name, env),
					Line:     s.Line,
				})
			}
		}
	}
	return errs
}

// v6StepTimeouts warns if step timeouts fall outside the 1s–24h range.
func v6StepTimeouts(ast *rbast.RunbookAST) []ValidationError {
	const minTimeout = time.Second
	const maxTimeout = 24 * time.Hour

	var errs []ValidationError
	for _, s := range ast.Steps {
		if s.Timeout == "" {
			continue
		}
		d, err := time.ParseDuration(s.Timeout)
		if err != nil {
			errs = append(errs, ValidationError{
				Severity: Error,
				Message:  fmt.Sprintf("step %q has invalid timeout %q: %v", s.Name, s.Timeout, err),
				Line:     s.Line,
			})
			continue
		}
		if d < minTimeout || d > maxTimeout {
			errs = append(errs, ValidationError{
				Severity: Warning,
				Message:  fmt.Sprintf("step %q timeout %s is outside recommended range (1s–24h)", s.Name, s.Timeout),
				Line:     s.Line,
			})
		}
	}
	return errs
}

// v7TemplateVars checks that all {{...}} expressions contain valid identifiers.
func v7TemplateVars(ast *rbast.RunbookAST) []ValidationError {
	var errs []ValidationError

	checkVars := func(command string, blockName string, line int) {
		matches := varPattern.FindAllStringSubmatch(command, -1)
		for _, m := range matches {
			inner := strings.TrimSpace(m[1])
			if inner == "" {
				errs = append(errs, ValidationError{
					Severity: Error,
					Message:  fmt.Sprintf("block %q contains empty template variable {{}}", blockName),
					Line:     line,
				})
			} else if !identPattern.MatchString(inner) {
				errs = append(errs, ValidationError{
					Severity: Error,
					Message:  fmt.Sprintf("block %q contains invalid template variable {{%s}}", blockName, inner),
					Line:     line,
				})
			}
		}
	}

	for _, c := range ast.Checks {
		checkVars(c.Command, c.Name, c.Line)
	}
	for _, s := range ast.Steps {
		checkVars(s.Command, s.Name, s.Line)
	}
	for _, r := range ast.Rollbacks {
		checkVars(r.Command, r.Name, r.Line)
	}
	for _, w := range ast.Waits {
		checkVars(w.Command, w.Name, w.Line)
	}
	return errs
}

// v8RollbackCycles checks that rollback references do not create cycles.
// A cycle would occur if step A's rollback references a rollback that itself
// is associated with a step that rolls back to A's rollback, etc.
// In practice this checks that no step's rollback name matches another step's
// name that transitively rolls back to the first.
func v8RollbackCycles(ast *rbast.RunbookAST) []ValidationError {
	// Build a mapping: step name -> rollback name
	stepRollback := make(map[string]string)
	stepLine := make(map[string]int)
	for _, s := range ast.Steps {
		if s.Rollback != "" {
			stepRollback[s.Name] = s.Rollback
			stepLine[s.Name] = s.Line
		}
	}

	// Build a mapping: rollback name -> step name (which step uses this rollback)
	rollbackToStep := make(map[string]string)
	for _, s := range ast.Steps {
		if s.Rollback != "" {
			rollbackToStep[s.Rollback] = s.Name
		}
	}

	// Check for cycles: follow the chain step -> rollback -> step-that-has-same-name -> rollback -> ...
	var errs []ValidationError
	for stepName, rbName := range stepRollback {
		visited := map[string]bool{stepName: true}
		current := rbName
		for current != "" {
			// Does any step have the same name as this rollback?
			if nextStep, ok := rollbackToStep[current]; ok && nextStep != stepName {
				if visited[nextStep] {
					errs = append(errs, ValidationError{
						Severity: Error,
						Message:  fmt.Sprintf("rollback cycle detected involving step %q", stepName),
						Line:     stepLine[stepName],
					})
					break
				}
				visited[nextStep] = true
				current = stepRollback[nextStep]
			} else {
				break
			}
		}
	}
	return errs
}

// v9DependsOnRefs warns if depends_on references a non-existent step.
func v9DependsOnRefs(ast *rbast.RunbookAST) []ValidationError {
	stepNames := make(map[string]bool)
	for _, s := range ast.Steps {
		stepNames[s.Name] = true
	}

	var errs []ValidationError
	for _, s := range ast.Steps {
		if s.DependsOn == "" {
			continue
		}
		if !stepNames[s.DependsOn] {
			msg := fmt.Sprintf("step %q depends_on non-existent step %q", s.Name, s.DependsOn)
			if suggestion := suggestName(s.DependsOn, stepNames); suggestion != "" {
				msg += fmt.Sprintf("; did you mean %q?", suggestion)
			}
			errs = append(errs, ValidationError{
				Severity: Warning,
				Message:  msg,
				Line:     s.Line,
			})
		}
	}
	return errs
}

// lookPathFunc is the function used to check tool availability. It can be
// overridden in tests to avoid depending on the host system's PATH.
var lookPathFunc = exec.LookPath

// v10RequiredTools warns if requires.tools lists tools not found in PATH.
func v10RequiredTools(ast *rbast.RunbookAST) []ValidationError {
	var errs []ValidationError
	for _, tool := range ast.Metadata.Requires.Tools {
		if _, err := lookPathFunc(tool); err != nil {
			errs = append(errs, ValidationError{
				Severity: Warning,
				Message:  fmt.Sprintf("required tool %q not found in PATH", tool),
				Line:     0,
			})
		}
	}
	return errs
}

// v11NonEmptyCommands checks that executable blocks have non-empty command bodies.
func v11NonEmptyCommands(ast *rbast.RunbookAST) []ValidationError {
	var errs []ValidationError

	for _, c := range ast.Checks {
		if strings.TrimSpace(c.Command) == "" {
			errs = append(errs, ValidationError{
				Severity: Error,
				Message:  fmt.Sprintf("check %q has an empty command body", c.Name),
				Line:     c.Line,
			})
		}
	}
	for _, s := range ast.Steps {
		if strings.TrimSpace(s.Command) == "" {
			errs = append(errs, ValidationError{
				Severity: Error,
				Message:  fmt.Sprintf("step %q has an empty command body", s.Name),
				Line:     s.Line,
			})
		}
	}
	for _, r := range ast.Rollbacks {
		if strings.TrimSpace(r.Command) == "" {
			errs = append(errs, ValidationError{
				Severity: Error,
				Message:  fmt.Sprintf("rollback %q has an empty command body", r.Name),
				Line:     r.Line,
			})
		}
	}
	for _, w := range ast.Waits {
		if strings.TrimSpace(w.Command) == "" {
			errs = append(errs, ValidationError{
				Severity: Error,
				Message:  fmt.Sprintf("wait %q has an empty command body", w.Name),
				Line:     w.Line,
			})
		}
	}
	return errs
}

// v12DuplicateYAMLKeys checks the raw frontmatter for duplicate YAML keys.
func v12DuplicateYAMLKeys(ast *rbast.RunbookAST) []ValidationError {
	if ast.RawFrontmatter == "" {
		return nil
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(ast.RawFrontmatter), &node); err != nil {
		return nil // parse errors are handled elsewhere
	}

	var errs []ValidationError
	findDuplicateKeys(&node, &errs)
	return errs
}

// findDuplicateKeys recursively walks a yaml.Node tree looking for duplicate mapping keys.
func findDuplicateKeys(node *yaml.Node, errs *[]ValidationError) {
	if node == nil {
		return
	}

	if node.Kind == yaml.DocumentNode {
		for _, child := range node.Content {
			findDuplicateKeys(child, errs)
		}
		return
	}

	if node.Kind == yaml.MappingNode {
		seen := make(map[string]int)
		for i := 0; i < len(node.Content)-1; i += 2 {
			key := node.Content[i]
			if prevLine, ok := seen[key.Value]; ok {
				*errs = append(*errs, ValidationError{
					Severity: Error,
					Message:  fmt.Sprintf("duplicate YAML key %q (first defined at line %d)", key.Value, prevLine),
					Line:     key.Line,
				})
			} else {
				seen[key.Value] = key.Line
			}
			// Recurse into values
			findDuplicateKeys(node.Content[i+1], errs)
		}
		return
	}

	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			findDuplicateKeys(child, errs)
		}
	}
}

// suggestName returns the closest name from candidates using Levenshtein distance,
// if the distance is within a reasonable threshold. Returns "" if no good match.
func suggestName(target string, candidates map[string]bool) string {
	best := ""
	bestDist := len(target)/2 + 1 // threshold: half the target length

	for name := range candidates {
		d := levenshtein.ComputeDistance(target, name)
		if d < bestDist {
			bestDist = d
			best = name
		}
	}
	return best
}
