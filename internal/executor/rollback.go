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
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// defaultRollbackTimeout is the per-block timeout enforced during rollback
// execution. A hanging rollback is as dangerous as a hanging step.
const defaultRollbackTimeout = 5 * time.Minute

// Trigger reasons recorded on a RollbackReport. Exported so that callers
// inspecting the report can match against well-known constants.
const (
	// TriggerStepFailure indicates a step exited non-zero or timed out.
	TriggerStepFailure = "step_failure"
	// TriggerUserDeclined indicates the operator answered "no" at a confirmation gate.
	TriggerUserDeclined = "user_declined"
	// TriggerUserAbort indicates the operator interrupted the run via SIGINT.
	TriggerUserAbort = "user_abort"
)

// rollbackBlockPrefix is prepended to the step name when a rollback runs,
// so captured output is attributed distinctly from step output.
const rollbackBlockPrefix = "rollback:"

// RollbackStatus represents the outcome of a single rollback execution.
type RollbackStatus int

const (
	// RollbackSuccess indicates the rollback block exited with status 0.
	RollbackSuccess RollbackStatus = iota
	// RollbackFailed indicates the rollback block exited with a non-zero status.
	RollbackFailed
	// RollbackTimedOut indicates the rollback block exceeded defaultRollbackTimeout.
	RollbackTimedOut
)

// Labels returned by RollbackStatus.String.
const (
	rollbackLabelSuccess = "success"
	rollbackLabelFailed  = "failed"
	rollbackLabelTimeout = "timeout"
)

// RollbackEntry records the outcome of a single rollback block execution.
type RollbackEntry struct {
	// Name is the rollback block's name attribute.
	Name string
	// Command is the shell script that was executed.
	Command string
	// Status is the outcome of the execution.
	Status RollbackStatus
	// Error is a short description set when the rollback did not succeed.
	Error string
	// Duration is the wall-clock time spent running the rollback.
	Duration time.Duration
	// StartedAt is the UTC start timestamp.
	StartedAt time.Time
	// FinishedAt is the UTC end timestamp.
	FinishedAt time.Time
	// ExitCode is the subprocess exit code (-1 on timeout).
	ExitCode int
	// Stdout is the captured (possibly truncated) standard output.
	Stdout string
	// Stderr is the captured (possibly truncated) standard error.
	Stderr string
}

// RollbackReport summarizes the full rollback pass.
type RollbackReport struct {
	// Entries is the per-block outcome, in execution order (LIFO relative to push order).
	Entries []RollbackEntry
	// Trigger describes why rollback was initiated; one of the Trigger* constants.
	Trigger string
	// TriggerStep is the name of the step that caused rollback
	// (empty when Trigger == TriggerUserAbort).
	TriggerStep string
	// TotalDuration is the wall-clock time spent running all rollbacks.
	TotalDuration time.Duration
	// Succeeded counts rollback blocks that finished cleanly.
	Succeeded int
	// Failed counts rollback blocks that failed (including timeouts).
	Failed int
	// TimedOut counts rollback blocks killed for exceeding defaultRollbackTimeout.
	TimedOut int
}

// RollbackItem is a single entry on the LIFO rollback stack.
// It is exported so that callers can inspect the Plan without reflection.
type RollbackItem struct {
	// Name is the rollback block's name attribute.
	Name string
	// Command is the shell script to execute when the rollback is popped.
	Command string
}

// RollbackEngine maintains a LIFO stack of rollback blocks and executes
// them in reverse order when triggered.
//
// Push is safe to call from multiple goroutines concurrently (the DAG
// executor may push from parallel step workers). Execute must only be
// called once, after all workers have drained.
type RollbackEngine struct {
	// mu guards stack during concurrent Push calls.
	mu sync.Mutex
	// stack is the LIFO list of pushed rollback items.
	stack []RollbackItem
	// executor runs each rollback command.
	executor *StepExecutor
	// Output is where rollback status messages are written (default: os.Stderr).
	Output io.Writer
}

// String returns the lowercase label of the rollback status.
func (s RollbackStatus) String() string {
	switch s {
	case RollbackSuccess:
		return rollbackLabelSuccess
	case RollbackTimedOut:
		return rollbackLabelTimeout
	default:
		return rollbackLabelFailed
	}
}

// NewRollbackEngine creates a RollbackEngine backed by the given StepExecutor.
func NewRollbackEngine(executor *StepExecutor) *RollbackEngine {
	return &RollbackEngine{
		executor: executor,
		Output:   os.Stderr,
	}
}

// Push adds a rollback block to the top of the LIFO stack.
// Call this after a step succeeds and has a rollback attribute.
func (r *RollbackEngine) Push(name, command string) {
	r.mu.Lock()
	r.stack = append(r.stack, RollbackItem{Name: name, Command: command})
	r.mu.Unlock()
}

// Len returns the number of rollback blocks on the stack.
func (r *RollbackEngine) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.stack)
}

// Plan returns the rollback blocks in the order they would execute
// (LIFO: last pushed first). The returned slice is a copy; mutations do not
// affect the engine's internal stack.
func (r *RollbackEngine) Plan() []RollbackItem {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.stack)
	if n == 0 {
		return nil
	}
	out := make([]RollbackItem, n)
	for i, item := range r.stack {
		out[n-1-i] = item // reverse to match execution order
	}
	return out
}

// clearStack empties the stack without executing rollbacks. Used when the
// user declines a rollback plan. Safe for concurrent use.
func (r *RollbackEngine) clearStack() {
	r.mu.Lock()
	r.stack = r.stack[:0]
	r.mu.Unlock()
}

// Execute pops the rollback stack in reverse order, running each block with
// a 5-minute timeout. If a block fails or times out the failure is recorded
// and execution continues with remaining rollbacks (best-effort). The trigger
// string describes why rollback was initiated.
//
// All concurrent Push calls must have completed before Execute is invoked.
func (r *RollbackEngine) Execute(ctx context.Context, trigger string) *RollbackReport {
	out := r.Output
	if out == nil {
		out = os.Stderr
	}

	// Snapshot and clear the stack under the lock. After this point Push
	// would reopen the stack, but callers are expected to have drained
	// their workers already.
	r.mu.Lock()
	stack := append([]RollbackItem(nil), r.stack...)
	r.stack = r.stack[:0]
	r.mu.Unlock()

	report := &RollbackReport{
		Trigger: trigger,
		Entries: make([]RollbackEntry, 0, len(stack)),
	}

	if len(stack) == 0 {
		fmt.Fprintf(out, "[rollback] no rollback blocks to execute\n")
		return report
	}

	fmt.Fprintf(out, "[rollback] starting rollback (%d blocks, trigger: %s)\n", len(stack), trigger)
	totalStart := time.Now()

	// Pop in LIFO order (last pushed = first executed).
	for i := len(stack) - 1; i >= 0; i-- {
		item := stack[i]
		fmt.Fprintf(out, "[rollback] executing %q (%d of %d)\n", item.Name, len(stack)-i, len(stack))

		blockStart := time.Now()
		result, err := r.executor.Run(ctx, rollbackBlockPrefix+item.Name, item.Command, defaultRollbackTimeout, 0)
		blockEnd := time.Now()

		entry := RollbackEntry{
			Name:       item.Name,
			Command:    item.Command,
			StartedAt:  blockStart,
			FinishedAt: blockEnd,
		}

		if err != nil {
			entry.Status = RollbackFailed
			entry.Error = fmt.Sprintf("execution error: %v", err)
			entry.Duration = blockEnd.Sub(blockStart)
			report.Failed++
			fmt.Fprintf(out, "[rollback] %q failed: %s\n", item.Name, entry.Error)
		} else {
			entry.Duration = result.Duration
			entry.ExitCode = result.ExitCode
			entry.Stdout = result.Stdout
			entry.Stderr = result.Stderr
			switch result.Status {
			case StatusSuccess:
				entry.Status = RollbackSuccess
				report.Succeeded++
				fmt.Fprintf(out, "[rollback] %q succeeded (%s)\n", item.Name, result.Duration.Round(time.Millisecond))
			case StatusTimeout:
				entry.Status = RollbackTimedOut
				entry.Error = fmt.Sprintf("timed out after %s", defaultRollbackTimeout)
				report.TimedOut++
				report.Failed++
				fmt.Fprintf(out, "[rollback] %q timed out after %s\n", item.Name, defaultRollbackTimeout)
			default:
				entry.Status = RollbackFailed
				entry.Error = fmt.Sprintf("exit code %d", result.ExitCode)
				report.Failed++
				fmt.Fprintf(out, "[rollback] %q failed: exit code %d\n", item.Name, result.ExitCode)
			}
		}

		report.Entries = append(report.Entries, entry)
	}

	report.TotalDuration = time.Since(totalStart)

	fmt.Fprintf(out, "[rollback] complete: %d succeeded, %d failed (total %s)\n",
		report.Succeeded, report.Failed, report.TotalDuration.Round(time.Millisecond))
	return report
}
