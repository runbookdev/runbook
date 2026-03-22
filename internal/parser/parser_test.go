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
