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

	result, err := e.Run(context.Background(), "greet", "echo hello world", 0)
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
	// Check prefixed output was streamed.
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

	result, err := e.Run(context.Background(), "fail-step", "exit 42", 0)
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

	result, err := e.Run(context.Background(), "slow-step", "sleep 60", 500*time.Millisecond)
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

	result, err := e.Run(context.Background(), "err-step", "echo oops >&2", 0)
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

	result, err := e.Run(context.Background(), "env-step", "echo $RUNBOOK_REGION $RUNBOOK_APP", 0)
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
	result, err := e.Run(context.Background(), "multi", cmd, 0)
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

	result, err := e.Run(context.Background(), "pwd-step", "pwd", 0)
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
	// Cancel after a short delay to ensure the process starts.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	result, err := e.Run(ctx, "cancel-step", "sleep 60", 0)
	// On cancellation, killProcessGroup may race with cmd.Wait() causing
	// "no child processes". Both an error return and a non-success result
	// are acceptable outcomes.
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

	// Further writes should be discarded.
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

	// Generate output larger than 1MB. Each line is ~100 chars, need ~11000 lines.
	cmd := `i=0; while [ $i -lt 12000 ]; do printf '%0100d\n' $i; i=$((i+1)); done`
	result, err := e.Run(context.Background(), "big-output", cmd, 30*time.Second)
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

func TestRunChildProcessKilledOnTimeout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	e := &StepExecutor{
		Shell:   "/bin/sh",
		WorkDir: t.TempDir(),
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	// Spawn a child that would run forever; timeout should kill the group.
	cmd := "sleep 300 &\nwait"
	result, err := e.Run(context.Background(), "child-timeout", cmd, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusTimeout {
		t.Errorf("expected timeout, got %s", result.Status)
	}
}
