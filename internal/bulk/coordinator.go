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

// Package bulk coordinates execution of multiple .runbook files in a
// single invocation. It wraps executor.Run with a worker pool that
// fans out runs across a configurable concurrency cap, aggregates the
// per-file results into a BulkResult, and optionally cancels pending
// work on the first failure (--fail-fast).
//
// Concurrency is expressed as two independent dials:
//
//   - MaxRunbooks: how many runbooks run at once (the outer pool).
//   - RunOptions.MaxParallel: how many steps inside a single runbook run
//     at once (the DAG scheduler, unchanged from single-run behaviour).
//
// The two multiply. Callers are expected to choose values whose
// product matches their machine's capacity.
package bulk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/runbookdev/runbook/internal/executor"
)

// MaxRunbooksUpperBound caps the outer concurrency dial. Mirrors the
// per-runbook MaxParallel cap (internal/validator.maxParallelUpperBound)
// so the two dials share the same policy ceiling.
const MaxRunbooksUpperBound = 256

// msgPrefix tags every coordinator-level log line so operators can
// distinguish bulk machinery messages from the wrapped runbook's own
// [runbook] output.
const msgPrefix = "[bulk] "

// ErrNoJobs is returned by Run when Options.Jobs is empty.
var ErrNoJobs = errors.New("bulk: no jobs to execute")

// RunStatus is a string label summarising a single runbook's outcome
// inside a bulk run. It is a superset of executor.RunStatus labels with
// one additional value — StatusSkipped — used when fail-fast cancels a
// file before it was dispatched.
type RunStatus string

// Per-file status labels emitted in BulkResult.Runs.
const (
	// StatusSkipped marks a file that was never dispatched because an
	// earlier failure triggered fail-fast cancellation.
	StatusSkipped RunStatus = "skipped"
)

// Job is a single unit of bulk work: a runbook file plus an optional
// per-run variable binding (merged over Options.Template.Vars) and an
// optional display label used to tag prefixed output. The label is
// how matrix rows of the same file are distinguished in reports and
// streamed output; when empty it falls back to the file's base name.
type Job struct {
	// FilePath is the runbook file to execute.
	FilePath string
	// Vars are per-run variable overrides. Nil or empty means the job
	// inherits Options.Template.Vars unchanged.
	Vars map[string]string
	// Label is an optional display tag. Empty means filepath.Base(FilePath).
	Label string
}

// Options configures a bulk run.
type Options struct {
	// Jobs is the ordered list of bulk work items. Each is run through
	// executor.Run with its FilePath and a merged variable map.
	Jobs []Job
	// MaxRunbooks caps outer concurrency. Zero or one means sequential.
	// Values above MaxRunbooksUpperBound are clamped with a warning
	// written to Stderr.
	MaxRunbooks int
	// FailFast cancels pending and in-flight runs when the first
	// non-success result arrives. Default behaviour; set KeepGoing
	// via its own flag at the CLI layer to disable.
	FailFast bool
	// Template is the RunOptions applied to every job. FilePath and
	// Vars are overwritten per run; all other fields (Env, EnvFile,
	// DryRun, Verbose, Strict, Shell, MaxParallel, AuditLogger) are
	// shared. NonInteractive is forced true by Run when MaxRunbooks>1
	// so parallel workers never block on a prompt.
	Template executor.RunOptions
	// Stdout is the writer for per-run output. When MaxRunbooks>1 each
	// run's stdout is wrapped in a prefixWriter tagged with the job's
	// label; when sequential the writer is passed through untouched.
	Stdout io.Writer
	// Stderr is the writer for per-run status and error output. Same
	// prefixing rules as Stdout.
	Stderr io.Writer
	// ReportStderr is the writer that receives coordinator-level
	// messages (the "forced non-interactive" warning, fail-fast cancel
	// notices, the final summary when the CLI asks for one). Defaults
	// to os.Stderr. Kept separate from Stderr so per-run output and
	// coordinator output can be directed independently in tests.
	ReportStderr io.Writer
}

// Result is the outcome of a single job within a bulk run.
type Result struct {
	// FilePath is the runbook file that was executed (or skipped).
	FilePath string
	// Vars is the per-job variable binding that produced this run
	// (nil when no binding was supplied).
	Vars map[string]string
	// Label is the display tag used in prefixed output and reports.
	Label string
	// Status is the per-run label: either one of executor.RunStatus's
	// label strings, or StatusSkipped when fail-fast cancelled dispatch.
	Status RunStatus
	// ExitCode is the executor.RunStatus exit code (0 on success,
	// >0 on any failure). Skipped runs report 0 since they never ran.
	ExitCode int
	// Duration is the wall-clock time the run took. Zero for skipped.
	Duration time.Duration
	// Error is the human-readable error message from executor.RunResult,
	// empty on success or skip.
	Error string
	// RunResult is the full executor outcome when the run was dispatched.
	// Nil for skipped jobs.
	RunResult *executor.RunResult
}

// BulkResult aggregates the outcomes of a bulk invocation.
type BulkResult struct {
	// Runs lists per-file results in the same order as Options.FilePaths.
	Runs []Result
	// Duration is the wall-clock time of the overall bulk invocation,
	// end-to-end (not the sum of per-run durations).
	Duration time.Duration
	// FailedCount is the number of non-success, non-skipped runs.
	FailedCount int
	// SkippedCount is the number of files that were never dispatched
	// because fail-fast cancelled pending work.
	SkippedCount int
}

// ExitCode returns the highest-severity exit code across all runs,
// using the same ranking as executor.RunStatus.ExitCode(): 20 (internal
// error) > 10 (aborted) > 5 (partial rollback) > 4 (check failed) > 3
// (validation) > 2 (rolled back) > 1 (step failed) > 0 (success).
// Skipped runs contribute 0. An empty BulkResult returns 0.
func (b *BulkResult) ExitCode() int {
	max := 0
	for _, r := range b.Runs {
		if r.ExitCode > max {
			max = r.ExitCode
		}
	}
	return max
}

// runFunc is the executor entry point, injected for testability.
type runFunc func(context.Context, executor.RunOptions) *executor.RunResult

// Run executes every job in opts.Jobs and returns the aggregate.
// It respects the shared parent context — if ctx is cancelled every
// in-flight run sees the cancellation and pending runs are marked
// skipped. Concurrency is capped by opts.MaxRunbooks (0 or 1 = sequential).
//
// Run never writes to opts.Template directly; it derives per-run
// RunOptions for each job so callers can reuse the same Options across
// multiple bulk invocations.
func Run(ctx context.Context, opts Options) (*BulkResult, error) {
	return runWithExecutor(ctx, opts, executor.Run)
}

// runWithExecutor is the test seam: identical to Run but with an
// injectable executor. Production callers use Run.
func runWithExecutor(ctx context.Context, opts Options, exec runFunc) (*BulkResult, error) {
	if len(opts.Jobs) == 0 {
		return nil, ErrNoJobs
	}

	start := time.Now()
	reportErr := opts.ReportStderr
	if reportErr == nil {
		reportErr = os.Stderr
	}

	workers := normaliseWorkers(opts.MaxRunbooks, reportErr)
	parallel := workers > 1

	// When running in parallel we force NonInteractive so no worker
	// blocks reading from stdin while another is streaming output.
	// The executor already treats this as "auto-confirm" for confirm:
	// gates (see internal/executor/run.go promptGate), so runbooks
	// that rely on confirmation still execute — they just skip the
	// prompt instead of hanging.
	template := opts.Template
	if parallel && !template.NonInteractive {
		_, _ = fmt.Fprint(reportErr,
			msgPrefix+"forcing --non-interactive because --max-runbooks>1; confirm gates will auto-approve\n")
		template.NonInteractive = true
	}

	runs := make([]Result, len(opts.Jobs))
	for i, job := range opts.Jobs {
		runs[i] = Result{
			FilePath: job.FilePath,
			Vars:     job.Vars,
			Label:    effectiveLabel(job),
		}
	}

	runCtx, cancelRuns := context.WithCancel(ctx)
	defer cancelRuns()

	// Track whether we've already triggered fail-fast cancellation so
	// only the first failure prints the cancel notice.
	var (
		cancelMu   sync.Mutex
		cancelled  bool
		dispatched = make([]bool, len(opts.Jobs))
	)

	markCancelled := func(firstFail string) bool {
		cancelMu.Lock()
		defer cancelMu.Unlock()
		if cancelled {
			return false
		}
		cancelled = true
		_, _ = fmt.Fprintf(reportErr,
			msgPrefix+"cancelling pending runs after failure in %s\n", firstFail)
		cancelRuns()
		return true
	}

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	// Dispatch is gated on acquiring a semaphore slot in the main
	// goroutine so fail-fast checks kick in deterministically in
	// input order. If we spawned all goroutines up front and let
	// them race for the semaphore, a later job could start before
	// an earlier failure had a chance to cancel the run.
dispatch:
	for i, job := range opts.Jobs {
		cancelMu.Lock()
		c := cancelled
		cancelMu.Unlock()
		if c || runCtx.Err() != nil {
			break
		}

		// Block until a worker slot frees. If the context is
		// cancelled while we wait, stop dispatching.
		select {
		case sem <- struct{}{}:
		case <-runCtx.Done():
			break dispatch
		}

		// Slot acquired — re-check cancellation, since a previous
		// worker may have failed while we were blocked.
		cancelMu.Lock()
		c = cancelled
		cancelMu.Unlock()
		if c {
			<-sem
			break
		}

		wg.Add(1)
		dispatched[i] = true
		idx, jobCopy := i, job
		label := effectiveLabel(jobCopy)

		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			runOpts := template
			runOpts.FilePath = jobCopy.FilePath
			runOpts.Vars = mergeVars(template.Vars, jobCopy.Vars)
			runOpts.Stdout, runOpts.Stderr = writersFor(label, opts.Stdout, opts.Stderr, parallel)

			runStart := time.Now()
			res := exec(runCtx, runOpts)

			// Flush prefixed writers so no partial line is lost.
			flushIfPrefixed(runOpts.Stdout)
			flushIfPrefixed(runOpts.Stderr)

			status, code, msg := summarise(res)
			runs[idx] = Result{
				FilePath:  jobCopy.FilePath,
				Vars:      jobCopy.Vars,
				Label:     label,
				Status:    status,
				ExitCode:  code,
				Duration:  time.Since(runStart),
				Error:     msg,
				RunResult: res,
			}

			if code != 0 && opts.FailFast {
				markCancelled(label)
			}
		}()
	}

	wg.Wait()

	// Anything the outer loop bailed over before dispatching (still
	// holding the zero Result value) is marked skipped too.
	for i, job := range opts.Jobs {
		if !dispatched[i] {
			runs[i] = Result{
				FilePath: job.FilePath,
				Vars:     job.Vars,
				Label:    effectiveLabel(job),
				Status:   StatusSkipped,
			}
		}
	}

	failed, skipped := 0, 0
	for _, r := range runs {
		switch {
		case r.Status == StatusSkipped:
			skipped++
		case r.ExitCode != 0:
			failed++
		}
	}

	return &BulkResult{
		Runs:         runs,
		Duration:     time.Since(start),
		FailedCount:  failed,
		SkippedCount: skipped,
	}, nil
}

// normaliseWorkers clamps MaxRunbooks into [1, MaxRunbooksUpperBound],
// printing a one-line warning to reportErr when clamping occurs.
func normaliseWorkers(n int, reportErr io.Writer) int {
	if n <= 0 {
		return 1
	}

	if n > MaxRunbooksUpperBound {
		_, _ = fmt.Fprintf(reportErr,
			msgPrefix+"--max-runbooks=%d exceeds upper bound %d; clamping\n",
			n, MaxRunbooksUpperBound)
		return MaxRunbooksUpperBound
	}
	return n
}

// writersFor returns the per-run stdout/stderr writers. In parallel
// mode it wraps each in a prefixWriter keyed off the supplied label
// so interleaved output is still attributable. In sequential mode it
// passes the writers through unchanged to preserve the single-run UX.
func writersFor(label string, stdout, stderr io.Writer, parallel bool) (io.Writer, io.Writer) {
	if !parallel {
		return stdout, stderr
	}
	return newPrefixWriter(label, stdout), newPrefixWriter(label, stderr)
}

// effectiveLabel returns the job's display label, falling back to the
// file's base name when none was set. Jobs produced by the CLI always
// carry an explicit label (matrix rows embed their binding); callers
// building Jobs directly can leave Label blank for the plain-file case.
func effectiveLabel(j Job) string {
	if j.Label != "" {
		return j.Label
	}
	return filepath.Base(j.FilePath)
}

// mergeVars returns a new map containing base with overrides applied
// on top. Nil is returned when both inputs are empty so RunOptions.Vars
// stays nil in the common no-override path (matches the phase-1
// single-run behaviour where an empty Vars map and nil are equivalent).
func mergeVars(base, overrides map[string]string) map[string]string {
	if len(base) == 0 && len(overrides) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(overrides))
	maps.Copy(out, base)
	maps.Copy(out, overrides)
	return out
}

// flushIfPrefixed drains any buffered partial line in a prefixWriter.
// Other writer types are ignored.
func flushIfPrefixed(w io.Writer) {
	if pw, ok := w.(*prefixWriter); ok {
		_ = pw.Flush()
	}
}

// summarise extracts the per-file status fields from an executor result.
// A nil result is treated as an internal error so we never lose a slot
// in the BulkResult.
func summarise(r *executor.RunResult) (RunStatus, int, string) {
	if r == nil {
		return RunStatus(executor.RunInternalError.String()), executor.RunInternalError.ExitCode(),
			"bulk: executor returned nil result"
	}
	return RunStatus(r.Status.String()), r.Status.ExitCode(), r.Error
}
