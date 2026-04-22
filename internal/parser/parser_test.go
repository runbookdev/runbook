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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return string(data)
}

func TestParse_Minimal(t *testing.T) {
	content := readTestdata(t, "minimal.runbook")
	ast, err := Parse("minimal.runbook", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ast.Metadata.Name != "Minimal Runbook" {
		t.Errorf("metadata name = %q, want %q", ast.Metadata.Name, "Minimal Runbook")
	}
	if ast.Metadata.Version != "1.0.0" {
		t.Errorf("metadata version = %q, want %q", ast.Metadata.Version, "1.0.0")
	}
	if len(ast.Checks) != 1 {
		t.Fatalf("checks count = %d, want 1", len(ast.Checks))
	}
	if ast.Checks[0].Name != "pre-check" {
		t.Errorf("check name = %q, want %q", ast.Checks[0].Name, "pre-check")
	}
	if ast.Checks[0].Command != `echo "checking"` {
		t.Errorf("check command = %q, want %q", ast.Checks[0].Command, `echo "checking"`)
	}
	if len(ast.Steps) != 1 {
		t.Fatalf("steps count = %d, want 1", len(ast.Steps))
	}
	if ast.Steps[0].Name != "do-it" {
		t.Errorf("step name = %q, want %q", ast.Steps[0].Name, "do-it")
	}
}

func TestParse_Full(t *testing.T) {
	content := readTestdata(t, "full.runbook")
	ast, err := Parse("full.runbook", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Metadata
	if ast.Metadata.Name != "Full Deploy" {
		t.Errorf("name = %q, want %q", ast.Metadata.Name, "Full Deploy")
	}
	if ast.Metadata.Timeout != "30m" {
		t.Errorf("timeout = %q, want %q", ast.Metadata.Timeout, "30m")
	}
	if len(ast.Metadata.Owners) != 2 {
		t.Errorf("owners count = %d, want 2", len(ast.Metadata.Owners))
	}
	if len(ast.Metadata.Requires.Tools) != 2 {
		t.Errorf("tools count = %d, want 2", len(ast.Metadata.Requires.Tools))
	}
	if len(ast.Metadata.Requires.Approvals) != 1 {
		t.Errorf("approvals count = %d, want 1", len(ast.Metadata.Requires.Approvals))
	}

	// Checks
	if len(ast.Checks) != 2 {
		t.Fatalf("checks count = %d, want 2", len(ast.Checks))
	}
	if ast.Checks[0].Name != "cluster-healthy" {
		t.Errorf("check[0] name = %q, want %q", ast.Checks[0].Name, "cluster-healthy")
	}
	if ast.Checks[1].Name != "no-incidents" {
		t.Errorf("check[1] name = %q, want %q", ast.Checks[1].Name, "no-incidents")
	}

	// Steps
	if len(ast.Steps) != 3 {
		t.Fatalf("steps count = %d, want 3", len(ast.Steps))
	}

	migrate := ast.Steps[0]
	if migrate.Name != "migrate-db" {
		t.Errorf("step[0] name = %q, want %q", migrate.Name, "migrate-db")
	}
	if migrate.Rollback != "rollback-db" {
		t.Errorf("step[0] rollback = %q, want %q", migrate.Rollback, "rollback-db")
	}
	if migrate.Timeout != "300s" {
		t.Errorf("step[0] timeout = %q, want %q", migrate.Timeout, "300s")
	}
	if migrate.Confirm != "production" {
		t.Errorf("step[0] confirm = %q, want %q", migrate.Confirm, "production")
	}
	if len(migrate.Env) != 2 {
		t.Errorf("step[0] env count = %d, want 2", len(migrate.Env))
	}

	deploy := ast.Steps[1]
	if deploy.Name != "deploy-app" {
		t.Errorf("step[1] name = %q, want %q", deploy.Name, "deploy-app")
	}
	if deploy.DependsOn != "migrate-db" {
		t.Errorf("step[1] depends_on = %q, want %q", deploy.DependsOn, "migrate-db")
	}
	if deploy.Timeout != "600s" {
		t.Errorf("step[1] timeout = %q, want %q", deploy.Timeout, "600s")
	}

	smoke := ast.Steps[2]
	if smoke.Name != "smoke-test" {
		t.Errorf("step[2] name = %q, want %q", smoke.Name, "smoke-test")
	}
	if smoke.DependsOn != "deploy-app" {
		t.Errorf("step[2] depends_on = %q, want %q", smoke.DependsOn, "deploy-app")
	}

	// Rollbacks
	if len(ast.Rollbacks) != 2 {
		t.Fatalf("rollbacks count = %d, want 2", len(ast.Rollbacks))
	}
	if ast.Rollbacks[0].Name != "rollback-db" {
		t.Errorf("rollback[0] name = %q, want %q", ast.Rollbacks[0].Name, "rollback-db")
	}
	if ast.Rollbacks[1].Name != "rollback-app" {
		t.Errorf("rollback[1] name = %q, want %q", ast.Rollbacks[1].Name, "rollback-app")
	}

	// Waits
	if len(ast.Waits) != 1 {
		t.Fatalf("waits count = %d, want 1", len(ast.Waits))
	}
	if ast.Waits[0].Name != "canary-watch" {
		t.Errorf("wait name = %q, want %q", ast.Waits[0].Name, "canary-watch")
	}
	if ast.Waits[0].Duration != "300s" {
		t.Errorf("wait duration = %q, want %q", ast.Waits[0].Duration, "300s")
	}
	if ast.Waits[0].AbortIf != "error_rate > 1%" {
		t.Errorf("wait abort_if = %q, want %q", ast.Waits[0].AbortIf, "error_rate > 1%")
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	content := readTestdata(t, "no_frontmatter.runbook")
	ast, err := Parse("no_frontmatter.runbook", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ast.Metadata.Name != "" {
		t.Errorf("metadata name = %q, want empty", ast.Metadata.Name)
	}
	if len(ast.Steps) != 1 {
		t.Fatalf("steps count = %d, want 1", len(ast.Steps))
	}
	if ast.Steps[0].Name != "hello" {
		t.Errorf("step name = %q, want %q", ast.Steps[0].Name, "hello")
	}
}

func TestParse_ErrorCases(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		wantErr string
	}{
		{
			name:    "unclosed block",
			file:    "unclosed_block.runbook",
			wantErr: "unclosed code block",
		},
		{
			name:    "missing name attribute",
			file:    "missing_name.runbook",
			wantErr: "missing required 'name' attribute",
		},
		{
			name:    "duplicate step name",
			file:    "duplicate_step.runbook",
			wantErr: "duplicate step name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := readTestdata(t, tt.file)
			_, err := Parse(tt.file, content)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := err.Error(); !contains(got, tt.wantErr) {
				t.Errorf("error = %q, want substring %q", got, tt.wantErr)
			}
		})
	}
}

func TestParse_InvalidFrontmatter(t *testing.T) {
	content := "---\n: invalid: yaml: [[\n---\n"
	_, err := Parse("bad.runbook", content)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if got := err.Error(); !contains(got, "invalid frontmatter YAML") {
		t.Errorf("error = %q, want substring %q", got, "invalid frontmatter YAML")
	}
}

func TestParse_UnclosedFrontmatter(t *testing.T) {
	content := "---\nname: Test\n"
	_, err := Parse("bad.runbook", content)
	if err == nil {
		t.Fatal("expected error for unclosed frontmatter")
	}
	if got := err.Error(); !contains(got, "unclosed frontmatter") {
		t.Errorf("error = %q, want substring %q", got, "unclosed frontmatter")
	}
}

func TestParse_DeployTemplate(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "templates", "deploy.runbook"))
	if err != nil {
		t.Skipf("skipping: %v", err)
	}

	ast, err := Parse("deploy.runbook", string(data))
	if err != nil {
		t.Fatalf("unexpected error parsing deploy template: %v", err)
	}

	if ast.Metadata.Name != "Deploy Service" {
		t.Errorf("name = %q, want %q", ast.Metadata.Name, "Deploy Service")
	}
	if len(ast.Checks) != 3 {
		t.Errorf("checks = %d, want 3", len(ast.Checks))
	}
	if len(ast.Steps) != 3 {
		t.Errorf("steps = %d, want 3", len(ast.Steps))
	}
	if len(ast.Rollbacks) != 2 {
		t.Errorf("rollbacks = %d, want 2", len(ast.Rollbacks))
	}
	if len(ast.Waits) != 1 {
		t.Errorf("waits = %d, want 1", len(ast.Waits))
	}
}

func TestExtractFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantFM   string
		wantBody string
		wantErr  bool
	}{
		{
			name:     "standard frontmatter",
			input:    "---\nname: Test\n---\nbody here",
			wantFM:   "name: Test",
			wantBody: "\nbody here",
		},
		{
			name:     "no frontmatter",
			input:    "# Just markdown\nsome text",
			wantFM:   "",
			wantBody: "# Just markdown\nsome text",
		},
		{
			name:    "unclosed frontmatter",
			input:   "---\nname: Test\n",
			wantErr: true,
		},
		{
			name:     "empty frontmatter",
			input:    "---\n---\nbody",
			wantFM:   "",
			wantBody: "\nbody",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := extractFrontmatter(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fm != tt.wantFM {
				t.Errorf("frontmatter = %q, want %q", fm, tt.wantFM)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestParseBlockOpening(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantType  string
		wantAttrs map[string]string
		wantOK    bool
	}{
		{
			name:      "simple check",
			line:      "```check name=\"my-check\"",
			wantType:  "check",
			wantAttrs: map[string]string{"name": "my-check"},
			wantOK:    true,
		},
		{
			name:      "step with rollback",
			line:      "```step name=\"deploy\" rollback=\"undo-deploy\"",
			wantType:  "step",
			wantAttrs: map[string]string{"name": "deploy", "rollback": "undo-deploy"},
			wantOK:    true,
		},
		{
			name:      "wait with duration",
			line:      "```wait name=\"pause\" duration=\"60s\"",
			wantType:  "wait",
			wantAttrs: map[string]string{"name": "pause", "duration": "60s"},
			wantOK:    true,
		},
		{
			name:   "plain code block",
			line:   "```bash",
			wantOK: false,
		},
		{
			name:   "closing fence",
			line:   "```",
			wantOK: false,
		},
		{
			name:   "not a code block",
			line:   "some text",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blockType, attrs, ok := parseBlockOpening(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if blockType != tt.wantType {
				t.Errorf("type = %q, want %q", blockType, tt.wantType)
			}
			for k, want := range tt.wantAttrs {
				if got := attrs[k]; got != want {
					t.Errorf("attrs[%q] = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func TestSplitBlockContent(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCommand string
		wantMeta    string
	}{
		{
			name:        "no metadata",
			input:       "echo hello",
			wantCommand: "echo hello",
			wantMeta:    "",
		},
		{
			name:        "with metadata",
			input:       "  timeout: 30s\n  confirm: production\n---\necho hello",
			wantCommand: "echo hello",
			wantMeta:    "  timeout: 30s\n  confirm: production",
		},
		{
			name:        "multiline command",
			input:       "echo line1\necho line2",
			wantCommand: "echo line1\necho line2",
			wantMeta:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, meta := splitBlockContent(tt.input)
			if cmd != tt.wantCommand {
				t.Errorf("command = %q, want %q", cmd, tt.wantCommand)
			}
			if meta != tt.wantMeta {
				t.Errorf("meta = %q, want %q", meta, tt.wantMeta)
			}
		})
	}
}

func TestParseList(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"[staging, production]", []string{"staging", "production"}},
		{"[single]", []string{"single"}},
		{"staging, production", []string{"staging", "production"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseList(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// --- File size limit ---

func TestParse_FileSizeLimit_Reject(t *testing.T) {
	// Build a string just over 1 MB.
	content := strings.Repeat("a", maxFileSizeBytes+1)
	_, err := Parse("big.runbook", content)
	if err == nil {
		t.Fatal("expected error for oversized content, got nil")
	}
	if !contains(err.Error(), "file exceeds maximum size of 1 MB") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_FileSizeLimit_ExactlyAtLimit(t *testing.T) {
	// Exactly at the limit should be accepted (no error from the size check).
	// Use a content that is exactly maxFileSizeBytes bytes of ASCII 'a'.
	// Parse will still fail on invalid frontmatter, but not on size.
	content := strings.Repeat("a", maxFileSizeBytes)
	_, err := Parse("exact.runbook", content)
	// Any error must NOT be about the size limit.
	if err != nil && contains(err.Error(), "file exceeds maximum size of 1 MB") {
		t.Errorf("content at exactly the limit should not trigger size error: %v", err)
	}
}

// TestParseFile_FileSizeLimit_Reject uses ParseFile with a real temp file that
// exceeds 1 MB, confirming the stat-before-read path works.
func TestParseFile_FileSizeLimit_Reject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oversized.runbook")

	// Generate a file programmatically: 1 MB + 1 byte of 'x'.
	data := make([]byte, maxFileSizeBytes+1)
	for i := range data {
		data[i] = 'x'
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing oversized file: %v", err)
	}

	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("expected error for oversized file, got nil")
	}
	if !contains(err.Error(), "file exceeds maximum size of 1 MB") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestParseFile_FileSizeLimit_BelowLimit confirms a file just below the limit
// is not rejected for size.
func TestParseFile_FileSizeLimit_BelowLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.runbook")

	content := "---\nname: Small\n---\n```step name=\"go\"\necho ok\n```\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	tree, err := ParseFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tree.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(tree.Steps))
	}
}

// --- UTF-8 validation ---

func TestParse_InvalidUTF8_Reject(t *testing.T) {
	// \xFF is not valid UTF-8.
	content := "---\nname: Bad\n---\n" + string([]byte{0xFF, 0xFE, 0x00})
	_, err := Parse("bad.runbook", content)
	if err == nil {
		t.Fatal("expected error for invalid UTF-8, got nil")
	}
	if !contains(err.Error(), "file contains invalid UTF-8") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_ValidUTF8_Pass(t *testing.T) {
	// Multi-byte UTF-8 (emoji and non-ASCII) should be accepted.
	content := "---\nname: Unicode ✓\n---\n```step name=\"greet\"\necho 'héllo wörld 🎉'\n```\n"
	_, err := Parse("unicode.runbook", content)
	if err != nil {
		t.Fatalf("unexpected error for valid UTF-8 content: %v", err)
	}
}

// TestParseFile_InvalidUTF8 confirms ParseFile rejects files with \xFF bytes.
func TestParseFile_InvalidUTF8(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid-utf8.runbook")

	data := []byte("---\nname: test\n---\n\xFF\xFE")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("expected error for \xFF bytes, got nil")
	}
	if !contains(err.Error(), "file contains invalid UTF-8") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Block count limit ---

func TestParse_BlockCountLimit_Reject(t *testing.T) {
	// Build a file with 1001 step blocks.
	var b strings.Builder
	b.WriteString("---\nname: Many Blocks\n---\n")
	for i := 0; i <= maxBlocks; i++ {
		b.WriteString("```step name=\"s")
		b.WriteString(strings.Repeat("x", 4)) // pad names to keep them unique
		b.WriteString(itoa(i))
		b.WriteString("\"\necho ok\n```\n")
	}

	_, err := Parse("many.runbook", b.String())
	if err == nil {
		t.Fatal("expected error for too many blocks, got nil")
	}
	if !contains(err.Error(), "file contains more than 1000 blocks") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_BlockCountLimit_AtLimit(t *testing.T) {
	// Exactly 1000 blocks should succeed (unique names required).
	var b strings.Builder
	b.WriteString("---\nname: At Limit\n---\n")
	for i := range maxBlocks {
		b.WriteString("```step name=\"s")
		b.WriteString(itoa(i))
		b.WriteString("\"\necho ok\n```\n")
	}

	_, err := Parse("atlimit.runbook", b.String())
	if err != nil {
		t.Fatalf("expected no error at block limit, got: %v", err)
	}
}

// itoa converts a non-negative int to its decimal string representation
// without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

// --- Frontmatter size limit ---

func TestParse_FrontmatterSizeLimit_Reject(t *testing.T) {
	// Build a frontmatter block that is just over 64 KB.
	bigValue := strings.Repeat("x", maxFrontmatterBytes+1)
	content := "---\ndescription: " + bigValue + "\n---\n"
	_, err := Parse("bigfm.runbook", content)
	if err == nil {
		t.Fatal("expected error for oversized frontmatter, got nil")
	}
	if !contains(err.Error(), "frontmatter exceeds maximum size of 64 KB") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_FrontmatterSizeLimit_BelowLimit(t *testing.T) {
	content := "---\nname: Small FM\ndescription: " + strings.Repeat("y", 100) + "\n---\n"
	_, err := Parse("smallfm.runbook", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- YAML strict parsing (KnownFields) ---

func TestParse_UnknownFrontmatterField_Reject(t *testing.T) {
	content := "---\nname: Test\nunknown_field: bad\n---\n"
	_, err := Parse("unknown.runbook", content)
	if err == nil {
		t.Fatal("expected error for unknown YAML field, got nil")
	}
	if !contains(err.Error(), "invalid frontmatter YAML") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_AllKnownFrontmatterFields_Pass(t *testing.T) {
	content := `---
name: Full
version: 1.0.0
description: All known fields
owners: [team-a]
environments: [staging]
requires:
  tools: [curl]
  permissions: [read]
  approvals:
    production: [lead]
timeout: 10m
trigger: manual
severity: low
---
`
	_, err := Parse("full.runbook", content)
	if err != nil {
		t.Fatalf("unexpected error with all known fields: %v", err)
	}
}

func TestParse_TypoInFieldName_Reject(t *testing.T) {
	// A transposed field name is a common typo — strict mode catches this.
	content := "---\nnmae: Oops\n---\n" //nolint:misspell
	_, err := Parse("typo.runbook", content)
	if err == nil {
		t.Fatal("expected error for typo field name, got nil")
	}
	if !contains(err.Error(), "invalid frontmatter YAML") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Line length warning ---

func TestParse_LongLine_ProducesWarning(t *testing.T) {
	longLine := strings.Repeat("a", maxLineLengthChars+1)
	content := "---\nname: test\n---\n" + longLine + "\n```step name=\"s\"\necho ok\n```\n"
	tree, err := Parse("longline.runbook", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tree.ParseWarnings) == 0 {
		t.Error("expected at least one ParseWarning for the long line")
	}
	if !contains(tree.ParseWarnings[0], "line exceeds 10,000 characters") {
		t.Errorf("unexpected warning: %q", tree.ParseWarnings[0])
	}
}

func TestParse_LongLine_DoesNotReject(t *testing.T) {
	// A long line must warn but not reject — parsing still succeeds.
	longLine := strings.Repeat("b", maxLineLengthChars+500)
	content := "---\nname: test\n---\n" + longLine + "\n```step name=\"go\"\necho ok\n```\n"
	tree, err := Parse("longline.runbook", content)
	if err != nil {
		t.Fatalf("long line should not cause a parse error: %v", err)
	}
	if len(tree.Steps) != 1 {
		t.Errorf("expected step to be parsed, got %d steps", len(tree.Steps))
	}
}

func TestParse_NormalLines_NoWarning(t *testing.T) {
	content := readTestdata(t, "minimal.runbook")
	tree, err := Parse("minimal.runbook", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tree.ParseWarnings) != 0 {
		t.Errorf("expected no warnings for normal content, got: %v", tree.ParseWarnings)
	}
}

// --- Nested backtick handling ---

// TestParse_BackticksInsideStepBody verifies that triple backticks appearing
// inside a step's command (e.g. a step that generates markdown) do NOT
// prematurely close the block. Only a line that is exactly "```" (after
// trimming whitespace) closes the block.
func TestParse_BackticksInsideStepBody(t *testing.T) {
	content := "---\nname: backtick-test\n---\n" +
		"```step name=\"gen-markdown\"\n" +
		"cat > doc.md << 'HEREDOC'\n" +
		"# Heading\n" +
		"```bash\n" + // triple-backtick line with extra text — must NOT close block
		"echo hello\n" +
		"```\n" + // triple-backtick alone — closes the step block
		"\n"

	tree, err := Parse("backtick.runbook", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tree.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(tree.Steps))
	}

	cmd := tree.Steps[0].Command
	if !contains(cmd, "```bash") {
		t.Errorf("expected command to contain '```bash', got: %q", cmd)
	}
	if !contains(cmd, "HEREDOC") {
		t.Errorf("expected command to contain heredoc marker, got: %q", cmd)
	}
}

func TestParse_BackticksWithSpacesDoNotClose(t *testing.T) {
	// A line like "  ```  " (trimmed == "```") SHOULD close the block.
	content := "---\nname: t\n---\n" +
		"```step name=\"s\"\n" +
		"echo hello\n" +
		"  ```  \n" // trimmed is "```" → closes
	_, err := Parse("trim.runbook", content)
	if err != nil {
		t.Fatalf("trimmed-backtick line should close block: %v", err)
	}
}

func TestParse_BackticksWithTextDoNotClose(t *testing.T) {
	// "```bash" inside a block body should NOT close it — the block must remain
	// open until a bare "```" line is encountered.
	content := "---\nname: t\n---\n" +
		"```step name=\"markdown-gen\"\n" +
		"```bash\n" + // has extra text after backticks — not a closer
		"echo hi\n" +
		"```\n" // bare backticks — closes the block
	tree, err := Parse("notclosed.runbook", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tree.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(tree.Steps))
	}
	if !contains(tree.Steps[0].Command, "```bash") {
		t.Errorf("expected command body to include the ```bash line, got: %q", tree.Steps[0].Command)
	}
}

// --- ParseFile happy path ---

func TestParseFile_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid.runbook")
	content := "---\nname: Valid\n---\n```step name=\"run\"\necho done\n```\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	tree, err := ParseFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tree.Metadata.Name != "Valid" {
		t.Errorf("name = %q, want %q", tree.Metadata.Name, "Valid")
	}
	if len(tree.Steps) != 1 {
		t.Errorf("steps = %d, want 1", len(tree.Steps))
	}
}

func TestParseFile_MissingFile(t *testing.T) {
	_, err := ParseFile("/nonexistent/path/runbook.runbook")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
