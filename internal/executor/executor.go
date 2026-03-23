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
	"strconv"
	"strings"
	"sync"
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
	// TempDir is the directory for temporary script files. Empty = OS default.
	// Set in tests to use a controlled directory for permission/cleanup checks.
	TempDir string
	// Env is additional environment variables injected as RUNBOOK_* vars.
	Env map[string]string
	// Stdout is the writer for real-time stdout streaming (default: os.Stdout).
	Stdout io.Writer
	// Stderr is the writer for real-time stderr streaming (default: os.Stderr).
	Stderr io.Writer
	// Stdin is forwarded to the subprocess. When nil, the subprocess reads
	// from /dev/null (exec.Cmd default) — safe for non-interactive mode.
	Stdin io.Reader
	// OrphanCheckDelay is how long to wait before scanning for orphaned child
	// processes after a timeout kill. Zero disables orphan detection.
	// Production code sets this to 2s via run.go.
	OrphanCheckDelay time.Duration
}

// Run executes a single step command with the given timeout and grace period.
// timeout=0 means no per-step deadline (only the parent context applies).
// gracePeriod=0 falls back to defaultGracePeriod (10s).
func (e *StepExecutor) Run(ctx context.Context, stepName, command string, timeout, gracePeriod time.Duration) (*StepResult, error) {
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
	grace := gracePeriod
	if grace == 0 {
		grace = defaultGracePeriod
	}

	// Write the command to a temp script file (mode 0600 — os.CreateTemp default).
	// The defer removes the file on any return path, including panics.
	tmpFile, err := os.CreateTemp(e.TempDir, "runbook-step-*.sh")
	if err != nil {
		return nil, fmt.Errorf("creating temp script: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmpFile.WriteString(command); err != nil {
		_ = tmpFile.Close()
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
	setSysProcAttr(cmd)

	// Wire stdin: nil → subprocess reads from /dev/null (non-interactive safe).
	// Non-nil → forward the provided reader (interactive mode).
	cmd.Stdin = e.Stdin

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

	// Verify that Setpgid was honored: the process should be its own group leader.
	// If not, group-kill will fall back to process-kill automatically in killProcessGroup.
	pid := cmd.Process.Pid
	pgid, pgidErr := getProcGroupID(pid)
	pgidVerified := pgidErr == nil && pgid == pid
	if !pgidVerified {
		fmt.Fprintf(stderr,
			"[runbook] warning: step %q: process %d is not its own process group leader (pgid=%d, err=%v); kill will target process only\n",
			stepName, pid, pgid, pgidErr)
	}

	// deadlineCtx inherits cancellation from ctx, so a global timeout set
	// by the caller (via context.WithTimeout on ctx in run.go) will also
	// trigger this select, appearing as context.Canceled (not DeadlineExceeded).
	// Only a per-step timeout creates DeadlineExceeded → StatusTimeout.
	var deadlineCtx context.Context
	var deadlineCancel context.CancelFunc
	if timeout > 0 {
		deadlineCtx, deadlineCancel = context.WithTimeout(ctx, timeout)
	} else {
		deadlineCtx, deadlineCancel = context.WithCancel(ctx)
	}
	defer deadlineCancel()

	// Stream output and wait in a dedicated goroutine. It is the sole owner
	// of cmd.Wait() — killProcessGroup must NOT call cmd.Process.Wait() to
	// avoid the ECHILD race.
	waitDone := make(chan error, 1)
	go func() {
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
		return buildResult(stepName, waitErr, &stdoutBuf, &stderrBuf, time.Since(start), false)

	case <-deadlineCtx.Done():
		timedOut := deadlineCtx.Err() == context.DeadlineExceeded
		killProcessGroup(cmd, grace, pgidVerified, stderr, stepName)
		// Start orphan check asynchronously so it does not delay the return.
		if e.OrphanCheckDelay > 0 {
			go func() {
				time.Sleep(e.OrphanCheckDelay)
				checkOrphans(pid, stepName, stderr)
			}()
		}
		// Wait for the streaming goroutine to finish draining pipes after kill.
		waitErr := <-waitDone
		return buildResult(stepName, waitErr, &stdoutBuf, &stderrBuf, time.Since(start), timedOut)
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

// checkOrphans looks for processes whose PPID matches parentPID and logs a
// warning if any are found. Called asynchronously after a kill.
func checkOrphans(parentPID int, stepName string, w io.Writer) {
	orphans := findOrphans(parentPID)
	if len(orphans) == 0 {
		return
	}
	pids := make([]string, len(orphans))
	for i, p := range orphans {
		pids[i] = strconv.Itoa(p)
	}
	fmt.Fprintf(w,
		"⚠ Warning: step %q was killed but left %d orphaned processes. PIDs: [%s]. Consider adding 'exec' prefix to your commands.\n",
		stepName, len(orphans), strings.Join(pids, ", "))
}

// newlineByte avoids allocating []byte("\n") on every iteration.
var newlineByte = []byte{'\n'}

// streamLines reads from r line-by-line, writing each line to both the
// prefixed writer (for real-time display) and the buffer (for capture).
func streamLines(r io.Reader, prefix io.Writer, buf *limitedBuffer) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		_, _ = prefix.Write(line)
		buf.Write(line)
		buf.Write(newlineByte)
	}
}

// prefixWriter wraps each Write call with a "[step-name] | " prefix.
type prefixWriter struct {
	prefix  []byte
	w       io.Writer
	scratch []byte
}

func newPrefixWriter(stepName string, w io.Writer) *prefixWriter {
	return &prefixWriter{
		prefix: append(append([]byte("["), stepName...), "] | "...),
		w:      w,
	}
}

func (p *prefixWriter) Write(data []byte) (int, error) {
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
	p.scratch = buf
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

// ResolvedDir returns the absolute directory of the given file path.
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
