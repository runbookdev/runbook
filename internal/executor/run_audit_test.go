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
	"strings"
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

	// Step logs: setup (ok), migrate (ok), deploy (fail) + 2 rollback entries = 5.
	_, steps, err := al.GetRun(runs[0].ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if len(steps) != 5 {
		t.Fatalf("expected 5 step logs (3 steps + 2 rollbacks), got %d", len(steps))
	}
	if steps[2].Status != "failed" {
		t.Errorf("deploy step: expected status 'failed', got %q", steps[2].Status)
	}
	if steps[2].ExitCode != 1 {
		t.Errorf("deploy step: expected exit code 1, got %d", steps[2].ExitCode)
	}
	// Rollback entries are logged after the failed step.
	if steps[3].BlockType != "rollback" {
		t.Errorf("steps[3]: expected block_type 'rollback', got %q", steps[3].BlockType)
	}
	if steps[4].BlockType != "rollback" {
		t.Errorf("steps[4]: expected block_type 'rollback', got %q", steps[4].BlockType)
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

func TestRun_AuditSecretRedaction(t *testing.T) {
	al := openTestAuditLogger(t)
	var stdout, stderr bytes.Buffer
	const secret = "supersecret123"

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "secret_redaction.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		AuditLogger:    al,
		Vars:           map[string]string{"db_password": secret},
	})

	if result.Status != RunSuccess {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
	}

	// Two steps: "use-password" (template substitution) and "check-env" (env var).
	if len(result.StepResults) != 2 {
		t.Fatalf("expected 2 step results, got %d", len(result.StepResults))
	}

	// Step 1: in-memory stdout contains the secret via template substitution.
	// This proves the subprocess ran with the real resolved value.
	if !strings.Contains(result.StepResults[0].Stdout, secret) {
		t.Errorf("step 1 in-memory stdout should contain actual secret (template substitution), got %q",
			result.StepResults[0].Stdout)
	}

	// Step 2: in-memory stdout contains the secret via RUNBOOK_DB_PASSWORD env var.
	// This proves the secret was present in the actual subprocess environment.
	if !strings.Contains(result.StepResults[1].Stdout, secret) {
		t.Errorf("step 2 in-memory stdout should contain actual secret (subprocess env RUNBOOK_DB_PASSWORD), got %q",
			result.StepResults[1].Stdout)
	}

	// The audit log must NOT contain the raw secret anywhere.
	runs, err := al.ListRuns(10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	run, steps, err := al.GetRun(runs[0].ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	// runs.variables must not contain the raw secret.
	if v := run.Variables["db_password"]; v == secret {
		t.Errorf("runs.variables must not contain the raw secret, got %q", v)
	}

	if len(steps) != 2 {
		t.Fatalf("expected 2 step logs, got %d", len(steps))
	}
	for _, s := range steps {
		if strings.Contains(s.Command, secret) {
			t.Errorf("step_logs[%s].command must not contain the raw secret, got %q", s.StepName, s.Command)
		}
		if strings.Contains(s.Stdout, secret) {
			t.Errorf("step_logs[%s].stdout must not contain the raw secret, got %q", s.StepName, s.Stdout)
		}
		if strings.Contains(s.Stderr, secret) {
			t.Errorf("step_logs[%s].stderr must not contain the raw secret, got %q", s.StepName, s.Stderr)
		}
	}
}

func TestRun_DryRunRedactsSecrets(t *testing.T) {
	var stdout, stderr bytes.Buffer
	const secret = "supersecret123"

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "secret_redaction.runbook"),
		NonInteractive: true,
		DryRun:         true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		Vars:           map[string]string{"db_password": secret},
	})

	if result.Status != RunSuccess {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
	}

	// Neither stdout nor stderr from the dry-run output should reveal the secret.
	if strings.Contains(stderr.String(), secret) {
		t.Errorf("dry-run stderr output must not contain the raw secret %q:\n%s", secret, stderr.String())
	}
	if strings.Contains(stdout.String(), secret) {
		t.Errorf("dry-run stdout output must not contain the raw secret %q:\n%s", secret, stdout.String())
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

// TestRun_AuditRollbackCompleteness is the primary rollback-audit integration
// test. It uses a 5-step fixture where step 3 fails, triggering LIFO rollback
// of steps 1 and 2. The audit log must contain:
//   - 3 step entries (setup-infra, migrate-db, deploy-app)
//   - 2 rollback entries (undo-migrate first, then undo-infra — LIFO)
//   - run.status = "rolled_back"
//   - correct block_type, status, and non-empty output for each rollback entry
func TestRun_AuditRollbackCompleteness(t *testing.T) {
	al := openTestAuditLogger(t)
	var stdout, stderr bytes.Buffer

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "step3of5_rollback.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		AuditLogger:    al,
	})

	if result.Status != RunRolledBack {
		t.Fatalf("expected RunRolledBack, got %s (error: %s)", result.Status, result.Error)
	}

	// Exactly one run record.
	runs, err := al.ListRuns(10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Status != "rolled_back" {
		t.Errorf("run status: expected 'rolled_back', got %q", runs[0].Status)
	}

	run, steps, err := al.GetRun(runs[0].ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	_ = run

	// 3 steps (setup-infra ok, migrate-db ok, deploy-app fail) +
	// 2 rollbacks (undo-migrate, undo-infra) = 5 total.
	if len(steps) != 5 {
		names := make([]string, len(steps))
		for i, s := range steps {
			names[i] = s.BlockType + ":" + s.StepName
		}
		t.Fatalf("expected 5 step logs, got %d: %v", len(steps), names)
	}

	// ── Step entries ────────────────────────────────────────────────────────
	wantSteps := []struct {
		name   string
		btype  string
		status string
	}{
		{"setup-infra", "step", "success"},
		{"migrate-db", "step", "success"},
		{"deploy-app", "step", "failed"},
	}
	for i, w := range wantSteps {
		s := steps[i]
		if s.StepName != w.name {
			t.Errorf("steps[%d].StepName: want %q, got %q", i, w.name, s.StepName)
		}
		if s.BlockType != w.btype {
			t.Errorf("steps[%d].BlockType: want %q, got %q", i, w.btype, s.BlockType)
		}
		if s.Status != w.status {
			t.Errorf("steps[%d].Status: want %q, got %q", i, w.status, s.Status)
		}
	}
	if steps[2].ExitCode != 1 {
		t.Errorf("deploy-app exit code: want 1, got %d", steps[2].ExitCode)
	}

	// ── Rollback entries (LIFO: undo-migrate before undo-infra) ────────────
	wantRollbacks := []struct {
		name   string
		status string
	}{
		{"undo-migrate", "success"}, // pushed second, executed first
		{"undo-infra", "success"},   // pushed first, executed second
	}
	for i, w := range wantRollbacks {
		s := steps[3+i]
		if s.BlockType != "rollback" {
			t.Errorf("rollback[%d].BlockType: want 'rollback', got %q", i, s.BlockType)
		}
		if s.StepName != w.name {
			t.Errorf("rollback[%d].StepName: want %q, got %q", i, w.name, s.StepName)
		}
		if s.Status != w.status {
			t.Errorf("rollback[%d].Status: want %q, got %q", i, w.status, s.Status)
		}
		if s.Stdout == "" {
			t.Errorf("rollback[%d].Stdout: expected non-empty output captured", i)
		}
		if s.StartedAt.IsZero() || s.FinishedAt.IsZero() {
			t.Errorf("rollback[%d]: StartedAt/FinishedAt must be set", i)
		}
	}

	// ── RollbackReport fields ───────────────────────────────────────────────
	if result.RollbackReport == nil {
		t.Fatal("RollbackReport must not be nil")
	}
	rr := result.RollbackReport
	if rr.TriggerStep != "deploy-app" {
		t.Errorf("RollbackReport.TriggerStep: want 'deploy-app', got %q", rr.TriggerStep)
	}
	if rr.Succeeded != 2 {
		t.Errorf("RollbackReport.Succeeded: want 2, got %d", rr.Succeeded)
	}
	if rr.Failed != 0 {
		t.Errorf("RollbackReport.Failed: want 0, got %d", rr.Failed)
	}
	if rr.TotalDuration <= 0 {
		t.Errorf("RollbackReport.TotalDuration must be positive, got %s", rr.TotalDuration)
	}

	// Steps 4-5 (smoke-test, notify-team) must NOT appear in audit log.
	for _, s := range steps {
		if s.StepName == "smoke-test" || s.StepName == "notify-team" {
			t.Errorf("unreached step %q must not appear in audit log", s.StepName)
		}
	}
}

// TestNewRunID_UniqueAcross1000Generations verifies that 1000 consecutive
// newRunID calls produce no duplicates and all follow the expected format.
func TestNewRunID_UniqueAcross1000Generations(t *testing.T) {
	const iterations = 1000
	seen := make(map[string]struct{}, iterations)
	for i := range iterations {
		id := newRunID()
		if !strings.HasPrefix(id, "run_") {
			t.Errorf("iteration %d: expected prefix run_, got %q", i, id)
		}
		suffix := strings.TrimPrefix(id, "run_")
		if len(suffix) != 8 {
			t.Errorf("iteration %d: expected 8 hex chars, got %d in %q", i, len(suffix), id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("iteration %d: duplicate run ID %q", i, id)
		}
		seen[id] = struct{}{}
	}
}
