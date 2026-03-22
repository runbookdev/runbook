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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// maxOutputBytes is the maximum captured output size per stream (1 MB).
const maxOutputBytes = 1 << 20

// defaultGracePeriod is how long to wait after SIGTERM before sending SIGKILL.
const defaultGracePeriod = 10 * time.Second

// StepStatus represents the outcome of a step execution.
type StepStatus int

const (
	StatusSuccess StepStatus = iota
	StatusFailed
	StatusTimeout
)

func (s StepStatus) String() string {
	switch s {
	case StatusSuccess:
		return "success"
	case StatusFailed:
		return "failed"
	case StatusTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// StepResult holds the outcome of executing a single step.
type StepResult struct {
	StepName string
	Status   StepStatus
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// StepExecutor runs individual steps as subprocesses.
type StepExecutor struct {
	// Shell is the shell binary to invoke (default: /bin/sh).
	Shell string
	// WorkDir is the working directory for command execution.
	WorkDir string
	// Env is additional environment variables injected as RUNBOOK_* vars.
	Env map[string]string
	// Stdout is the writer for real-time stdout streaming (default: os.Stdout).
	Stdout io.Writer
	// Stderr is the writer for real-time stderr streaming (default: os.Stderr).
	Stderr io.Writer
}

// Run executes a single step command with the given timeout.
// If timeout is zero, the step runs without a deadline.
func (e *StepExecutor) Run(ctx context.Context, stepName, command string, timeout time.Duration) (*StepResult, error) {
	shell := e.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	stdout := e.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := e.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	// Write the command to a temp file so it survives shell quoting.
	tmpFile, err := os.CreateTemp("", "runbook-step-*.sh")
	if err != nil {
		return nil, fmt.Errorf("creating temp script: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(command); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("writing temp script: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("closing temp script: %w", err)
	}

	// Build the command using a plain exec.Command (no CommandContext).
	// We manage timeouts ourselves via process group signals.
	cmd := exec.Command(shell, tmpPath)
	cmd.Dir = e.WorkDir

	// Set up process group so we can kill child processes on timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Build environment: inherit current env + RUNBOOK_* vars.
	cmd.Env = os.Environ()
	for k, v := range e.Env {
		cmd.Env = append(cmd.Env, "RUNBOOK_"+strings.ToUpper(k)+"="+v)
	}

	// Set up output capture with size limits.
	var stdoutBuf, stderrBuf limitedBuffer
	stdoutBuf.max = maxOutputBytes
	stderrBuf.max = maxOutputBytes

	// Create prefixed writers that stream in real time.
	stdoutPrefixer := newPrefixWriter(stepName, stdout)
	stderrPrefixer := newPrefixWriter(stepName, stderr)

	// Use pipes for line-by-line prefixed output.
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("creating stderr pipe: %w", err)
	}

	start := time.Now()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting command: %w", err)
	}

	// Determine the effective deadline from timeout or parent context.
	var timedOut bool
	var deadlineCtx context.Context
	var deadlineCancel context.CancelFunc
	if timeout > 0 {
		deadlineCtx, deadlineCancel = context.WithTimeout(ctx, timeout)
	} else {
		deadlineCtx, deadlineCancel = context.WithCancel(ctx)
	}
	defer deadlineCancel()

	// Monitor for timeout/cancellation in a separate goroutine.
	waitDone := make(chan error, 1)
	go func() {
		// Stream output in the foreground before waiting.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			streamLines(stdoutPipe, stdoutPrefixer, &stdoutBuf)
		}()
		go func() {
			defer wg.Done()
			streamLines(stderrPipe, stderrPrefixer, &stderrBuf)
		}()
		wg.Wait()
		waitDone <- cmd.Wait()
	}()

	select {
	case waitErr := <-waitDone:
		duration := time.Since(start)
		return buildResult(stepName, waitErr, &stdoutBuf, &stderrBuf, duration, false)

	case <-deadlineCtx.Done():
		timedOut = deadlineCtx.Err() == context.DeadlineExceeded
		// Kill the entire process group: SIGTERM → grace → SIGKILL.
		killProcessGroup(cmd, defaultGracePeriod)
		// Drain the wait channel so goroutine can exit.
		waitErr := <-waitDone
		duration := time.Since(start)
		return buildResult(stepName, waitErr, &stdoutBuf, &stderrBuf, duration, timedOut)
	}
}

// buildResult constructs a StepResult from the execution outcome.
func buildResult(stepName string, waitErr error, stdoutBuf, stderrBuf *limitedBuffer, duration time.Duration, timedOut bool) (*StepResult, error) {
	result := &StepResult{
		StepName: stepName,
		Duration: duration,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
	}

	if stdoutBuf.truncated {
		result.Stderr += fmt.Sprintf("\n[runbook] WARNING: stdout truncated at %d bytes", maxOutputBytes)
	}
	if stderrBuf.truncated {
		result.Stderr += fmt.Sprintf("\n[runbook] WARNING: stderr truncated at %d bytes", maxOutputBytes)
	}

	if timedOut {
		result.Status = StatusTimeout
		result.ExitCode = -1
		return result, nil
	}

	if waitErr == nil {
		result.Status = StatusSuccess
		result.ExitCode = 0
		return result, nil
	}

	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		result.Status = StatusFailed
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}

	return nil, fmt.Errorf("executing step %q: %w", stepName, waitErr)
}

// killProcessGroup sends SIGTERM to the process group, waits the grace period,
// then sends SIGKILL if the process is still running.
func killProcessGroup(cmd *exec.Cmd, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return
	}

	// Send SIGTERM to the process group (negative PID).
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	// Wait for the grace period, then SIGKILL.
	done := make(chan struct{})
	go func() {
		// Process may already be waited on; ignore errors.
		_, _ = cmd.Process.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(grace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// newlineByte avoids allocating []byte("\n") on every iteration.
var newlineByte = []byte{'\n'}

// streamLines reads from r line-by-line, writing each line to both the
// prefixed writer (for real-time display) and the buffer (for capture).
func streamLines(r io.Reader, prefix io.Writer, buf *limitedBuffer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		// Write to the prefixed terminal output.
		_, _ = prefix.Write(line)
		// Write to the capture buffer.
		buf.Write(line)
		buf.Write(newlineByte)
	}
}

// prefixWriter wraps each Write call with a "[step-name] | " prefix.
type prefixWriter struct {
	prefix  []byte
	w       io.Writer
	scratch []byte // reusable buffer to reduce per-line allocations
}

func newPrefixWriter(stepName string, w io.Writer) *prefixWriter {
	return &prefixWriter{
		prefix: append(append([]byte("["), stepName...), "] | "...),
		w:      w,
	}
}

func (p *prefixWriter) Write(data []byte) (int, error) {
	// Write prefix + data + newline as a single write to avoid interleaving.
	// Reuse the scratch buffer when possible to reduce allocations.
	needed := len(p.prefix) + len(data) + 1
	buf := p.scratch
	if cap(buf) < needed {
		buf = make([]byte, 0, needed)
	} else {
		buf = buf[:0]
	}
	buf = append(buf, p.prefix...)
	buf = append(buf, data...)
	buf = append(buf, '\n')
	p.scratch = buf // keep for reuse
	_, err := p.w.Write(buf)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// limitedBuffer captures output up to a maximum size.
type limitedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.truncated {
		return len(p), nil
	}
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

// resolvedDir returns the absolute directory of the given file path.
func ResolvedDir(filePath string) string {
	if filePath == "" {
		return ""
	}
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return filepath.Dir(filePath)
	}
	return filepath.Dir(abs)
}
