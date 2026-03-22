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

package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testdataPath(t *testing.T, name string) string {
	t.Helper()
	// Find testdata relative to the test file.
	path := filepath.Join("testdata", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("testdata file not found: %s", path)
	}
	return path
}

func runWithDefaults(t *testing.T, fixture string, mutate func(*RunOptions)) *RunResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts := RunOptions{
		FilePath:       testdataPath(t, fixture),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
	}
	if mutate != nil {
		mutate(&opts)
	}
	return Run(context.Background(), opts)
}

// --- Integration tests using .runbook fixture files ---

func TestRun_SimpleSuccess(t *testing.T) {
	result := runWithDefaults(t, "simple_success.runbook", nil)

	if result.Status != RunSuccess {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if result.Phase != PhaseComplete {
		t.Errorf("expected COMPLETE phase, got %s", result.Phase)
	}
	if len(result.StepResults) != 2 {
		t.Errorf("expected 2 step results, got %d", len(result.StepResults))
	}
	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}
	for _, sr := range result.StepResults {
		if sr.Status != StatusSuccess {
			t.Errorf("step %q: expected success, got %s", sr.StepName, sr.Status)
		}
	}
}

func TestRun_StepFailsWithRollback(t *testing.T) {
	var stderr bytes.Buffer
	result := runWithDefaults(t, "step_fails_with_rollback.runbook", func(o *RunOptions) {
		o.Stderr = &stderr
	})

	if result.Status != RunRolledBack {
		t.Errorf("expected rolled_back, got %s (error: %s)", result.Status, result.Error)
	}
	if result.Phase != PhaseRollingBack {
		t.Errorf("expected ROLLING_BACK phase, got %s", result.Phase)
	}

	// Steps: setup (ok), migrate (ok), deploy (fail) — verify should not run.
	if len(result.StepResults) != 3 {
		t.Fatalf("expected 3 step results, got %d", len(result.StepResults))
	}
	if result.StepResults[0].Status != StatusSuccess {
		t.Errorf("setup: expected success, got %s", result.StepResults[0].Status)
	}
	if result.StepResults[1].Status != StatusSuccess {
		t.Errorf("migrate: expected success, got %s", result.StepResults[1].Status)
	}
	if result.StepResults[2].Status != StatusFailed {
		t.Errorf("deploy: expected failed, got %s", result.StepResults[2].Status)
	}

	// Verify rollback happened.
	if result.RollbackReport == nil {
		t.Fatal("expected rollback report")
	}
	rr := result.RollbackReport
	if rr.Trigger != "step_failure" {
		t.Errorf("expected trigger 'step_failure', got %q", rr.Trigger)
	}
	if rr.Succeeded != 2 {
		t.Errorf("expected 2 succeeded rollbacks, got %d", rr.Succeeded)
	}
	// LIFO: undo-migrate first, then undo-setup.
	if len(rr.Entries) != 2 {
		t.Fatalf("expected 2 rollback entries, got %d", len(rr.Entries))
	}
	if rr.Entries[0].Name != "undo-migrate" {
		t.Errorf("expected first rollback 'undo-migrate', got %q", rr.Entries[0].Name)
	}
	if rr.Entries[1].Name != "undo-setup" {
		t.Errorf("expected second rollback 'undo-setup', got %q", rr.Entries[1].Name)
	}

	// Verify that "should not run" never executed.
	if strings.Contains(stderr.String(), "should not run") {
		t.Error("verify step should not have executed")
	}
}

func TestRun_CheckFails(t *testing.T) {
	var stdout bytes.Buffer
	result := runWithDefaults(t, "check_fails.runbook", func(o *RunOptions) {
		o.Stdout = &stdout
	})

	if result.Status != RunCheckFailed {
		t.Errorf("expected check_failed, got %s (error: %s)", result.Status, result.Error)
	}
	if !strings.Contains(result.Error, "always-fail") {
		t.Errorf("expected error to name the failing check, got %q", result.Error)
	}
	// No steps should have run.
	if len(result.StepResults) != 0 {
		t.Errorf("expected 0 step results, got %d", len(result.StepResults))
	}
	// "should-not-run" step output should be absent.
	if strings.Contains(stdout.String(), "this should never execute") {
		t.Error("step should not have run after check failure")
	}
}

func TestRun_ValidationError(t *testing.T) {
	result := runWithDefaults(t, "invalid_syntax.runbook", nil)

	if result.Status != RunValidationError {
		t.Errorf("expected validation_error, got %s (error: %s)", result.Status, result.Error)
	}
}

func TestRun_FileNotFound(t *testing.T) {
	var stdout, stderr bytes.Buffer
	result := Run(context.Background(), RunOptions{
		FilePath:       "/nonexistent/file.runbook",
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
	})

	if result.Status != RunInternalError {
		t.Errorf("expected internal_error, got %s", result.Status)
	}
}

func TestRun_DryRun(t *testing.T) {
	var stderr bytes.Buffer
	result := runWithDefaults(t, "dry_run.runbook", func(o *RunOptions) {
		o.DryRun = true
		o.Stderr = &stderr
	})

	if result.Status != RunSuccess {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if result.Phase != PhaseComplete {
		t.Errorf("expected COMPLETE phase, got %s", result.Phase)
	}

	output := stderr.String()
	if !strings.Contains(output, "[dry-run]") {
		t.Error("expected dry-run output")
	}
	if !strings.Contains(output, "Dry Run Demo") {
		t.Error("expected runbook name in dry-run output")
	}
	if !strings.Contains(output, "deploy") {
		t.Error("expected step names in dry-run output")
	}
	if !strings.Contains(output, "no commands were executed") {
		t.Error("expected dry-run footer")
	}
	if !strings.Contains(output, "timeout=120s") {
		t.Error("expected timeout metadata in dry-run output")
	}
	if !strings.Contains(output, "rollback=undo-deploy") {
		t.Error("expected rollback metadata in dry-run output")
	}
	if !strings.Contains(output, "confirm=production") {
		t.Error("expected confirm metadata in dry-run output")
	}
	// No steps should have run.
	if len(result.StepResults) != 0 {
		t.Errorf("expected 0 step results in dry-run, got %d", len(result.StepResults))
	}
}

func TestRun_WithVariables(t *testing.T) {
	var stdout bytes.Buffer
	result := runWithDefaults(t, "with_variables.runbook", func(o *RunOptions) {
		o.Env = "staging"
		o.Stdout = &stdout
	})

	if result.Status != RunSuccess {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if len(result.StepResults) != 1 {
		t.Fatalf("expected 1 step result, got %d", len(result.StepResults))
	}
	out := result.StepResults[0].Stdout
	if !strings.Contains(out, "name=With Variables") {
		t.Errorf("expected runbook_name resolved, got %q", out)
	}
	if !strings.Contains(out, "version=2.0.0") {
		t.Errorf("expected runbook_version resolved, got %q", out)
	}
	if !strings.Contains(out, "env=staging") {
		t.Errorf("expected env resolved, got %q", out)
	}
}

func TestRun_EnvFilter(t *testing.T) {
	result := runWithDefaults(t, "env_filter.runbook", func(o *RunOptions) {
		o.Env = "staging"
	})

	if result.Status != RunSuccess {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	// In staging: always-run, staging-only, also-always (production-only filtered out).
	if len(result.StepResults) != 3 {
		t.Fatalf("expected 3 step results, got %d", len(result.StepResults))
	}
	names := make([]string, len(result.StepResults))
	for i, sr := range result.StepResults {
		names[i] = sr.StepName
	}
	expected := []string{"always-run", "staging-only", "also-always"}
	for i, want := range expected {
		if names[i] != want {
			t.Errorf("step[%d]: expected %q, got %q", i, want, names[i])
		}
	}
}

func TestRun_ConfirmNonInteractive(t *testing.T) {
	result := runWithDefaults(t, "with_confirm.runbook", func(o *RunOptions) {
		o.Env = "production"
		o.NonInteractive = true
	})

	if result.Status != RunSuccess {
		t.Errorf("expected success (auto-confirm), got %s (error: %s)", result.Status, result.Error)
	}
	// All 3 steps should run (confirm is auto-approved in non-interactive).
	if len(result.StepResults) != 3 {
		t.Fatalf("expected 3 step results, got %d", len(result.StepResults))
	}
}

func TestRun_ConfirmYes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	input := strings.NewReader("y\n")

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "with_confirm.runbook"),
		Env:            "production",
		NonInteractive: false,
		Stdout:         &stdout,
		Stderr:         &stderr,
		PromptInput:    input,
	})

	if result.Status != RunSuccess {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if len(result.StepResults) != 3 {
		t.Fatalf("expected 3 step results, got %d", len(result.StepResults))
	}
}

func TestRun_ConfirmSkip(t *testing.T) {
	var stdout, stderr bytes.Buffer
	input := strings.NewReader("s\n")

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "with_confirm.runbook"),
		Env:            "production",
		NonInteractive: false,
		Stdout:         &stdout,
		Stderr:         &stderr,
		PromptInput:    input,
	})

	if result.Status != RunSuccess {
		t.Errorf("expected success (skip + continue), got %s (error: %s)", result.Status, result.Error)
	}
	// Steps: safe-step, (dangerous-step skipped), final-step = 2 results.
	if len(result.StepResults) != 2 {
		t.Fatalf("expected 2 step results (1 skipped), got %d", len(result.StepResults))
	}
	if result.StepResults[0].StepName != "safe-step" {
		t.Errorf("expected safe-step first, got %q", result.StepResults[0].StepName)
	}
	if result.StepResults[1].StepName != "final-step" {
		t.Errorf("expected final-step second, got %q", result.StepResults[1].StepName)
	}
}

func TestRun_ConfirmAbort(t *testing.T) {
	var stdout, stderr bytes.Buffer
	input := strings.NewReader("a\n")

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "with_confirm.runbook"),
		Env:            "production",
		NonInteractive: false,
		Stdout:         &stdout,
		Stderr:         &stderr,
		PromptInput:    input,
	})

	if result.Status != RunAborted {
		t.Errorf("expected aborted, got %s (error: %s)", result.Status, result.Error)
	}
}

func TestRun_ConfirmNo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	input := strings.NewReader("n\n")

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "with_confirm.runbook"),
		Env:            "production",
		NonInteractive: false,
		Stdout:         &stdout,
		Stderr:         &stderr,
		PromptInput:    input,
	})

	// "No" triggers rollback of completed steps.
	if result.Status != RunStepFailed && result.Status != RunRolledBack {
		t.Errorf("expected step_failed or rolled_back, got %s (error: %s)", result.Status, result.Error)
	}
}

func TestRun_ConfirmNotTriggeredForWrongEnv(t *testing.T) {
	result := runWithDefaults(t, "with_confirm.runbook", func(o *RunOptions) {
		o.Env = "staging"
		o.NonInteractive = false
		// No PromptInput — if confirm fires it would block/fail.
	})

	if result.Status != RunSuccess {
		t.Errorf("expected success (confirm not triggered for staging), got %s (error: %s)", result.Status, result.Error)
	}
	if len(result.StepResults) != 3 {
		t.Errorf("expected 3 step results, got %d", len(result.StepResults))
	}
}

func TestRun_PerStepTimeout(t *testing.T) {
	result := runWithDefaults(t, "with_timeout.runbook", nil)

	if result.Status != RunRolledBack && result.Status != RunStepFailed {
		t.Errorf("expected failure from timeout, got %s (error: %s)", result.Status, result.Error)
	}
	// fast-step succeeds, slow-step times out.
	if len(result.StepResults) < 2 {
		t.Fatalf("expected at least 2 step results, got %d", len(result.StepResults))
	}
	if result.StepResults[0].Status != StatusSuccess {
		t.Errorf("fast-step: expected success, got %s", result.StepResults[0].Status)
	}
	if result.StepResults[1].Status != StatusTimeout {
		t.Errorf("slow-step: expected timeout, got %s", result.StepResults[1].Status)
	}
}

func TestRun_GlobalTimeout(t *testing.T) {
	result := runWithDefaults(t, "global_timeout.runbook", nil)

	// Global 500ms timeout should kill the slow step.
	if result.Status == RunSuccess {
		t.Error("expected failure due to global timeout")
	}
	// At least fast-step should have run.
	if len(result.StepResults) < 1 {
		t.Fatal("expected at least 1 step result")
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a brief delay.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	var stdout, stderr bytes.Buffer
	result := Run(ctx, RunOptions{
		FilePath:       testdataPath(t, "global_timeout.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
	})

	if result.Status == RunSuccess {
		t.Error("expected non-success due to context cancellation")
	}
}

// --- Unit tests for helpers ---

func TestConfirmMatches(t *testing.T) {
	tests := []struct {
		confirm string
		env     string
		want    bool
	}{
		{"production", "production", true},
		{"production", "staging", false},
		{"Production", "production", true},
		{"always", "staging", true},
		{"always", "production", true},
		{"", "production", false},
	}
	for _, tt := range tests {
		if got := confirmMatches(tt.confirm, tt.env); got != tt.want {
			t.Errorf("confirmMatches(%q, %q) = %v, want %v", tt.confirm, tt.env, got, tt.want)
		}
	}
}

func TestParseConfirmAction(t *testing.T) {
	tests := []struct {
		input string
		want  ConfirmAction
	}{
		{"y", ConfirmYes},
		{"Y", ConfirmYes},
		{"yes", ConfirmYes},
		{"n", ConfirmNo},
		{"N", ConfirmNo},
		{"no", ConfirmNo},
		{"s", ConfirmSkip},
		{"skip", ConfirmSkip},
		{"a", ConfirmAbort},
		{"abort", ConfirmAbort},
		{"", ConfirmNo},
		{"x", ConfirmNo},
	}
	for _, tt := range tests {
		if got := parseConfirmAction(tt.input); got != tt.want {
			t.Errorf("parseConfirmAction(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestRunStatus_ExitCode(t *testing.T) {
	tests := []struct {
		status RunStatus
		code   int
	}{
		{RunSuccess, 0},
		{RunStepFailed, 1},
		{RunRolledBack, 2},
		{RunValidationError, 3},
		{RunCheckFailed, 4},
		{RunAborted, 10},
		{RunInternalError, 20},
	}
	for _, tt := range tests {
		if got := tt.status.ExitCode(); got != tt.code {
			t.Errorf("%s.ExitCode() = %d, want %d", tt.status, got, tt.code)
		}
	}
}

func TestRunStatus_String(t *testing.T) {
	tests := []struct {
		status RunStatus
		want   string
	}{
		{RunSuccess, "success"},
		{RunStepFailed, "step_failed"},
		{RunRolledBack, "rolled_back"},
		{RunValidationError, "validation_error"},
		{RunCheckFailed, "check_failed"},
		{RunAborted, "aborted"},
		{RunInternalError, "internal_error"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("RunStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestIndentCommand(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"echo hello", "echo hello"},
		{"echo line1\necho line2", "echo line1"},
		{strings.Repeat("x", 100), strings.Repeat("x", 77) + "..."},
	}
	for _, tt := range tests {
		if got := indentCommand(tt.cmd); got != tt.want {
			t.Errorf("indentCommand(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}
