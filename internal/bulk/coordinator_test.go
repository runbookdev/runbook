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

package bulk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runbookdev/runbook/internal/executor"
)

// fakeExecutor returns a deterministic runFunc for tests. It records
// which files it was asked to execute and returns per-file results
// from a map keyed by file path.
type fakeExecutor struct {
	mu        sync.Mutex
	called    []string
	responses map[string]*executor.RunResult
	// block, when non-nil, is closed by the test once workers should
	// be allowed to return. Used to exercise concurrency.
	block <-chan struct{}
	// delay is applied before returning a result, to let the
	// coordinator observe overlapping work.
	delay time.Duration
}

// jobList builds a minimal []Job from a list of file paths, matching the
// phase-1 call pattern where Options exposed FilePaths directly. Kept
// in the test file so production code does not grow a convenience
// wrapper nobody calls outside tests.
func jobList(paths ...string) []Job {
	out := make([]Job, len(paths))
	for i, p := range paths {
		out[i] = Job{FilePath: p}
	}
	return out
}

func (f *fakeExecutor) run(ctx context.Context, opts executor.RunOptions) *executor.RunResult {
	f.mu.Lock()
	f.called = append(f.called, opts.FilePath)
	f.mu.Unlock()

	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return &executor.RunResult{Status: executor.RunAborted}
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return &executor.RunResult{Status: executor.RunAborted}
		}
	}
	if r, ok := f.responses[opts.FilePath]; ok {
		// Preserve status but set a sensible phase.
		if r.Phase == "" {
			r.Phase = executor.PhaseComplete
		}
		return r
	}
	return &executor.RunResult{Status: executor.RunSuccess, Phase: executor.PhaseComplete}
}

func TestRun_RejectsEmptyFileList(t *testing.T) {
	_, err := runWithExecutor(context.Background(), Options{}, func(context.Context, executor.RunOptions) *executor.RunResult {
		t.Fatalf("executor should not be called")
		return nil
	})
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("want ErrNoJobs, got %v", err)
	}
}

func TestRun_SequentialAllSucceed(t *testing.T) {
	fx := &fakeExecutor{responses: map[string]*executor.RunResult{}}
	files := []string{"a.runbook", "b.runbook", "c.runbook"}

	result, err := runWithExecutor(context.Background(), Options{
		Jobs:     jobList(files...),
		FailFast: true,
	}, fx.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ExitCode() != 0 {
		t.Fatalf("want exit 0, got %d", result.ExitCode())
	}

	if len(result.Runs) != len(files) {
		t.Fatalf("want %d runs, got %d", len(files), len(result.Runs))
	}

	for i, r := range result.Runs {
		if r.FilePath != files[i] {
			t.Errorf("run %d: file = %q, want %q", i, r.FilePath, files[i])
		}

		if r.ExitCode != 0 {
			t.Errorf("run %d (%s): exit = %d, want 0", i, r.FilePath, r.ExitCode)
		}
	}
}

func TestRun_FailFastCancelsPending(t *testing.T) {
	fx := &fakeExecutor{responses: map[string]*executor.RunResult{
		"fail.runbook": {Status: executor.RunStepFailed, Error: "boom"},
	}}

	var report bytes.Buffer
	result, err := runWithExecutor(context.Background(), Options{
		Jobs:         jobList("fail.runbook", "later1.runbook", "later2.runbook"),
		MaxRunbooks:  1,
		FailFast:     true,
		ReportStderr: &report,
	}, fx.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Runs[0].ExitCode != 1 {
		t.Errorf("first run: exit = %d, want 1", result.Runs[0].ExitCode)
	}

	if result.Runs[1].Status != StatusSkipped || result.Runs[2].Status != StatusSkipped {
		t.Errorf("pending runs should be skipped; got %s, %s",
			result.Runs[1].Status, result.Runs[2].Status)
	}

	if result.FailedCount != 1 || result.SkippedCount != 2 {
		t.Errorf("failed=%d skipped=%d, want 1 and 2", result.FailedCount, result.SkippedCount)
	}

	if result.ExitCode() != 1 {
		t.Errorf("aggregate exit = %d, want 1 (RunStepFailed)", result.ExitCode())
	}

	if !strings.Contains(report.String(), "cancelling pending runs") {
		t.Errorf("expected cancel notice in report stderr, got: %q", report.String())
	}
}

func TestRun_KeepGoingRunsEverythingAndPicksHighestSeverity(t *testing.T) {
	fx := &fakeExecutor{responses: map[string]*executor.RunResult{
		"a.runbook": {Status: executor.RunStepFailed, Error: "step"},
		"b.runbook": {Status: executor.RunSuccess},
		"c.runbook": {Status: executor.RunInternalError, Error: "internal"},
	}}

	result, err := runWithExecutor(context.Background(), Options{
		Jobs:     jobList("a.runbook", "b.runbook", "c.runbook"),
		FailFast: false,
	}, fx.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SkippedCount != 0 {
		t.Errorf("nothing should be skipped under keep-going, got %d skipped", result.SkippedCount)
	}

	if result.FailedCount != 2 {
		t.Errorf("want 2 failures, got %d", result.FailedCount)
	}

	// Highest severity is RunInternalError (exit 20).
	if got := result.ExitCode(); got != 20 {
		t.Errorf("aggregate exit = %d, want 20", got)
	}

	// fakeExecutor should have seen all three files.
	if len(fx.called) != 3 {
		t.Errorf("executor called %d times, want 3", len(fx.called))
	}
}

func TestRun_ParallelRespectsConcurrencyCap(t *testing.T) {
	// Every run holds a tiny delay so we can observe overlap. We
	// assert that the peak number of concurrent executor calls never
	// exceeds the configured MaxRunbooks.
	var (
		concurrent atomic.Int32
		peak       atomic.Int32
	)
	tracker := func(ctx context.Context, opts executor.RunOptions) *executor.RunResult {
		n := concurrent.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		defer concurrent.Add(-1)
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
		}
		return &executor.RunResult{Status: executor.RunSuccess, Phase: executor.PhaseComplete}
	}

	files := make([]string, 8)
	for i := range files {
		files[i] = fmt.Sprintf("r%d.runbook", i)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := runWithExecutor(ctx, Options{
		Jobs:        jobList(files...),
		MaxRunbooks: 3,
		FailFast:    true,
	}, tracker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if peak.Load() > 3 {
		t.Errorf("peak concurrency = %d, want <=3", peak.Load())
	}

	if peak.Load() < 2 {
		t.Errorf("peak concurrency = %d, want >=2 (expected some overlap)", peak.Load())
	}

	if len(result.Runs) != len(files) {
		t.Errorf("want %d runs, got %d", len(files), len(result.Runs))
	}
}

func TestRun_ForcesNonInteractiveWhenParallel(t *testing.T) {
	seenNI := make(chan bool, 2)
	fx := func(ctx context.Context, opts executor.RunOptions) *executor.RunResult {
		seenNI <- opts.NonInteractive
		return &executor.RunResult{Status: executor.RunSuccess, Phase: executor.PhaseComplete}
	}

	var report bytes.Buffer
	_, err := runWithExecutor(context.Background(), Options{
		Jobs:         jobList("a.runbook", "b.runbook"),
		MaxRunbooks:  2,
		ReportStderr: &report,
		Template:     executor.RunOptions{NonInteractive: false},
	}, fx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	close(seenNI)
	for v := range seenNI {
		if !v {
			t.Errorf("parallel bulk should force NonInteractive=true on template")
		}
	}

	if !strings.Contains(report.String(), "forcing --non-interactive") {
		t.Errorf("expected forced-non-interactive notice, got: %q", report.String())
	}
}

func TestRun_SequentialDoesNotOverrideNonInteractive(t *testing.T) {
	seenNI := make(chan bool, 1)
	fx := func(ctx context.Context, opts executor.RunOptions) *executor.RunResult {
		seenNI <- opts.NonInteractive
		return &executor.RunResult{Status: executor.RunSuccess, Phase: executor.PhaseComplete}
	}

	var report bytes.Buffer
	_, err := runWithExecutor(context.Background(), Options{
		Jobs:         jobList("a.runbook"),
		MaxRunbooks:  1,
		ReportStderr: &report,
		Template:     executor.RunOptions{NonInteractive: false},
	}, fx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	close(seenNI)
	for v := range seenNI {
		if v {
			t.Errorf("sequential bulk must preserve caller's NonInteractive (false), got true")
		}
	}

	if strings.Contains(report.String(), "forcing --non-interactive") {
		t.Errorf("sequential run should not emit the forced-non-interactive warning: %q", report.String())
	}
}

func TestRun_ClampsAboveUpperBound(t *testing.T) {
	fx := &fakeExecutor{}
	var report bytes.Buffer

	_, err := runWithExecutor(context.Background(), Options{
		Jobs:         jobList("a.runbook"),
		MaxRunbooks:  MaxRunbooksUpperBound + 5,
		ReportStderr: &report,
	}, fx.run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(report.String(), "clamping") {
		t.Errorf("expected clamp notice, got %q", report.String())
	}
}

func TestRun_ParallelPrefixesOutput(t *testing.T) {
	fx := func(ctx context.Context, opts executor.RunOptions) *executor.RunResult {
		_, _ = fmt.Fprintln(opts.Stdout, "hello from "+opts.FilePath)
		_, _ = fmt.Fprintln(opts.Stderr, "status from "+opts.FilePath)
		return &executor.RunResult{Status: executor.RunSuccess, Phase: executor.PhaseComplete}
	}

	var stdout, stderr bytes.Buffer
	_, err := runWithExecutor(context.Background(), Options{
		Jobs:        jobList("alpha.runbook", "beta.runbook"),
		MaxRunbooks: 2,
		Stdout:      &stdout,
		Stderr:      &stderr,
	}, fx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each line in the captured output should carry its file label.
	for line := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
		if !strings.HasPrefix(line, "[alpha.runbook] ") && !strings.HasPrefix(line, "[beta.runbook] ") {
			t.Errorf("stdout line missing expected prefix: %q", line)
		}
	}

	for line := range strings.SplitSeq(strings.TrimSpace(stderr.String()), "\n") {
		if !strings.HasPrefix(line, "[alpha.runbook] ") && !strings.HasPrefix(line, "[beta.runbook] ") {
			t.Errorf("stderr line missing expected prefix: %q", line)
		}
	}
}

func TestRun_MergesJobVarsOverTemplateVars(t *testing.T) {
	// Template contributes a common var; each job overrides one key
	// and adds another. The executor must see the merged map, with
	// the job's value winning on conflict.
	var gotVars []map[string]string
	var mu sync.Mutex
	fx := func(ctx context.Context, opts executor.RunOptions) *executor.RunResult {
		mu.Lock()
		// Copy so the post-hoc assertion sees the map we were given
		// (the coordinator reuses local maps — defensive dup).
		cp := make(map[string]string, len(opts.Vars))
		maps.Copy(cp, opts.Vars)
		gotVars = append(gotVars, cp)
		mu.Unlock()
		return &executor.RunResult{Status: executor.RunSuccess, Phase: executor.PhaseComplete}
	}

	jobs := []Job{
		{FilePath: "a.runbook", Vars: map[string]string{"env": "staging", "region": "us"}},
		{FilePath: "b.runbook", Vars: map[string]string{"env": "prod"}},
	}
	_, err := runWithExecutor(context.Background(), Options{
		Jobs:     jobs,
		FailFast: true,
		Template: executor.RunOptions{Vars: map[string]string{"env": "dev", "team": "sre"}},
	}, fx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(gotVars) != 2 {
		t.Fatalf("want 2 exec calls, got %d", len(gotVars))
	}

	// Job 1: job wins on env, region added, team inherited.
	if got := gotVars[0]; got["env"] != "staging" || got["region"] != "us" || got["team"] != "sre" {
		t.Errorf("job 1 vars = %v, want env=staging region=us team=sre", got)
	}

	// Job 2: job wins on env, team inherited, no region.
	if got := gotVars[1]; got["env"] != "prod" || got["team"] != "sre" || got["region"] != "" {
		t.Errorf("job 2 vars = %v, want env=prod team=sre no-region", got)
	}
}

func TestRun_SequentialDoesNotPrefix(t *testing.T) {
	fx := func(ctx context.Context, opts executor.RunOptions) *executor.RunResult {
		_, _ = fmt.Fprintln(opts.Stdout, "hello")
		return &executor.RunResult{Status: executor.RunSuccess, Phase: executor.PhaseComplete}
	}

	var stdout bytes.Buffer
	_, err := runWithExecutor(context.Background(), Options{
		Jobs:        jobList("x.runbook"),
		MaxRunbooks: 1,
		Stdout:      &stdout,
	}, fx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := strings.TrimSpace(stdout.String()); got != "hello" {
		t.Errorf("sequential stdout should be unprefixed, got %q", got)
	}
}
