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
	"time"
)

// RollbackStatus represents the outcome of a single rollback execution.
type RollbackStatus int

const (
	RollbackSuccess RollbackStatus = iota
	RollbackFailed
)

func (s RollbackStatus) String() string {
	if s == RollbackSuccess {
		return "success"
	}
	return "failed"
}

// RollbackEntry records the outcome of a single rollback block execution.
type RollbackEntry struct {
	Name     string
	Status   RollbackStatus
	Error    string
	Duration time.Duration
}

// RollbackReport summarizes the full rollback pass.
type RollbackReport struct {
	Entries   []RollbackEntry
	Trigger   string // "step_failure", "user_abort", or "signal"
	Succeeded int
	Failed    int
}

// rollbackItem is a single entry pushed onto the LIFO stack.
type rollbackItem struct {
	name    string
	command string
}

// RollbackEngine maintains a LIFO stack of rollback blocks and executes
// them in reverse order when triggered.
type RollbackEngine struct {
	stack    []rollbackItem
	executor *StepExecutor
	// Output is where rollback status messages are written (default: os.Stderr).
	Output io.Writer
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
	r.stack = append(r.stack, rollbackItem{name: name, command: command})
}

// Len returns the number of rollback blocks on the stack.
func (r *RollbackEngine) Len() int {
	return len(r.stack)
}

// Execute pops the rollback stack in reverse order, running each block.
// If a rollback block fails, the failure is recorded and execution continues
// with remaining rollbacks (best-effort). The trigger string describes why
// rollback was initiated (e.g. "step_failure", "user_abort").
func (r *RollbackEngine) Execute(ctx context.Context, trigger string) *RollbackReport {
	out := r.Output
	if out == nil {
		out = os.Stderr
	}

	report := &RollbackReport{Trigger: trigger}

	if len(r.stack) == 0 {
		fmt.Fprintf(out, "[rollback] no rollback blocks to execute\n")
		return report
	}

	fmt.Fprintf(out, "[rollback] starting rollback (%d blocks, trigger: %s)\n", len(r.stack), trigger)

	// Pop in LIFO order (last pushed = first executed).
	for i := len(r.stack) - 1; i >= 0; i-- {
		item := r.stack[i]
		fmt.Fprintf(out, "[rollback] executing %q (%d of %d)\n", item.name, len(r.stack)-i, len(r.stack))

		result, err := r.executor.Run(ctx, "rollback:"+item.name, item.command, 0)

		entry := RollbackEntry{Name: item.name}

		if err != nil {
			entry.Status = RollbackFailed
			entry.Error = fmt.Sprintf("execution error: %v", err)
			report.Failed++
			fmt.Fprintf(out, "[rollback] %q failed: %s\n", item.name, entry.Error)
		} else {
			entry.Duration = result.Duration
			if result.Status == StatusSuccess {
				entry.Status = RollbackSuccess
				report.Succeeded++
				fmt.Fprintf(out, "[rollback] %q succeeded (%s)\n", item.name, result.Duration)
			} else {
				entry.Status = RollbackFailed
				entry.Error = fmt.Sprintf("exit code %d", result.ExitCode)
				report.Failed++
				fmt.Fprintf(out, "[rollback] %q failed: exit code %d\n", item.name, result.ExitCode)
			}
		}

		report.Entries = append(report.Entries, entry)
	}

	// Clear the stack after execution.
	r.stack = r.stack[:0]

	fmt.Fprintf(out, "[rollback] complete: %d succeeded, %d failed\n", report.Succeeded, report.Failed)
	return report
}
