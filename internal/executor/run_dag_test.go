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
	"slices"
	"strings"
	"testing"
	"time"
)

// TestRun_DAGParallelBranches verifies that three independent branches
// depending on a common root run concurrently: the wall-clock duration
// should be noticeably less than the serial sum of sleep durations.
// Each branch sleeps 0.2s; serial would be ≥0.6s, parallel ~0.2s.
func TestRun_DAGParallelBranches(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts := RunOptions{
		FilePath:       testdataPath(t, "parallel_dag.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		// Frontmatter max_parallel=3 takes precedence; we assert the
		// CLI fallback path by setting 1 here (frontmatter wins).
		MaxParallel: 1,
	}

	start := time.Now()
	result := Run(context.Background(), opts)
	elapsed := time.Since(start)

	if result.Status != RunSuccess {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Error)
	}
	if len(result.StepResults) != 5 {
		t.Fatalf("expected 5 step results, got %d", len(result.StepResults))
	}
	// Root + 3 parallel branches (~0.2s wall) + join. Allow generous
	// slack for CI jitter but must be well under the 0.6s serial sum.
	if elapsed > 500*time.Millisecond {
		t.Errorf("branches did not run in parallel: elapsed=%s (expected <500ms)", elapsed)
	}

	// Completion order: root first, join last, branches in the middle.
	first := result.StepResults[0].StepName
	last := result.StepResults[len(result.StepResults)-1].StepName
	if first != "root" {
		t.Errorf("expected 'root' to complete first, got %q", first)
	}
	if last != "join" {
		t.Errorf("expected 'join' to complete last, got %q", last)
	}
}

// TestRun_DAGFrontmatterOverridesCLI verifies the precedence rule:
// when max_parallel is declared in frontmatter, it overrides the CLI
// MaxParallel option.
func TestRun_DAGFrontmatterOverridesCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts := RunOptions{
		FilePath:       testdataPath(t, "parallel_dag.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
		MaxParallel:    0, // frontmatter says 3 — should still go parallel
	}

	start := time.Now()
	result := Run(context.Background(), opts)
	elapsed := time.Since(start)

	if result.Status != RunSuccess {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Error)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("frontmatter max_parallel ignored: elapsed=%s", elapsed)
	}
}

// TestRun_DAGFailureCascadesAndRollsBack verifies that a failing branch:
//   - marks its dependents as skipped (they never run),
//   - lets sibling branches complete,
//   - triggers rollback for already-completed steps with rollback blocks.
func TestRun_DAGFailureCascadesAndRollsBack(t *testing.T) {
	var stdout, stderr bytes.Buffer
	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "dag_fail_cascades.runbook"),
		NonInteractive: true,
		Stdout:         &stdout,
		Stderr:         &stderr,
	})

	if result.Status != RunRolledBack {
		t.Errorf("expected rolled_back, got %s (error: %s)", result.Status, result.Error)
	}

	// The skipped step "after-bad" must not appear in results.
	for _, sr := range result.StepResults {
		if sr.StepName == "after-bad" {
			t.Errorf("'after-bad' should have been skipped due to failed parent, but ran with status %s", sr.Status)
		}
	}

	// Rollback should have executed undo-good exactly once.
	if result.RollbackReport == nil {
		t.Fatal("expected rollback report")
	}
	if result.RollbackReport.Succeeded != 1 {
		t.Errorf("expected 1 rollback succeeded, got %d", result.RollbackReport.Succeeded)
	}
	if got := result.RollbackReport.TriggerStep; got != "branch-bad" {
		t.Errorf("expected trigger step 'branch-bad', got %q", got)
	}
}

// TestRun_DAGConfirmSerialized verifies that when two parallel steps both
// open a confirmation gate, the prompts are serialized: each prompt block
// reaches its "Confirm [y/n/s/a]:" line exactly once, neither prompt's
// header is lost or duplicated, and both steps ultimately run.
//
// Must be race-clean — a missing promptMu would race on the shared
// stderr buffer and on the scanner reading stdin.
func TestRun_DAGConfirmSerialized(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// Two "y\n" answers, one per step.
	input := strings.NewReader("y\ny\n")

	result := Run(context.Background(), RunOptions{
		FilePath:       testdataPath(t, "dag_confirm_parallel.runbook"),
		NonInteractive: false, // we want the prompts to fire
		Stdout:         &stdout,
		Stderr:         &stderr,
		PromptInput:    input,
	})

	if result.Status != RunSuccess {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if len(result.StepResults) != 2 {
		t.Fatalf("expected 2 step results, got %d", len(result.StepResults))
	}

	// Each step must have produced exactly one confirmation header in
	// stderr. Duplicate or missing headers would indicate interleaved
	// prompts or lost input. The header uses the target env in its
	// trailing quoted string; we assert on the step-specific prefix.
	errOut := stderr.String()
	for _, name := range []string{"alpha", "beta"} {
		prefix := `step "` + name + `" requires confirmation for`
		if got := strings.Count(errOut, prefix); got != 1 {
			t.Errorf("expected exactly 1 confirm header for %s, got %d\n---\n%s", name, got, errOut)
		}
	}

	// The "Confirm [y/n/s/a]:" line should appear at least twice (one per
	// step). promptConfirm may loop on invalid input; with scripted valid
	// input it should be exactly 2.
	if got := strings.Count(errOut, "Confirm [y/n/s/a]:"); got != 2 {
		t.Errorf("expected exactly 2 prompt lines, got %d\n---\n%s", got, errOut)
	}

	// Both steps' commands should have produced their output.
	outStr := stdout.String()
	if !strings.Contains(outStr, "alpha ran") || !strings.Contains(outStr, "beta ran") {
		t.Errorf("expected both 'alpha ran' and 'beta ran' in stdout, got:\n%s", outStr)
	}
}

// TestRun_SequentialPathUnchangedWhenMaxParallelIsZero verifies that
// runbooks without frontmatter max_parallel and with MaxParallel=0
// still go through the sequential code path (regression guard).
func TestRun_SequentialPathUnchangedWhenMaxParallelIsZero(t *testing.T) {
	result := runWithDefaults(t, "simple_success.runbook", nil)
	if result.Status != RunSuccess {
		t.Fatalf("expected success, got %s", result.Status)
	}
	// Document-order preservation: step-one then step-two.
	names := make([]string, 0, len(result.StepResults))
	for _, sr := range result.StepResults {
		names = append(names, sr.StepName)
	}
	want := []string{"step-one", "step-two"}
	if !slices.Equal(names, want) {
		t.Errorf("expected %v, got %v", want, names)
	}
}
