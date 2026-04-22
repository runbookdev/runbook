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

// Fence-line literals used when recognizing a block opening.
const (
	// blockFence is the triple-backtick sequence that opens or closes a block.
	blockFence = "```"
	// frontmatterDelim is the YAML frontmatter delimiter.
	frontmatterDelim = "---"
	// metaSeparator is the "\n---\n" sequence that splits block metadata from
	// the command body inside a fenced block.
	metaSeparator = "\n---\n"
)

// Attribute keys recognized on the opening fence line of a block
// (e.g. ```step name="deploy" rollback="revert").
const (
	// attrKeyName is required on every block.
	attrKeyName = "name"
	// attrKeyRollback names the rollback handler for a step.
	attrKeyRollback = "rollback"
	// attrKeyDependsOn lists parent step names for DAG scheduling.
	attrKeyDependsOn = "depends_on"
	// attrKeyDuration is recognized on wait blocks when specified inline.
	attrKeyDuration = "duration"
)

// Metadata keys recognized in the "key: value" header of a block body,
// separated from the command by metaSeparator.
const (
	// metaKeyTimeout sets a per-step execution deadline.
	metaKeyTimeout = "timeout"
	// metaKeyKillGrace sets the SIGTERM-to-SIGKILL grace period.
	metaKeyKillGrace = "kill_grace"
	// metaKeyConfirm gates execution behind an interactive prompt.
	metaKeyConfirm = "confirm"
	// metaKeyEnv restricts execution to the listed environments.
	metaKeyEnv = "env"
	// metaKeyDuration is the wait block's duration, when set in the body header.
	metaKeyDuration = "duration"
	// metaKeyAbortIf short-circuits a wait block when the predicate is true.
	metaKeyAbortIf = "abort_if"
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
// Frontmatter must appear at the start of the file, delimited by frontmatterDelim.
func extractFrontmatter(content string) (frontmatter, body string, err error) {
	trimmed := strings.TrimLeft(content, "\n\r")
	if !strings.HasPrefix(trimmed, frontmatterDelim) {
		return "", content, nil
	}

	// Find the closing frontmatterDelim.
	rest := trimmed[len(frontmatterDelim):]

	// Skip the newline after the opening delimiter.
	if idx := strings.IndexByte(rest, '\n'); idx >= 0 {
		rest = rest[idx+1:]
	} else {
		return "", content, nil
	}

	// Handle empty frontmatter where the closing delimiter is the very first line.
	if strings.HasPrefix(rest, frontmatterDelim) {
		body = rest[len(frontmatterDelim):]
		return "", body, nil
	}

	before, after, ok := strings.Cut(rest, "\n"+frontmatterDelim)
	if !ok {
		return "", "", fmt.Errorf("line 1: unclosed frontmatter: missing closing ---")
	}

	frontmatter = before
	body = after
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
		// the fence. Lines that contain triple backticks but also have other
		// content (e.g. "```bash" or "echo '```'") are included in the body.
		i++
		blockLines := make([]string, 0, 16)
		closed := false
		for i < len(lines) {
			if strings.TrimSpace(lines[i]) == blockFence {
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
	if !strings.HasPrefix(line, blockFence) {
		return "", nil, false
	}

	after := line[len(blockFence):]
	if after == "" || after == "`" {
		return "", nil, false
	}

	// Extract the block type (first word).
	parts := strings.Fields(after)
	blockType := parts[0]

	// Must be a known block type.
	switch blockType {
	case ast.BlockTypeCheck, ast.BlockTypeStep, ast.BlockTypeRollback, ast.BlockTypeWait:
		// valid
	default:
		return "", nil, false
	}

	// Parse key="value" attributes.
	attrs := make(map[string]string)
	matches := attrPattern.FindAllStringSubmatch(after, -1)
	for _, m := range matches {
		attrs[m[1]] = m[2]
	}

	return blockType, attrs, true
}

// buildNode creates the appropriate AST node from parsed block data.
func buildNode(filePath, blockType string, attrs map[string]string, content string, line int, tree *ast.RunbookAST) error {
	name := attrs[attrKeyName]
	if name == "" {
		return fmt.Errorf("%s:%d: %s block missing required 'name' attribute", filePath, line, blockType)
	}

	// Split content into metadata and command at the metadata separator.
	command, meta := splitBlockContent(content)

	switch blockType {
	case ast.BlockTypeCheck:
		tree.Checks = append(tree.Checks, ast.CheckNode{
			Name:    name,
			Command: command,
			Line:    line,
		})

	case ast.BlockTypeStep:
		node := ast.StepNode{
			Name:      name,
			Command:   command,
			Rollback:  attrs[attrKeyRollback],
			DependsOn: attrs[attrKeyDependsOn],
			Line:      line,
		}
		applyStepMeta(&node, meta)
		return appendStep(filePath, node, tree)

	case ast.BlockTypeRollback:
		tree.Rollbacks = append(tree.Rollbacks, ast.RollbackNode{
			Name:    name,
			Command: command,
			Line:    line,
		})

	case ast.BlockTypeWait:
		node := ast.WaitNode{
			Name:     name,
			Duration: attrs[attrKeyDuration],
			Command:  command,
			Line:     line,
		}
		applyWaitMeta(&node, meta)
		tree.Waits = append(tree.Waits, node)
	}

	return nil
}

// splitBlockContent separates a block body into metadata lines and the command.
// If the body contains a metaSeparator, text before it is metadata and text
// after is the command. Otherwise the entire body is the command.
func splitBlockContent(content string) (command, meta string) {
	// Fast path: find the separator without splitting into lines.
	before, after, ok := strings.Cut(content, metaSeparator)
	if ok {
		meta = before
		command = strings.TrimSpace(after)
		return command, meta
	}

	// Handle a trailing frontmatterDelim at end of content (no trailing newline).
	trailingDelim := "\n" + frontmatterDelim
	if strings.HasSuffix(content, trailingDelim) {
		meta = content[:len(content)-len(trailingDelim)]
		return "", meta
	}
	return strings.TrimSpace(content), ""
}

// applyStepMeta parses metadata lines (key: value) and applies them to a StepNode.
func applyStepMeta(node *ast.StepNode, meta string) {
	if meta == "" {
		return
	}
	for line := range strings.SplitSeq(meta, "\n") {
		key, value := parseMetaLine(line)
		switch key {
		case metaKeyTimeout:
			node.Timeout = value
		case metaKeyKillGrace:
			node.KillGrace = value
		case metaKeyConfirm:
			node.Confirm = value
		case metaKeyEnv:
			node.Env = parseList(value)
		}
	}
}

// applyWaitMeta parses metadata lines and applies them to a WaitNode.
func applyWaitMeta(node *ast.WaitNode, meta string) {
	if meta == "" {
		return
	}
	for line := range strings.SplitSeq(meta, "\n") {
		key, value := parseMetaLine(line)
		switch key {
		case metaKeyDuration:
			if node.Duration == "" {
				node.Duration = value
			}
		case metaKeyAbortIf:
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
