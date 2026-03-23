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
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/runbookdev/runbook/internal/ast"
)

const (
	// maxFileSizeBytes is the hard limit on .runbook file size (1 MB).
	maxFileSizeBytes = 1 * 1024 * 1024

	// maxFrontmatterBytes is the hard limit on frontmatter size (64 KB).
	maxFrontmatterBytes = 64 * 1024

	// maxBlocks is the maximum number of code blocks allowed in a single file.
	maxBlocks = 1000

	// maxLineLengthChars is the threshold above which a line triggers a warning.
	maxLineLengthChars = 10_000
)

// attrPattern matches key="value" pairs on the block opening line.
var attrPattern = regexp.MustCompile(`(\w+)="([^"]*)"`)

// ParseFile reads filePath from disk, enforces the 1 MB size limit before
// reading, validates UTF-8, and delegates to Parse for content-level checks.
func ParseFile(filePath string) (*ast.RunbookAST, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filePath, err)
	}
	if info.Size() > maxFileSizeBytes {
		return nil, fmt.Errorf("%s: file exceeds maximum size of 1 MB", filePath)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filePath, err)
	}
	return Parse(filePath, string(data))
}

// Parse parses a .runbook file and returns its AST.
// It performs UTF-8 validation, frontmatter size enforcement, block count
// limiting, strict YAML parsing, and line-length warnings.
func Parse(filePath, content string) (*ast.RunbookAST, error) {
	// File size guard (catches content passed directly without going through
	// ParseFile, e.g. in tests or when content is generated in-memory).
	if len(content) > maxFileSizeBytes {
		return nil, fmt.Errorf("%s: file exceeds maximum size of 1 MB", filePath)
	}

	// UTF-8 validation: reject binary or mis-encoded files early.
	if !utf8.Valid([]byte(content)) {
		return nil, fmt.Errorf("%s: file contains invalid UTF-8", filePath)
	}

	// Scan for suspiciously long lines (warn only; do not reject).
	var warnings []string
	for lineNum, line := range strings.Split(content, "\n") {
		if len(line) > maxLineLengthChars {
			warnings = append(warnings, fmt.Sprintf(
				"%s:%d: line exceeds 10,000 characters (possible binary file)", filePath, lineNum+1,
			))
		}
	}

	meta, body, err := extractFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filePath, err)
	}

	// Frontmatter size limit: guard against YAML payloads designed to consume
	// large amounts of memory during unmarshalling.
	if len(meta) > maxFrontmatterBytes {
		return nil, fmt.Errorf("%s: frontmatter exceeds maximum size of 64 KB", filePath)
	}

	var metadata ast.Metadata
	if meta != "" {
		// KnownFields(true) rejects unknown YAML keys, preventing YAML bombs
		// delivered via maliciously crafted anchor/alias chains and catching
		// accidental typos in field names.
		dec := yaml.NewDecoder(strings.NewReader(meta))
		dec.KnownFields(true)
		if err := dec.Decode(&metadata); err != nil {
			return nil, fmt.Errorf("%s: invalid frontmatter YAML: %w", filePath, err)
		}
	}

	tree := &ast.RunbookAST{
		Metadata:       metadata,
		FilePath:       filePath,
		RawFrontmatter: meta,
		ParseWarnings:  warnings,
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

// extractBlocks finds all typed fenced code blocks in the body and populates
// the AST. It enforces the 1 000-block limit and correctly handles triple
// backticks that appear inside a block body: only a line whose trimmed content
// is exactly "```" closes the current block.
func extractBlocks(filePath, body string, tree *ast.RunbookAST) error {
	lines := strings.Split(body, "\n")
	blockCount := 0
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		blockType, attrs, ok := parseBlockOpening(trimmed)
		if !ok {
			i++
			continue
		}

		blockCount++
		if blockCount > maxBlocks {
			return fmt.Errorf("%s: file contains more than 1000 blocks (limit: 1000)", filePath)
		}

		openLine := i + 1 // 1-based line within body (approximate)

		// Collect block content until a line whose trimmed value is exactly
		// "```". Lines that contain triple backticks but also have other content
		// (e.g. "```bash" or "echo '```'") are included in the command body.
		i++
		blockLines := make([]string, 0, 16)
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
// If the body contains a "\n---\n" separator, text before it is metadata and
// text after is the command. Otherwise the entire body is the command.
func splitBlockContent(content string) (command, meta string) {
	// Fast path: find the separator without splitting into lines.
	idx := strings.Index(content, "\n---\n")
	if idx >= 0 {
		meta = content[:idx]
		command = strings.TrimSpace(content[idx+5:])
		return command, meta
	}
	// Handle trailing "---" at end of content (no trailing newline).
	if strings.HasSuffix(content, "\n---") {
		meta = content[:len(content)-4]
		return "", meta
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
		case "kill_grace":
			node.KillGrace = value
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
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", ""
	}
	return strings.TrimSpace(key), strings.TrimSpace(value)
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
