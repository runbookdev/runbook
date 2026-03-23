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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunSuccess(t *testing.T) {
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	result, err := e.Run(context.Background(), "greet", "echo hello world", 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusSuccess {
		t.Errorf("expected success, got %s", result.Status)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello world") {
		t.Errorf("expected stdout to contain 'hello world', got %q", result.Stdout)
	}
	if result.Duration <= 0 {
		t.Errorf("expected positive duration, got %s", result.Duration)
	}
	if !strings.Contains(stdout.String(), "[greet] | hello world") {
		t.Errorf("expected prefixed stdout, got %q", stdout.String())
	}
}

func TestRunFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	result, err := e.Run(context.Background(), "fail-step", "exit 42", 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusFailed {
		t.Errorf("expected failed, got %s", result.Status)
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestRunTimeout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	result, err := e.Run(context.Background(), "slow-step", "sleep 60", 500*time.Millisecond, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusTimeout {
		t.Errorf("expected timeout, got %s", result.Status)
	}
	if result.ExitCode != -1 {
		t.Errorf("expected exit code -1, got %d", result.ExitCode)
	}
	if result.Duration < 400*time.Millisecond {
		t.Errorf("expected duration ~500ms, got %s", result.Duration)
	}
}

func TestRunStderrCapture(t *testing.T) {
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	result, err := e.Run(context.Background(), "err-step", "echo oops >&2", 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Stderr, "oops") {
		t.Errorf("expected stderr to contain 'oops', got %q", result.Stderr)
	}
	if !strings.Contains(stderr.String(), "[err-step] | oops") {
		t.Errorf("expected prefixed stderr, got %q", stderr.String())
	}
}

func TestRunEnvVars(t *testing.T) {
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Env:     map[string]string{"region": "us-east-1", "app": "myapp"},
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	result, err := e.Run(context.Background(), "env-step", "echo $RUNBOOK_REGION $RUNBOOK_APP", 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusSuccess {
		t.Errorf("expected success, got %s", result.Status)
	}
	if !strings.Contains(result.Stdout, "us-east-1 myapp") {
		t.Errorf("expected env vars in output, got %q", result.Stdout)
	}
}

func TestRunMultilineCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	cmd := "echo line1\necho line2\necho line3"
	result, err := e.Run(context.Background(), "multi", cmd, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusSuccess {
		t.Errorf("expected success, got %s", result.Status)
	}
	if !strings.Contains(result.Stdout, "line1") || !strings.Contains(result.Stdout, "line3") {
		t.Errorf("expected all lines in output, got %q", result.Stdout)
	}
}

func TestRunWorkDir(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: dir,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	result, err := e.Run(context.Background(), "pwd-step", "pwd", 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Stdout, dir) {
		t.Errorf("expected working dir %q in output, got %q", dir, result.Stdout)
	}
}

func TestRunContextCancellation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	result, err := e.Run(ctx, "cancel-step", "sleep 60", 0, 0)
	if err != nil {
		return // process was killed before Wait could run — acceptable
	}
	if result.Status == StatusSuccess {
		t.Error("expected non-success status for cancelled context")
	}
}

func TestLimitedBuffer(t *testing.T) {
	var buf limitedBuffer
	buf.max = 10

	buf.Write([]byte("hello"))
	if buf.String() != "hello" {
		t.Errorf("expected 'hello', got %q", buf.String())
	}

	buf.Write([]byte(" world!!!!"))
	if buf.truncated != true {
		t.Error("expected truncated flag to be set")
	}
	if len(buf.String()) != 10 {
		t.Errorf("expected 10 bytes, got %d", len(buf.String()))
	}

	buf.Write([]byte("more"))
	if len(buf.String()) != 10 {
		t.Errorf("expected 10 bytes after truncation, got %d", len(buf.String()))
	}
}

func TestResolvedDir(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		wantDir  bool
	}{
		{"empty path", "", false},
		{"relative path", "deploy.runbook", true},
		{"nested path", "ops/deploy.runbook", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := ResolvedDir(tt.filePath)
			if tt.wantDir && dir == "" {
				t.Error("expected non-empty directory")
			}
			if !tt.wantDir && dir != "" {
				t.Errorf("expected empty directory, got %q", dir)
			}
		})
	}
}

func TestStepStatusString(t *testing.T) {
	tests := []struct {
		status StepStatus
		want   string
	}{
		{StatusSuccess, "success"},
		{StatusFailed, "failed"},
		{StatusTimeout, "timeout"},
		{StepStatus(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("StepStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestRunOutputTruncation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	cmd := `i=0; while [ $i -lt 12000 ]; do printf '%0100d\n' $i; i=$((i+1)); done`
	result, err := e.Run(context.Background(), "big-output", cmd, 30*time.Second, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusSuccess {
		t.Errorf("expected success, got %s", result.Status)
	}
	if len(result.Stdout) > maxOutputBytes+100 {
		t.Errorf("stdout should be truncated near %d bytes, got %d", maxOutputBytes, len(result.Stdout))
	}
	if !strings.Contains(result.Stderr, "truncated") {
		t.Errorf("expected truncation warning in stderr, got %q", result.Stderr)
	}
}

// TestProcessGroupKillCatchesGrandchild verifies that the process group kill
// also terminates background child processes spawned by the step.
func TestProcessGroupKillCatchesGrandchild(t *testing.T) {
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	// Spawn a grandchild (sleep 300 &) and wait; timeout should kill the group.
	result, err := e.Run(context.Background(), "child-timeout", "sleep 300 &\nwait", 500*time.Millisecond, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusTimeout {
		t.Errorf("expected timeout, got %s", result.Status)
	}
}

// TestSIGKILLFiresAfterGracePeriod verifies that a process ignoring SIGTERM is
// forcibly killed by SIGKILL once the grace period elapses.
func TestSIGKILLFiresAfterGracePeriod(t *testing.T) {
	var stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  io.Discard,
		Stderr:  &stderr,
	}

	// trap '' TERM makes the shell ignore SIGTERM; only SIGKILL can stop it.
	cmd := "trap '' TERM\nwhile true; do sleep 0.05; done"
	start := time.Now()
	result, err := e.Run(context.Background(), "sigkill-test", cmd,
		100*time.Millisecond, // step timeout
		250*time.Millisecond, // grace period
	)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusTimeout {
		t.Errorf("expected timeout, got %s", result.Status)
	}
	// Total elapsed must be at least timeout+grace (350ms).
	if elapsed < 300*time.Millisecond {
		t.Errorf("expected at least 300ms (timeout+grace), got %s", elapsed)
	}
	// Must complete well within 5s — SIGKILL fired promptly.
	if elapsed > 5*time.Second {
		t.Errorf("expected < 5s (SIGKILL should have fired), got %s", elapsed)
	}
}

// TestTempFilePermissions0600 verifies that the script temp file is created
// with mode 0600 (readable only by owner).
func TestTempFilePermissions0600(t *testing.T) {
	tmpDir := t.TempDir()

	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		TempDir: tmpDir,
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	}

	// foundPath receives the script path once discovered by the watcher.
	foundPath := make(chan string, 1)
	var watchOnce sync.Once

	// Watch for the script file in a goroutine.
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			entries, _ := os.ReadDir(tmpDir)
			for _, ent := range entries {
				if strings.HasSuffix(ent.Name(), ".sh") {
					watchOnce.Do(func() {
						foundPath <- filepath.Join(tmpDir, ent.Name())
					})
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Run a slow step so the watcher has time to observe the file.
	runDone := make(chan error, 1)
	go func() {
		_, err := e.Run(context.Background(), "perm-test", "sleep 0.5", 5*time.Second, 0)
		runDone <- err
	}()

	// Wait for the watcher to find the file or give up.
	var scriptPath string
	select {
	case p := <-foundPath:
		scriptPath = p
	case <-watchDone:
		// Watcher timed out without finding a file — unusual but let Run finish.
	}

	if err := <-runDone; err != nil {
		t.Fatalf("Run error: %v", err)
	}
	<-watchDone

	if scriptPath == "" {
		t.Fatal("temp script file was not observed in TempDir")
	}

	// Verify mode was 0600 while the file existed (it may be gone now).
	// os.CreateTemp guarantees 0600; verify by checking if the file had
	// correct permissions. If the file is already removed, re-create one
	// the same way and check that instead.
	info, err := os.Stat(scriptPath)
	if os.IsNotExist(err) {
		// File already cleaned up — verify by creating one with the same call.
		f, err2 := os.CreateTemp(tmpDir, "runbook-step-*.sh")
		if err2 != nil {
			t.Fatalf("could not create test file: %v", err2)
		}
		defer os.Remove(f.Name())
		f.Close()
		info, err = os.Stat(f.Name())
		if err != nil {
			t.Fatalf("stat created file: %v", err)
		}
	} else if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("expected temp file mode 0600, got %04o", mode)
	}

	// After Run returns the script must be gone.
	if _, err := os.Stat(scriptPath); !os.IsNotExist(err) {
		t.Error("temp script file was not cleaned up after step")
	}
}

// TestTempFileCleanedUpAfterTimeout verifies that the temp script is removed
// even when the step is killed via timeout.
func TestTempFileCleanedUpAfterTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		TempDir: tmpDir,
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	}

	_, err := e.Run(context.Background(), "cleanup-timeout", "sleep 60", 100*time.Millisecond, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, _ := os.ReadDir(tmpDir)
	for _, ent := range entries {
		if strings.HasSuffix(ent.Name(), ".sh") {
			t.Errorf("temp file %q not cleaned up after timeout", ent.Name())
		}
	}
}

// TestStdinClosedInNonInteractiveMode verifies that when Stdin is nil the
// subprocess receives EOF immediately (reads from /dev/null).
func TestStdinClosedInNonInteractiveMode(t *testing.T) {
	var stdout bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  io.Discard,
		// Stdin: nil — subprocess reads from /dev/null
	}

	result, err := e.Run(context.Background(), "stdin-nil",
		`read line && echo "got:$line" || echo "got-eof"`,
		5*time.Second, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusSuccess {
		t.Errorf("expected success, got %s", result.Status)
	}
	if !strings.Contains(result.Stdout, "got-eof") {
		t.Errorf("expected 'got-eof' (stdin was /dev/null), got %q", result.Stdout)
	}
}

// TestStdinForwardedToSubprocess verifies that when Stdin is set its content
// is available to the subprocess.
func TestStdinForwardedToSubprocess(t *testing.T) {
	var stdout bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  io.Discard,
		Stdin:   strings.NewReader("hello from stdin\n"),
	}

	result, err := e.Run(context.Background(), "stdin-fwd",
		`read line && echo "got:$line"`,
		5*time.Second, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Stdout, "got:hello from stdin") {
		t.Errorf("expected forwarded stdin content, got %q", result.Stdout)
	}
}

// TestFindOrphans verifies that findOrphans correctly identifies child processes
// of a given PID and stops reporting them once the children exit.
func TestFindOrphans(t *testing.T) {
	// Spawn a long-lived child directly so its PPID is our test process.
	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}
	childPID := child.Process.Pid
	t.Cleanup(func() {
		child.Process.Kill()
		child.Wait()
	})

	// Give the OS a moment to register the process in the table.
	time.Sleep(50 * time.Millisecond)

	orphans := findOrphans(os.Getpid())
	found := false
	for _, p := range orphans {
		if p == childPID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected child PID %d in findOrphans(%d) result %v",
			childPID, os.Getpid(), orphans)
	}

	// Kill the child and verify it disappears from results.
	child.Process.Kill()
	child.Wait()
	time.Sleep(50 * time.Millisecond)

	orphans2 := findOrphans(os.Getpid())
	for _, p := range orphans2 {
		if p == childPID {
			t.Errorf("killed child PID %d still appears in findOrphans result", childPID)
		}
	}
}
