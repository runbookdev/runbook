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
	"strings"
	"testing"
)

func newTestExecutor(t *testing.T) (*StepExecutor, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}
	return e, &stdout, &stderr
}

func TestRollbackEngine_Step3Of5Fails(t *testing.T) {
	exec, _, _ := newTestExecutor(t)
	var rollbackLog bytes.Buffer

	engine := NewRollbackEngine(exec)
	engine.Output = &rollbackLog

	// Simulate: steps 1-5, step 3 fails.
	// Steps 1 and 2 succeed and have rollback blocks.
	steps := []struct {
		name     string
		command  string
		rollback string
		rbCmd    string
	}{
		{"setup-db", "echo setup-db", "undo-setup-db", "echo rolling back setup-db"},
		{"migrate", "echo migrate", "undo-migrate", "echo rolling back migrate"},
		{"deploy", "exit 1", "", ""},      // fails — no further steps run
		{"verify", "echo verify", "", ""}, // never reached
		{"notify", "echo notify", "", ""}, // never reached
	}

	ctx := context.Background()
	var failedStep string

	for _, s := range steps {
		result, err := exec.Run(ctx, s.name, s.command, 0)
		if err != nil {
			t.Fatalf("unexpected error running %q: %v", s.name, err)
		}

		if result.Status == StatusSuccess && s.rollback != "" {
			engine.Push(s.rollback, s.rbCmd)
		}

		if result.Status != StatusSuccess {
			failedStep = s.name
			break
		}
	}

	if failedStep != "deploy" {
		t.Fatalf("expected deploy to fail, got %q", failedStep)
	}
	if engine.Len() != 2 {
		t.Fatalf("expected 2 rollback blocks on stack, got %d", engine.Len())
	}

	// Execute rollback.
	report := engine.Execute(ctx, "step_failure")

	if report.Trigger != "step_failure" {
		t.Errorf("expected trigger 'step_failure', got %q", report.Trigger)
	}
	if report.Succeeded != 2 {
		t.Errorf("expected 2 succeeded rollbacks, got %d", report.Succeeded)
	}
	if report.Failed != 0 {
		t.Errorf("expected 0 failed rollbacks, got %d", report.Failed)
	}
	if len(report.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(report.Entries))
	}

	// Verify LIFO order: migrate was pushed last, so it executes first.
	if report.Entries[0].Name != "undo-migrate" {
		t.Errorf("expected first rollback to be 'undo-migrate', got %q", report.Entries[0].Name)
	}
	if report.Entries[1].Name != "undo-setup-db" {
		t.Errorf("expected second rollback to be 'undo-setup-db', got %q", report.Entries[1].Name)
	}

	// Verify rollback log output.
	log := rollbackLog.String()
	if !strings.Contains(log, "starting rollback (2 blocks") {
		t.Errorf("expected rollback start message, got %q", log)
	}
	if !strings.Contains(log, "complete: 2 succeeded, 0 failed") {
		t.Errorf("expected rollback complete message, got %q", log)
	}

	// Verify stack is cleared after execution.
	if engine.Len() != 0 {
		t.Errorf("expected empty stack after rollback, got %d", engine.Len())
	}
}

func TestRollbackEngine_RollbackItSelfFails(t *testing.T) {
	exec, _, _ := newTestExecutor(t)
	var rollbackLog bytes.Buffer

	engine := NewRollbackEngine(exec)
	engine.Output = &rollbackLog

	ctx := context.Background()

	// Push three rollback blocks: middle one fails.
	engine.Push("rb-first", "echo first-rollback")
	engine.Push("rb-middle", "exit 99")
	engine.Push("rb-last", "echo last-rollback")

	report := engine.Execute(ctx, "step_failure")

	if report.Succeeded != 2 {
		t.Errorf("expected 2 succeeded, got %d", report.Succeeded)
	}
	if report.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", report.Failed)
	}
	if len(report.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(report.Entries))
	}

	// LIFO: rb-last first, then rb-middle (fails), then rb-first.
	if report.Entries[0].Name != "rb-last" || report.Entries[0].Status != RollbackSuccess {
		t.Errorf("entry[0]: expected rb-last/success, got %q/%s", report.Entries[0].Name, report.Entries[0].Status)
	}
	if report.Entries[1].Name != "rb-middle" || report.Entries[1].Status != RollbackFailed {
		t.Errorf("entry[1]: expected rb-middle/failed, got %q/%s", report.Entries[1].Name, report.Entries[1].Status)
	}
	if report.Entries[1].Error == "" {
		t.Error("entry[1]: expected non-empty error message")
	}
	if report.Entries[2].Name != "rb-first" || report.Entries[2].Status != RollbackSuccess {
		t.Errorf("entry[2]: expected rb-first/success, got %q/%s", report.Entries[2].Name, report.Entries[2].Status)
	}

	// Verify the failure was logged but execution continued.
	log := rollbackLog.String()
	if !strings.Contains(log, `"rb-middle" failed`) {
		t.Errorf("expected failure log for rb-middle, got %q", log)
	}
	if !strings.Contains(log, `"rb-first" succeeded`) {
		t.Errorf("expected success log for rb-first after middle failure, got %q", log)
	}
	if !strings.Contains(log, "complete: 2 succeeded, 1 failed") {
		t.Errorf("expected summary with 1 failure, got %q", log)
	}
}

func TestRollbackEngine_UserAbortTriggersRollback(t *testing.T) {
	exec, _, _ := newTestExecutor(t)
	var rollbackLog bytes.Buffer

	engine := NewRollbackEngine(exec)
	engine.Output = &rollbackLog

	ctx := context.Background()

	// Simulate: two steps succeed, then user aborts (Ctrl+C).
	engine.Push("rb-step1", "echo rolling back step1")
	engine.Push("rb-step2", "echo rolling back step2")

	report := engine.Execute(ctx, "user_abort")

	if report.Trigger != "user_abort" {
		t.Errorf("expected trigger 'user_abort', got %q", report.Trigger)
	}
	if report.Succeeded != 2 {
		t.Errorf("expected 2 succeeded, got %d", report.Succeeded)
	}
	if report.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", report.Failed)
	}

	// LIFO: step2 first, then step1.
	if report.Entries[0].Name != "rb-step2" {
		t.Errorf("expected rb-step2 first, got %q", report.Entries[0].Name)
	}
	if report.Entries[1].Name != "rb-step1" {
		t.Errorf("expected rb-step1 second, got %q", report.Entries[1].Name)
	}

	log := rollbackLog.String()
	if !strings.Contains(log, "trigger: user_abort") {
		t.Errorf("expected user_abort trigger in log, got %q", log)
	}
}

func TestRollbackEngine_EmptyStack(t *testing.T) {
	exec, _, _ := newTestExecutor(t)
	var rollbackLog bytes.Buffer

	engine := NewRollbackEngine(exec)
	engine.Output = &rollbackLog

	report := engine.Execute(context.Background(), "step_failure")

	if len(report.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(report.Entries))
	}
	if report.Succeeded != 0 || report.Failed != 0 {
		t.Errorf("expected 0/0, got %d/%d", report.Succeeded, report.Failed)
	}
	if !strings.Contains(rollbackLog.String(), "no rollback blocks") {
		t.Errorf("expected empty-stack message, got %q", rollbackLog.String())
	}
}

func TestRollbackEngine_PushAndLen(t *testing.T) {
	exec, _, _ := newTestExecutor(t)
	engine := NewRollbackEngine(exec)

	if engine.Len() != 0 {
		t.Errorf("expected 0, got %d", engine.Len())
	}

	engine.Push("rb1", "echo 1")
	engine.Push("rb2", "echo 2")
	engine.Push("rb3", "echo 3")

	if engine.Len() != 3 {
		t.Errorf("expected 3, got %d", engine.Len())
	}
}

func TestRollbackEngine_AllRollbacksFail(t *testing.T) {
	exec, _, _ := newTestExecutor(t)
	var rollbackLog bytes.Buffer

	engine := NewRollbackEngine(exec)
	engine.Output = &rollbackLog

	engine.Push("rb1", "exit 1")
	engine.Push("rb2", "exit 2")

	report := engine.Execute(context.Background(), "step_failure")

	if report.Succeeded != 0 {
		t.Errorf("expected 0 succeeded, got %d", report.Succeeded)
	}
	if report.Failed != 2 {
		t.Errorf("expected 2 failed, got %d", report.Failed)
	}

	// Both should still have entries despite both failing.
	if len(report.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(report.Entries))
	}
	for _, e := range report.Entries {
		if e.Status != RollbackFailed {
			t.Errorf("expected failed status for %q, got %s", e.Name, e.Status)
		}
	}
}

func TestRollbackEngine_RollbackOutputStreamed(t *testing.T) {
	exec, stdout, _ := newTestExecutor(t)
	var rollbackLog bytes.Buffer

	engine := NewRollbackEngine(exec)
	engine.Output = &rollbackLog

	engine.Push("rb-verify", "echo rollback-output-check")

	engine.Execute(context.Background(), "step_failure")

	// The rollback command's stdout should be streamed with a prefix.
	if !strings.Contains(stdout.String(), "[rollback:rb-verify] | rollback-output-check") {
		t.Errorf("expected prefixed rollback output, got %q", stdout.String())
	}
}
