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

package parser

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/runbookdev/runbook/internal/ast"
	"gopkg.in/yaml.v3"
)

// attrPattern matches key="value" pairs on the block opening line.
var attrPattern = regexp.MustCompile(`(\w+)="([^"]*)"`)

// Parse parses a .runbook file and returns its AST.
func Parse(filePath, content string) (*ast.RunbookAST, error) {
	meta, body, err := extractFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filePath, err)
	}

	var metadata ast.Metadata
	if meta != "" {
		if err := yaml.Unmarshal([]byte(meta), &metadata); err != nil {
			return nil, fmt.Errorf("%s: invalid frontmatter YAML: %w", filePath, err)
		}
	}

	tree := &ast.RunbookAST{
		Metadata:       metadata,
		FilePath:       filePath,
		RawFrontmatter: meta,
	}

	if err := extractBlocks(filePath, body, tree); err != nil {
		return nil, err
	}

	return tree, nil
}

// extractFrontmatter splits YAML frontmatter from the body.
// Frontmatter must appear at the start of the file, delimited by "---".
func extractFrontmatter(content string) (frontmatter, body string, err error) {
	trimmed := strings.TrimLeft(content, "\n\r")
	if !strings.HasPrefix(trimmed, "---") {
		return "", content, nil
	}

	// Find the closing ---
	rest := trimmed[3:]
	// Skip the newline after opening ---
	if idx := strings.IndexByte(rest, '\n'); idx >= 0 {
		rest = rest[idx+1:]
	} else {
		return "", content, nil
	}

	// Handle empty frontmatter where closing --- is the very first line.
	if strings.HasPrefix(rest, "---") {
		body = rest[3:]
		return "", body, nil
	}

	closeIdx := strings.Index(rest, "\n---")
	if closeIdx < 0 {
		return "", "", fmt.Errorf("line 1: unclosed frontmatter: missing closing ---")
	}

	frontmatter = rest[:closeIdx]
	body = rest[closeIdx+4:] // skip \n---
	return frontmatter, body, nil
}

// extractBlocks finds all typed fenced code blocks in the body and populates the AST.
func extractBlocks(filePath, body string, tree *ast.RunbookAST) error {
	lines := strings.Split(body, "\n")
	// Calculate the line offset for the body within the full file.
	// The body starts after frontmatter, so we count frontmatter lines.
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		blockType, attrs, ok := parseBlockOpening(trimmed)
		if !ok {
			i++
			continue
		}

		openLine := i + 1 // 1-based line within body (approximate; adjusted by caller context)

		// Collect block content until closing ```
		i++
		var blockLines []string
		closed := false
		for i < len(lines) {
			if strings.TrimSpace(lines[i]) == "```" {
				closed = true
				i++
				break
			}
			blockLines = append(blockLines, lines[i])
			i++
		}

		if !closed {
			return fmt.Errorf("%s:%d: unclosed code block", filePath, openLine)
		}

		blockContent := strings.Join(blockLines, "\n")

		if err := buildNode(filePath, blockType, attrs, blockContent, openLine, tree); err != nil {
			return err
		}
	}
	return nil
}

// parseBlockOpening checks if a line opens a typed code block.
// Returns the block type, attributes map, and whether the line matched.
func parseBlockOpening(line string) (string, map[string]string, bool) {
	if !strings.HasPrefix(line, "```") {
		return "", nil, false
	}

	after := line[3:]
	if after == "" || after == "`" {
		return "", nil, false
	}

	// Extract the block type (first word)
	parts := strings.Fields(after)
	blockType := parts[0]

	// Must be a known block type
	switch blockType {
	case "check", "step", "rollback", "wait":
		// valid
	default:
		return "", nil, false
	}

	// Parse key="value" attributes
	attrs := make(map[string]string)
	matches := attrPattern.FindAllStringSubmatch(after, -1)
	for _, m := range matches {
		attrs[m[1]] = m[2]
	}

	return blockType, attrs, true
}

// buildNode creates the appropriate AST node from parsed block data.
func buildNode(filePath, blockType string, attrs map[string]string, content string, line int, tree *ast.RunbookAST) error {
	name := attrs["name"]
	if name == "" {
		return fmt.Errorf("%s:%d: %s block missing required 'name' attribute", filePath, line, blockType)
	}

	// Split content into metadata and command at --- separator.
	command, meta := splitBlockContent(content)

	switch blockType {
	case "check":
		tree.Checks = append(tree.Checks, ast.CheckNode{
			Name:    name,
			Command: command,
			Line:    line,
		})

	case "step":
		node := ast.StepNode{
			Name:      name,
			Command:   command,
			Rollback:  attrs["rollback"],
			DependsOn: attrs["depends_on"],
			Line:      line,
		}
		// Parse inline metadata (timeout, confirm, env)
		applyStepMeta(&node, meta)
		return appendStep(filePath, node, tree)

	case "rollback":
		tree.Rollbacks = append(tree.Rollbacks, ast.RollbackNode{
			Name:    name,
			Command: command,
			Line:    line,
		})

	case "wait":
		duration := attrs["duration"]
		node := ast.WaitNode{
			Name:     name,
			Duration: duration,
			Command:  command,
			Line:     line,
		}
		applyWaitMeta(&node, meta)
		tree.Waits = append(tree.Waits, node)
	}

	return nil
}

// splitBlockContent separates a block body into metadata lines and the command.
// If the body contains a --- separator, lines before it are metadata and lines after
// are the command. Otherwise the entire body is the command.
func splitBlockContent(content string) (command, meta string) {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			meta = strings.Join(lines[:i], "\n")
			command = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			return command, meta
		}
	}
	return strings.TrimSpace(content), ""
}

// applyStepMeta parses metadata lines (key: value) and applies them to a StepNode.
func applyStepMeta(node *ast.StepNode, meta string) {
	if meta == "" {
		return
	}
	for _, line := range strings.Split(meta, "\n") {
		key, value := parseMetaLine(line)
		switch key {
		case "timeout":
			node.Timeout = value
		case "confirm":
			node.Confirm = value
		case "env":
			node.Env = parseList(value)
		}
	}
}

// applyWaitMeta parses metadata lines and applies them to a WaitNode.
func applyWaitMeta(node *ast.WaitNode, meta string) {
	if meta == "" {
		return
	}
	for _, line := range strings.Split(meta, "\n") {
		key, value := parseMetaLine(line)
		switch key {
		case "duration":
			if node.Duration == "" {
				node.Duration = value
			}
		case "abort_if":
			node.AbortIf = value
		}
	}
}

// parseMetaLine splits "  key: value" into key and value.
func parseMetaLine(line string) (string, string) {
	line = strings.TrimSpace(line)
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return "", ""
	}
	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx+1:])
	return key, value
}

// parseList parses a bracketed list like "[staging, production]" into a string slice.
func parseList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// appendStep adds a StepNode to the AST, checking for duplicate names.
func appendStep(filePath string, node ast.StepNode, tree *ast.RunbookAST) error {
	for _, existing := range tree.Steps {
		if existing.Name == node.Name {
			return fmt.Errorf("%s:%d: duplicate step name %q (first defined at line %d)",
				filePath, node.Line, node.Name, existing.Line)
		}
	}
	tree.Steps = append(tree.Steps, node)
	return nil
}
