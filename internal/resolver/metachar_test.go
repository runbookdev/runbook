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

package resolver

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	rbast "github.com/runbookdev/runbook/internal/ast"
)

// --- Unit tests for findFirstMetachar ---

func TestFindFirstMetachar(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"1.0.0", ""},
		{"clean-value-123", ""},
		{"us-east-1", ""},
		{"1.0.0; rm -rf /", ";"},
		{"foo|bar", "|"},
		{"foo&bar", "&"},
		{"foo$bar", "$"},
		{"$(whoami)", "$("}, // $( detected before $
		{"`whoami`", "`"},
		{"foo\nbar", `\n`},
		{"foo\rbar", `\r`},
		{"foo>>bar", ">>"},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got := findFirstMetachar(tt.value)
			if got != tt.want {
				t.Errorf("findFirstMetachar(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

// TestSubshellDetectedBeforeDollar verifies $( is reported, not just $.
func TestSubshellDetectedBeforeDollar(t *testing.T) {
	got := findFirstMetachar("$(whoami)")
	if got != "$(" {
		t.Errorf("expected $( to be detected first, got %q", got)
	}
}

// --- Integration tests for Resolve with metachar scanning ---

func makeStepAST(varName, cmdTemplate string, line int) *rbast.RunbookAST {
	return &rbast.RunbookAST{
		FilePath: "deploy.runbook",
		Metadata: rbast.Metadata{Name: "deploy", Version: "1.0"},
		Steps: []rbast.StepNode{
			{Name: "deploy", Command: strings.ReplaceAll(cmdTemplate, "VAR", "{{"+varName+"}}"), Line: line},
		},
	}
}

// TestCleanValuesPassWithoutWarnings verifies that clean variable values
// produce no output on stderr and no error.
func TestCleanValuesPassWithoutWarnings(t *testing.T) {
	var stderr bytes.Buffer
	ast := makeStepAST("version", "deploy --version=VAR", 42)
	opts := Options{Stderr: &stderr, NonInteractive: true}

	if err := Resolve(ast, "production", map[string]string{"version": "1.0.0"}, "", opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr.Len() > 0 {
		t.Errorf("expected no warnings for clean value, got: %s", stderr.String())
	}
}

// TestSemicolonTriggerWarning verifies "1.0.0; rm -rf /" triggers a warning on ;.
func TestSemicolonTriggerWarning(t *testing.T) {
	var stderr bytes.Buffer
	ast := makeStepAST("version", "deploy --version=VAR", 42)
	opts := Options{Stderr: &stderr, NonInteractive: true}

	if err := Resolve(ast, "production", map[string]string{"version": "1.0.0; rm -rf /"}, "", opts); err != nil {
		t.Fatalf("unexpected error in non-interactive mode: %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "WARNING") {
		t.Errorf("expected WARNING in stderr, got: %s", out)
	}
	if !strings.Contains(out, `";"`) {
		t.Errorf("expected semicolon metachar in stderr, got: %s", out)
	}
	if !strings.Contains(out, "version") {
		t.Errorf("expected variable name in stderr, got: %s", out)
	}
	if !strings.Contains(out, "deploy.runbook:42") {
		t.Errorf("expected file:line in stderr, got: %s", out)
	}
}

// TestSubshellTriggerWarning verifies "$(whoami)" triggers a warning on $(.
func TestSubshellTriggerWarning(t *testing.T) {
	var stderr bytes.Buffer
	ast := makeStepAST("user_input", "run --user=VAR", 10)
	opts := Options{Stderr: &stderr, NonInteractive: true}

	if err := Resolve(ast, "", map[string]string{"user_input": "$(whoami)"}, "", opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "WARNING") {
		t.Errorf("expected WARNING, got: %s", out)
	}
	if !strings.Contains(out, "$(") {
		t.Errorf("expected $( in warning output, got: %s", out)
	}
}

// TestBacktickTriggerWarning verifies a backtick in a resolved value triggers a warning.
func TestBacktickTriggerWarning(t *testing.T) {
	var stderr bytes.Buffer
	ast := makeStepAST("tag", "deploy --tag=VAR", 5)
	opts := Options{Stderr: &stderr, NonInteractive: true}

	if err := Resolve(ast, "", map[string]string{"tag": "`whoami`"}, "", opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "WARNING") {
		t.Errorf("expected WARNING for backtick value, got: %s", out)
	}
}

// TestStrictModeReturnsMetacharError verifies --strict returns *MetacharError
// when a dangerous value is found (maps to exit code 3 via RunValidationError).
func TestStrictModeReturnsMetacharError(t *testing.T) {
	var stderr bytes.Buffer
	ast := makeStepAST("version", "deploy --version=VAR", 42)
	opts := Options{Stderr: &stderr, Strict: true}

	err := Resolve(ast, "production", map[string]string{"version": "1.0.0; rm -rf /"}, "", opts)
	if err == nil {
		t.Fatal("expected error in strict mode, got nil")
	}
	var metaErr *MetacharError
	if !errors.As(err, &metaErr) {
		t.Fatalf("expected *MetacharError, got %T: %v", err, err)
	}
	if len(metaErr.Warnings) == 0 {
		t.Error("expected at least one warning in MetacharError")
	}
	w := metaErr.Warnings[0]
	if w.VarName != "version" {
		t.Errorf("VarName = %q, want %q", w.VarName, "version")
	}
	if w.Metachar != ";" {
		t.Errorf("Metachar = %q, want %q", w.Metachar, ";")
	}
}

// TestStrictModeWarningIsPrinted verifies the warning is printed even in strict mode.
func TestStrictModeWarningIsPrinted(t *testing.T) {
	var stderr bytes.Buffer
	ast := makeStepAST("version", "deploy --version=VAR", 42)
	opts := Options{Stderr: &stderr, Strict: true}

	_ = Resolve(ast, "production", map[string]string{"version": "1.0.0; rm -rf /"}, "", opts)
	if !strings.Contains(stderr.String(), "WARNING") {
		t.Errorf("expected WARNING to be printed in strict mode, got: %s", stderr.String())
	}
}

// TestMultipleDangerousValuesAllReported verifies that dangerous values in
// different steps are each reported with their own warning.
func TestMultipleDangerousValuesAllReported(t *testing.T) {
	var stderr bytes.Buffer
	ast := &rbast.RunbookAST{
		FilePath: "deploy.runbook",
		Metadata: rbast.Metadata{Name: "deploy", Version: "1.0"},
		Steps: []rbast.StepNode{
			{Name: "step1", Command: "run {{dangerous1}}", Line: 10},
			{Name: "step2", Command: "run {{dangerous2}}", Line: 20},
		},
	}
	opts := Options{Stderr: &stderr, NonInteractive: true}
	cliVars := map[string]string{
		"dangerous1": "foo; bar",
		"dangerous2": "$(whoami)",
	}

	if err := Resolve(ast, "", cliVars, "", opts); err != nil {
		t.Fatalf("unexpected error in non-interactive mode: %v", err)
	}
	out := stderr.String()
	if !strings.Contains(out, "dangerous1") {
		t.Errorf("expected warning for dangerous1, got: %s", out)
	}
	if !strings.Contains(out, "dangerous2") {
		t.Errorf("expected warning for dangerous2, got: %s", out)
	}
	// Both step names should appear
	if !strings.Contains(out, "step1") {
		t.Errorf("expected step1 in output, got: %s", out)
	}
	if !strings.Contains(out, "step2") {
		t.Errorf("expected step2 in output, got: %s", out)
	}
}

// TestStrictModeMultipleWarnings verifies MetacharError contains all warnings.
func TestStrictModeMultipleWarnings(t *testing.T) {
	var stderr bytes.Buffer
	ast := &rbast.RunbookAST{
		FilePath: "deploy.runbook",
		Metadata: rbast.Metadata{Name: "deploy", Version: "1.0"},
		Steps: []rbast.StepNode{
			{Name: "step1", Command: "run {{v1}}", Line: 10},
			{Name: "step2", Command: "run {{v2}}", Line: 20},
		},
	}
	opts := Options{Stderr: &stderr, Strict: true}
	cliVars := map[string]string{"v1": "foo; bar", "v2": "$(whoami)"}

	err := Resolve(ast, "", cliVars, "", opts)
	if err == nil {
		t.Fatal("expected MetacharError, got nil")
	}
	var metaErr *MetacharError
	if !errors.As(err, &metaErr) {
		t.Fatalf("expected *MetacharError, got %T", err)
	}
	if len(metaErr.Warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d", len(metaErr.Warnings))
	}
}

// TestInteractiveModeDeclineReturnsError verifies that answering "n" to the
// prompt returns a *MetacharError.
func TestInteractiveModeDeclineReturnsError(t *testing.T) {
	var stderr bytes.Buffer
	prompt := strings.NewReader("n\n")
	ast := makeStepAST("version", "deploy --version=VAR", 1)
	opts := Options{Stderr: &stderr, PromptInput: prompt}

	err := Resolve(ast, "", map[string]string{"version": "1.0; rm -rf /"}, "", opts)
	if err == nil {
		t.Fatal("expected error when user declines prompt")
	}
	var metaErr *MetacharError
	if !errors.As(err, &metaErr) {
		t.Fatalf("expected *MetacharError, got %T: %v", err, err)
	}
}

// TestInteractiveModeAcceptContinues verifies that answering "y" to the prompt
// allows execution to proceed.
func TestInteractiveModeAcceptContinues(t *testing.T) {
	var stderr bytes.Buffer
	prompt := strings.NewReader("y\n")
	ast := makeStepAST("version", "deploy --version=VAR", 1)
	opts := Options{Stderr: &stderr, PromptInput: prompt}

	if err := Resolve(ast, "", map[string]string{"version": "1.0; rm -rf /"}, "", opts); err != nil {
		t.Fatalf("expected no error when user accepts, got: %v", err)
	}
}

// TestDryRunModeShowsWarningNoPropmt verifies dry-run prints warnings without
// prompting (PromptInput intentionally nil — would block if prompt were sent).
func TestDryRunModeShowsWarningNoPrompt(t *testing.T) {
	var stderr bytes.Buffer
	ast := makeStepAST("version", "deploy --version=VAR", 1)
	opts := Options{Stderr: &stderr, DryRun: true, PromptInput: nil}

	if err := Resolve(ast, "", map[string]string{"version": "1.0; rm -rf /"}, "", opts); err != nil {
		t.Fatalf("dry-run should not return error, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "WARNING") {
		t.Errorf("expected WARNING in dry-run output, got: %s", stderr.String())
	}
}
