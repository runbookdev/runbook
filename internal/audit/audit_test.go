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

package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// openTestDB creates a temporary SQLite database for testing.
func openTestDB(t *testing.T) *Logger {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	l, err := Open(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func TestOpenCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "deep", "runbook.db")
	l, err := Open(dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer l.Close()
}

func TestStartRunAndGetRun(t *testing.T) {
	l := openTestDB(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	err := l.StartRun(RunRecord{
		ID:          "run-001",
		Runbook:     "deploy.runbook",
		Name:        "Deploy Service",
		Version:     "1.0.0",
		Environment: "staging",
		StartedAt:   now,
		User:        "testuser",
		Hostname:    "testhost",
		Variables:   map[string]string{"region": "us-east-1", "app": "myapp"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	run, steps, err := l.GetRun("run-001")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	if run.ID != "run-001" {
		t.Errorf("expected id run-001, got %q", run.ID)
	}
	if run.Runbook != "deploy.runbook" {
		t.Errorf("expected runbook deploy.runbook, got %q", run.Runbook)
	}
	if run.Name != "Deploy Service" {
		t.Errorf("expected name 'Deploy Service', got %q", run.Name)
	}
	if run.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %q", run.Version)
	}
	if run.Environment != "staging" {
		t.Errorf("expected environment staging, got %q", run.Environment)
	}
	if run.Status != "running" {
		t.Errorf("expected status running, got %q", run.Status)
	}
	if run.User != "testuser" {
		t.Errorf("expected user testuser, got %q", run.User)
	}
	if run.Hostname != "testhost" {
		t.Errorf("expected hostname testhost, got %q", run.Hostname)
	}
	if run.Variables["region"] != "us-east-1" {
		t.Errorf("expected region us-east-1, got %q", run.Variables["region"])
	}
	if run.FinishedAt != nil {
		t.Errorf("expected nil finished_at, got %v", run.FinishedAt)
	}
	if len(steps) != 0 {
		t.Errorf("expected 0 step logs, got %d", len(steps))
	}
}

func TestLogStepAndGetRun(t *testing.T) {
	l := openTestDB(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	_ = l.StartRun(RunRecord{
		ID:        "run-002",
		Runbook:   "deploy.runbook",
		Name:      "Deploy",
		StartedAt: now,
	})

	stepStart := now.Add(1 * time.Second)
	stepEnd := now.Add(2 * time.Second)
	err := l.LogStep(StepLog{
		RunID:      "run-002",
		StepName:   "migrate-db",
		BlockType:  "step",
		StartedAt:  stepStart,
		FinishedAt: stepEnd,
		ExitCode:   0,
		Status:     "success",
		Stdout:     "migrated 3 tables",
		Stderr:     "",
		Command:    "migrate --env=staging",
	})
	if err != nil {
		t.Fatalf("LogStep: %v", err)
	}

	err = l.LogStep(StepLog{
		RunID:      "run-002",
		StepName:   "deploy-app",
		BlockType:  "step",
		StartedAt:  stepEnd,
		FinishedAt: stepEnd.Add(5 * time.Second),
		ExitCode:   1,
		Status:     "failed",
		Stdout:     "",
		Stderr:     "deployment failed",
		Command:    "deploy --image=v2",
	})
	if err != nil {
		t.Fatalf("LogStep: %v", err)
	}

	_, steps, err := l.GetRun("run-002")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	if len(steps) != 2 {
		t.Fatalf("expected 2 step logs, got %d", len(steps))
	}

	s := steps[0]
	if s.StepName != "migrate-db" {
		t.Errorf("expected step name migrate-db, got %q", s.StepName)
	}
	if s.BlockType != "step" {
		t.Errorf("expected block type step, got %q", s.BlockType)
	}
	if s.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", s.ExitCode)
	}
	if s.Status != "success" {
		t.Errorf("expected status success, got %q", s.Status)
	}
	if s.Stdout != "migrated 3 tables" {
		t.Errorf("expected stdout, got %q", s.Stdout)
	}
	if s.Command != "migrate --env=staging" {
		t.Errorf("expected command, got %q", s.Command)
	}
	if s.RunID != "run-002" {
		t.Errorf("expected run_id run-002, got %q", s.RunID)
	}

	s2 := steps[1]
	if s2.StepName != "deploy-app" {
		t.Errorf("expected step name deploy-app, got %q", s2.StepName)
	}
	if s2.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", s2.ExitCode)
	}
}

func TestEndRun(t *testing.T) {
	l := openTestDB(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	_ = l.StartRun(RunRecord{
		ID:        "run-003",
		Runbook:   "deploy.runbook",
		Name:      "Deploy",
		StartedAt: now,
	})

	finishedAt := now.Add(30 * time.Second)
	err := l.EndRun("run-003", "success", finishedAt)
	if err != nil {
		t.Fatalf("EndRun: %v", err)
	}

	run, _, err := l.GetRun("run-003")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	if run.Status != "success" {
		t.Errorf("expected status success, got %q", run.Status)
	}
	if run.FinishedAt == nil {
		t.Fatal("expected non-nil finished_at")
	}
}

func TestListRuns(t *testing.T) {
	l := openTestDB(t)
	base := time.Now().UTC().Truncate(time.Millisecond)

	for i := range 5 {
		_ = l.StartRun(RunRecord{
			ID:        fmt.Sprintf("run-%03d", i),
			Runbook:   "deploy.runbook",
			Name:      "Deploy",
			StartedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	runs, err := l.ListRuns(3)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}

	if len(runs) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(runs))
	}
	// Most recent first.
	if runs[0].ID != "run-004" {
		t.Errorf("expected run-004 first, got %q", runs[0].ID)
	}
	if runs[2].ID != "run-002" {
		t.Errorf("expected run-002 last, got %q", runs[2].ID)
	}
}

func TestListRunsDefaultLimit(t *testing.T) {
	l := openTestDB(t)
	base := time.Now().UTC()

	for i := range 25 {
		_ = l.StartRun(RunRecord{
			ID:        fmt.Sprintf("run-%03d", i),
			Runbook:   "deploy.runbook",
			Name:      "Deploy",
			StartedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	runs, err := l.ListRuns(0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}

	if len(runs) != 20 {
		t.Errorf("expected default limit 20, got %d", len(runs))
	}
}

func TestPrune(t *testing.T) {
	l := openTestDB(t)

	old := time.Now().UTC().AddDate(0, 0, -100)
	recent := time.Now().UTC()

	_ = l.StartRun(RunRecord{
		ID: "old-run", Runbook: "a.runbook", Name: "Old", StartedAt: old,
	})
	_ = l.LogStep(StepLog{
		RunID: "old-run", StepName: "step1", BlockType: "step",
		StartedAt: old, FinishedAt: old.Add(time.Second),
		Status: "success",
	})

	_ = l.StartRun(RunRecord{
		ID: "new-run", Runbook: "a.runbook", Name: "New", StartedAt: recent,
	})
	_ = l.LogStep(StepLog{
		RunID: "new-run", StepName: "step1", BlockType: "step",
		StartedAt: recent, FinishedAt: recent.Add(time.Second),
		Status: "success",
	})

	deleted, err := l.Prune(30)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if deleted != 1 {
		t.Errorf("expected 1 deleted run, got %d", deleted)
	}

	// Old run should be gone.
	_, _, err = l.GetRun("old-run")
	if err == nil {
		t.Error("expected error for deleted run")
	}

	// New run should still exist.
	run, steps, err := l.GetRun("new-run")
	if err != nil {
		t.Fatalf("GetRun after prune: %v", err)
	}
	if run.ID != "new-run" {
		t.Error("expected new-run to survive prune")
	}
	if len(steps) != 1 {
		t.Errorf("expected 1 step log to survive, got %d", len(steps))
	}

	// Old step logs should also be deleted.
	oldSteps, err := l.queryStepLogs("old-run")
	if err != nil {
		t.Fatalf("querying old step logs: %v", err)
	}
	if len(oldSteps) != 0 {
		t.Errorf("expected 0 old step logs, got %d", len(oldSteps))
	}
}

func TestGetRunNotFound(t *testing.T) {
	l := openTestDB(t)

	_, _, err := l.GetRun("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent run")
	}
}

// --- Redaction tests ---

func TestRedactVariables(t *testing.T) {
	vars := map[string]string{
		"region":         "us-east-1",
		"DB_PASSWORD":    "supersecret",
		"api_token":      "tok-123",
		"AWS_SECRET_KEY": "AKIAIOSFODNN",
		"app_name":       "myapp",
		"credential":     "cred-abc",
		"API_KEY":        "key-xyz",
	}

	redacted := RedactVariables(vars)

	if redacted["region"] != "us-east-1" {
		t.Errorf("region should not be redacted, got %q", redacted["region"])
	}
	if redacted["app_name"] != "myapp" {
		t.Errorf("app_name should not be redacted, got %q", redacted["app_name"])
	}

	sensitive := []string{"DB_PASSWORD", "api_token", "AWS_SECRET_KEY", "credential", "API_KEY"}
	for _, key := range sensitive {
		if redacted[key] != redactedValue {
			t.Errorf("%s should be redacted, got %q", key, redacted[key])
		}
	}
}

func TestRedactVariablesNil(t *testing.T) {
	if RedactVariables(nil) != nil {
		t.Error("expected nil for nil input")
	}
}

func TestRedactVariablesEmpty(t *testing.T) {
	result := RedactVariables(map[string]string{})
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d entries", len(result))
	}
}

func TestIsSensitive(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"DB_PASSWORD", true},
		{"db_password", true},
		{"api_token", true},
		{"API_TOKEN", true},
		{"secret_value", true},
		{"aws_secret_key", true},
		{"my_credential", true},
		{"MY_KEY", true},
		{"region", false},
		{"app_name", false},
		{"version", false},
	}
	for _, tt := range tests {
		if got := isSensitive(tt.name); got != tt.want {
			t.Errorf("isSensitive(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestStartRunRedactsSecrets(t *testing.T) {
	l := openTestDB(t)

	err := l.StartRun(RunRecord{
		ID:        "run-secret",
		Runbook:   "deploy.runbook",
		Name:      "Deploy",
		StartedAt: time.Now().UTC(),
		Variables: map[string]string{
			"region":      "us-east-1",
			"DB_PASSWORD": "supersecret",
			"api_token":   "tok-123",
		},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	run, _, err := l.GetRun("run-secret")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	if run.Variables["region"] != "us-east-1" {
		t.Errorf("region should not be redacted, got %q", run.Variables["region"])
	}
	if run.Variables["DB_PASSWORD"] != redactedValue {
		t.Errorf("DB_PASSWORD should be redacted, got %q", run.Variables["DB_PASSWORD"])
	}
	if run.Variables["api_token"] != redactedValue {
		t.Errorf("api_token should be redacted, got %q", run.Variables["api_token"])
	}
}

func TestMultipleStepLogsOrdering(t *testing.T) {
	l := openTestDB(t)
	now := time.Now().UTC()

	_ = l.StartRun(RunRecord{
		ID: "run-order", Runbook: "test.runbook", Name: "Test", StartedAt: now,
	})

	names := []string{"check:pre", "step:setup", "step:deploy", "rollback:undo"}
	blockTypes := []string{"check", "step", "step", "rollback"}
	for i, name := range names {
		_ = l.LogStep(StepLog{
			RunID:      "run-order",
			StepName:   name,
			BlockType:  blockTypes[i],
			StartedAt:  now.Add(time.Duration(i) * time.Second),
			FinishedAt: now.Add(time.Duration(i+1) * time.Second),
			Status:     "success",
		})
	}

	_, steps, err := l.GetRun("run-order")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	if len(steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(steps))
	}
	for i, name := range names {
		if steps[i].StepName != name {
			t.Errorf("step[%d]: expected %q, got %q", i, name, steps[i].StepName)
		}
		if steps[i].BlockType != blockTypes[i] {
			t.Errorf("step[%d]: expected block_type %q, got %q", i, blockTypes[i], steps[i].BlockType)
		}
	}
}

func TestDefaultDBPath(t *testing.T) {
	path, err := DefaultDBPath()
	if err != nil {
		t.Fatalf("DefaultDBPath: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}
	if filepath.Base(path) != "runbook.db" {
		t.Errorf("expected runbook.db, got %q", filepath.Base(path))
	}
}

// --- Permission warning tests ---

func TestOpen_NewDBFileGetsSecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	dbPath := filepath.Join(t.TempDir(), "new.db")
	l, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("expected new DB file to have mode 0600, got %04o", got)
	}
	// No warnings should be emitted for a new file that was just secured.
	for _, w := range l.Warnings {
		if strings.Contains(w, "audit database") {
			t.Errorf("unexpected DB permission warning for new file: %s", w)
		}
	}
}

func TestOpen_ExistingDBWithWidePermsProducesWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "audit.db")

	// First open creates the file and secures it to 0600.
	l, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	l.Close()

	// Widen the permissions to simulate a misconfigured file.
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	// Second open should detect and warn about the wrong permissions.
	l2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer l2.Close()

	var found bool
	for _, w := range l2.Warnings {
		if strings.Contains(w, "audit database") && strings.Contains(w, "chmod 600") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected DB permission warning, got warnings: %v", l2.Warnings)
	}
}

func TestOpen_ExistingDirWithWidePermsProducesWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	// Create the audit directory first with too-open permissions.
	dir := filepath.Join(t.TempDir(), "audit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dbPath := filepath.Join(dir, "runbook.db")
	l, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	var found bool
	for _, w := range l.Warnings {
		if strings.Contains(w, "audit directory") && strings.Contains(w, "chmod 700") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected directory permission warning, got warnings: %v", l.Warnings)
	}
}

func TestOpen_NewDirHasNoWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	// Let Open create a brand-new directory — it should use 0700 and not warn.
	dbPath := filepath.Join(t.TempDir(), "fresh", "runbook.db")
	l, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	for _, w := range l.Warnings {
		if strings.Contains(w, "audit directory") {
			t.Errorf("unexpected dir permission warning for new dir: %s", w)
		}
	}
}

// --- Hardening tests ---

// TestLogStep_SecretRedactedInStdoutStderr verifies that secret values embedded
// in stdout and stderr are replaced with [REDACTED] before storage.
func TestLogStep_SecretRedactedInStdoutStderr(t *testing.T) {
	l := openTestDB(t)
	now := time.Now().UTC()

	_ = l.StartRun(RunRecord{
		ID: "run-redact", Runbook: "test.runbook", Name: "Test", StartedAt: now,
	})

	secrets := map[string]string{
		"DB_PASSWORD": "s3cr3tP@ss",
		"API_TOKEN":   "tok-abc123",
	}

	err := l.LogStep(StepLog{
		RunID:      "run-redact",
		StepName:   "deploy",
		BlockType:  "step",
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
		Status:     "success",
		Stdout:     "connected with password s3cr3tP@ss to db",
		Stderr:     "warning: token tok-abc123 expires soon",
		Command:    "deploy --pass=s3cr3tP@ss",
		Secrets:    secrets,
	})
	if err != nil {
		t.Fatalf("LogStep: %v", err)
	}

	_, steps, err := l.GetRun("run-redact")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}

	s := steps[0]
	if strings.Contains(s.Stdout, "s3cr3tP@ss") {
		t.Errorf("stdout still contains secret: %q", s.Stdout)
	}
	if strings.Contains(s.Stderr, "tok-abc123") {
		t.Errorf("stderr still contains secret: %q", s.Stderr)
	}
	if strings.Contains(s.Command, "s3cr3tP@ss") {
		t.Errorf("command still contains secret: %q", s.Command)
	}
	if !strings.Contains(s.Stdout, redactedValue) {
		t.Errorf("stdout missing %s: %q", redactedValue, s.Stdout)
	}
	if !strings.Contains(s.Stderr, redactedValue) {
		t.Errorf("stderr missing %s: %q", redactedValue, s.Stderr)
	}
}

// TestLogStep_OutputTruncatedAt1MB verifies that stdout/stderr exceeding 1 MB
// is cut to exactly maxOutputBytes and suffixed with the truncation marker.
func TestLogStep_OutputTruncatedAt1MB(t *testing.T) {
	l := openTestDB(t)
	now := time.Now().UTC()

	_ = l.StartRun(RunRecord{
		ID: "run-trunc", Runbook: "test.runbook", Name: "Test", StartedAt: now,
	})

	// Build a string that is clearly larger than 1 MB.
	bigOutput := strings.Repeat("x", maxOutputBytes+512)

	err := l.LogStep(StepLog{
		RunID:      "run-trunc",
		StepName:   "bigstep",
		BlockType:  "step",
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
		Status:     "success",
		Stdout:     bigOutput,
		Stderr:     bigOutput,
	})
	if err != nil {
		t.Fatalf("LogStep: %v", err)
	}

	_, steps, err := l.GetRun("run-trunc")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}

	s := steps[0]
	wantLen := maxOutputBytes + len(outputTruncatedMarker)
	if len(s.Stdout) != wantLen {
		t.Errorf("stdout: expected len %d, got %d", wantLen, len(s.Stdout))
	}
	if !strings.HasSuffix(s.Stdout, outputTruncatedMarker) {
		t.Errorf("stdout missing truncation marker, got suffix: %q", s.Stdout[len(s.Stdout)-40:])
	}
	if len(s.Stderr) != wantLen {
		t.Errorf("stderr: expected len %d, got %d", wantLen, len(s.Stderr))
	}
	if !strings.HasSuffix(s.Stderr, outputTruncatedMarker) {
		t.Errorf("stderr missing truncation marker")
	}
}

// TestLogStep_OutputUnderLimitNotTruncated confirms that outputs smaller than
// 1 MB pass through unchanged and carry no truncation marker.
func TestLogStep_OutputUnderLimitNotTruncated(t *testing.T) {
	l := openTestDB(t)
	now := time.Now().UTC()

	_ = l.StartRun(RunRecord{
		ID: "run-notrunc", Runbook: "test.runbook", Name: "Test", StartedAt: now,
	})

	small := "just a small output"
	_ = l.LogStep(StepLog{
		RunID:      "run-notrunc",
		StepName:   "s",
		BlockType:  "step",
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
		Status:     "success",
		Stdout:     small,
	})

	_, steps, _ := l.GetRun("run-notrunc")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step")
	}
	if steps[0].Stdout != small {
		t.Errorf("expected stdout %q, got %q", small, steps[0].Stdout)
	}
	if strings.Contains(steps[0].Stdout, outputTruncatedMarker) {
		t.Errorf("unexpected truncation marker in small output")
	}
}

// TestStartRun_SQLInjectionSafe verifies that a malicious runbook name
// containing SQL metacharacters does not corrupt the database.
func TestStartRun_SQLInjectionSafe(t *testing.T) {
	l := openTestDB(t)
	now := time.Now().UTC()

	maliciousName := `'; DROP TABLE runs; --`
	err := l.StartRun(RunRecord{
		ID:        "run-sqlinject",
		Runbook:   "attack.runbook",
		Name:      maliciousName,
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("StartRun with SQL injection name: %v", err)
	}

	// The runs table must still exist and the record must be retrievable.
	run, _, err := l.GetRun("run-sqlinject")
	if err != nil {
		t.Fatalf("GetRun after SQL injection attempt: %v", err)
	}
	if run.Name != maliciousName {
		t.Errorf("expected name %q stored verbatim, got %q", maliciousName, run.Name)
	}

	// Confirm the runs table still works by inserting a normal record.
	err = l.StartRun(RunRecord{
		ID:        "run-after-inject",
		Runbook:   "normal.runbook",
		Name:      "Normal Run",
		StartedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("StartRun after SQL injection: %v", err)
	}
}

// TestConcurrentWrites verifies that two goroutines can write step logs
// simultaneously without error, exercising WAL mode's concurrent-write safety.
func TestConcurrentWrites(t *testing.T) {
	// Use a single shared DB file so both writers hit the same SQLite instance.
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	l, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	now := time.Now().UTC()
	for _, id := range []string{"run-a", "run-b"} {
		if err := l.StartRun(RunRecord{
			ID: id, Runbook: "test.runbook", Name: "Test", StartedAt: now,
		}); err != nil {
			t.Fatalf("StartRun %s: %v", id, err)
		}
	}

	const writesPerGoroutine = 20
	var wg sync.WaitGroup
	errc := make(chan error, 2*writesPerGoroutine)

	for _, runID := range []string{"run-a", "run-b"} {
		runID := runID
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range writesPerGoroutine {
				err := l.LogStep(StepLog{
					RunID:      runID,
					StepName:   fmt.Sprintf("step-%d", i),
					BlockType:  "step",
					StartedAt:  now.Add(time.Duration(i) * time.Millisecond),
					FinishedAt: now.Add(time.Duration(i+1) * time.Millisecond),
					Status:     "success",
				})
				if err != nil {
					errc <- fmt.Errorf("%s step %d: %w", runID, i, err)
				}
			}
		}()
	}

	wg.Wait()
	close(errc)

	for err := range errc {
		t.Errorf("concurrent write error: %v", err)
	}

	// Both runs should have all their steps.
	for _, runID := range []string{"run-a", "run-b"} {
		_, steps, err := l.GetRun(runID)
		if err != nil {
			t.Fatalf("GetRun %s: %v", runID, err)
		}
		if len(steps) != writesPerGoroutine {
			t.Errorf("%s: expected %d steps, got %d", runID, writesPerGoroutine, len(steps))
		}
	}
}
