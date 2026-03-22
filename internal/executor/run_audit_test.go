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
	"path/filepath"
	"testing"

	"github.com/runbookdev/runbook/internal/audit"
)

func openTestAuditLogger(t *testing.T) *audit.Logger {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test-audit.db")
	l, err := audit.Open(dbPath)
	if err != nil {
		t.Fatalf("opening test audit db: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func TestRun_AuditSuccess(t *testing.T) {
	al := openTestAuditLogger(t)
	var stdout, stderr bytes.Buffer

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "simple_success.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		AuditLogger:    al,
	})

	if result.Status != RunSuccess {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
	}

	// Verify audit records were created.
	runs, err := al.ListRuns(10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run record, got %d", len(runs))
	}

	run := runs[0]
	if run.Name != "Simple Success" {
		t.Errorf("expected name 'Simple Success', got %q", run.Name)
	}
	if run.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", run.Version)
	}
	if run.Status != "success" {
		t.Errorf("expected status 'success', got %q", run.Status)
	}
	if run.FinishedAt == nil {
		t.Error("expected non-nil finished_at")
	}
	if run.Runbook == "" {
		t.Error("expected non-empty runbook path")
	}

	// Verify step logs: 1 check + 2 steps = 3 entries.
	_, steps, err := al.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("expected 3 step logs (1 check + 2 steps), got %d", len(steps))
	}

	if steps[0].BlockType != "check" {
		t.Errorf("step[0]: expected block_type 'check', got %q", steps[0].BlockType)
	}
	if steps[0].StepName != "check:pre-check" {
		t.Errorf("step[0]: expected name 'check:pre-check', got %q", steps[0].StepName)
	}
	if steps[0].Status != "success" {
		t.Errorf("step[0]: expected status 'success', got %q", steps[0].Status)
	}

	if steps[1].BlockType != "step" {
		t.Errorf("step[1]: expected block_type 'step', got %q", steps[1].BlockType)
	}
	if steps[1].StepName != "step-one" {
		t.Errorf("step[1]: expected name 'step-one', got %q", steps[1].StepName)
	}
	if steps[2].StepName != "step-two" {
		t.Errorf("step[2]: expected name 'step-two', got %q", steps[2].StepName)
	}
}

func TestRun_AuditStepFailure(t *testing.T) {
	al := openTestAuditLogger(t)
	var stdout, stderr bytes.Buffer

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "step_fails_with_rollback.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		AuditLogger:    al,
	})

	if result.Status != RunRolledBack {
		t.Fatalf("expected rolled_back, got %s", result.Status)
	}

	runs, err := al.ListRuns(10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Status != "rolled_back" {
		t.Errorf("expected audit status 'rolled_back', got %q", runs[0].Status)
	}

	// Step logs: setup (ok), migrate (ok), deploy (fail) = 3 step entries.
	_, steps, err := al.GetRun(runs[0].ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("expected 3 step logs, got %d", len(steps))
	}
	if steps[2].Status != "failed" {
		t.Errorf("deploy step: expected status 'failed', got %q", steps[2].Status)
	}
	if steps[2].ExitCode != 1 {
		t.Errorf("deploy step: expected exit code 1, got %d", steps[2].ExitCode)
	}
}

func TestRun_AuditCheckFailure(t *testing.T) {
	al := openTestAuditLogger(t)
	var stdout, stderr bytes.Buffer

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "check_fails.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		AuditLogger:    al,
	})

	if result.Status != RunCheckFailed {
		t.Fatalf("expected check_failed, got %s", result.Status)
	}

	runs, _ := al.ListRuns(10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Status != "check_failed" {
		t.Errorf("expected audit status 'check_failed', got %q", runs[0].Status)
	}

	// 2 checks logged: always-pass (ok) + always-fail (fail).
	_, steps, _ := al.GetRun(runs[0].ID)
	if len(steps) != 2 {
		t.Fatalf("expected 2 check logs, got %d", len(steps))
	}
	if steps[0].Status != "success" {
		t.Errorf("check[0]: expected success, got %q", steps[0].Status)
	}
	if steps[1].Status != "failed" {
		t.Errorf("check[1]: expected failed, got %q", steps[1].Status)
	}
}

func TestRun_AuditDryRunNoRecord(t *testing.T) {
	al := openTestAuditLogger(t)
	var stdout, stderr bytes.Buffer

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "dry_run.runbook"),
		DryRun:         true,
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		AuditLogger:    al,
	})

	if result.Status != RunSuccess {
		t.Fatalf("expected success, got %s", result.Status)
	}

	// Dry runs should NOT create audit records.
	runs, _ := al.ListRuns(10)
	if len(runs) != 0 {
		t.Errorf("expected 0 runs for dry-run, got %d", len(runs))
	}
}

func TestRun_AuditRedactsSecrets(t *testing.T) {
	al := openTestAuditLogger(t)
	var stdout, stderr bytes.Buffer

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "simple_success.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		AuditLogger:    al,
		Vars: map[string]string{
			"region":      "us-east-1",
			"DB_PASSWORD": "supersecret",
			"api_token":   "tok-123",
		},
	})

	if result.Status != RunSuccess {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
	}

	runs, _ := al.ListRuns(10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	run, _, _ := al.GetRun(runs[0].ID)
	if run.Variables["region"] != "us-east-1" {
		t.Errorf("region should not be redacted, got %q", run.Variables["region"])
	}
	if run.Variables["DB_PASSWORD"] != "[REDACTED]" {
		t.Errorf("DB_PASSWORD should be redacted, got %q", run.Variables["DB_PASSWORD"])
	}
	if run.Variables["api_token"] != "[REDACTED]" {
		t.Errorf("api_token should be redacted, got %q", run.Variables["api_token"])
	}
}

func TestRun_AuditNilLoggerNoError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// Should work fine with nil AuditLogger.
	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "simple_success.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		AuditLogger:    nil,
	})

	if result.Status != RunSuccess {
		t.Errorf("expected success with nil logger, got %s", result.Status)
	}
}

func TestRun_AuditValidationErrorNoRecord(t *testing.T) {
	al := openTestAuditLogger(t)
	var stdout, stderr bytes.Buffer

	_ = Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "invalid_syntax.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		AuditLogger:    al,
	})

	// Validation errors happen before audit starts — no run record.
	runs, _ := al.ListRuns(10)
	if len(runs) != 0 {
		t.Errorf("expected 0 runs for validation error, got %d", len(runs))
	}
}
