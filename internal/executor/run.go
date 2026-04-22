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
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"sync"
	"time"

	"github.com/runbookdev/runbook/internal/ast"
	"github.com/runbookdev/runbook/internal/audit"
	"github.com/runbookdev/runbook/internal/dag"
	"github.com/runbookdev/runbook/internal/parser"
	"github.com/runbookdev/runbook/internal/resolver"
	"github.com/runbookdev/runbook/internal/validator"
)

// RunPhase tracks the lifecycle stage.
type RunPhase string

const (
	PhaseInit        RunPhase = "INIT"
	PhaseChecking    RunPhase = "CHECKING"
	PhaseRunning     RunPhase = "RUNNING"
	PhaseComplete    RunPhase = "COMPLETE"
	PhaseRollingBack RunPhase = "ROLLING_BACK"
	PhaseAborted     RunPhase = "ABORTED"
)

// RunStatus is the final outcome of a Run invocation.
type RunStatus int

const (
	RunSuccess             RunStatus = iota // exit 0
	RunStepFailed                           // exit 1
	RunRolledBack                           // exit 2
	RunValidationError                      // exit 3
	RunCheckFailed                          // exit 4
	RunPartiallyRolledBack RunStatus = 5    // exit 5 — rollback ran but some blocks failed
	RunAborted             RunStatus = 10   // exit 10
	RunInternalError       RunStatus = 20   // exit 20
)

func (s RunStatus) String() string {
	switch s {
	case RunSuccess:
		return "success"
	case RunStepFailed:
		return "step_failed"
	case RunRolledBack:
		return "rolled_back"
	case RunValidationError:
		return "validation_error"
	case RunCheckFailed:
		return "check_failed"
	case RunPartiallyRolledBack:
		return "partially_rolled_back"
	case RunAborted:
		return "aborted"
	case RunInternalError:
		return "internal_error"
	default:
		return "unknown"
	}
}

// ExitCode returns the process exit code for this status.
func (s RunStatus) ExitCode() int {
	switch s {
	case RunSuccess:
		return 0
	case RunStepFailed:
		return 1
	case RunRolledBack:
		return 2
	case RunValidationError:
		return 3
	case RunCheckFailed:
		return 4
	case RunPartiallyRolledBack:
		return 5
	case RunAborted:
		return 10
	case RunInternalError:
		return 20
	default:
		return 20
	}
}

// ConfirmAction represents the user's response to a confirmation gate.
type ConfirmAction int

const (
	ConfirmYes ConfirmAction = iota
	ConfirmNo
	ConfirmSkip
	ConfirmAbort
)

// stepOutcome categorises confirm-gate flow-control outcomes so the DAG
// coordinator can act without duplicating the sequential logic.
type stepOutcome int

const (
	outcomeProceed    stepOutcome = iota // no special handling — run the step
	outcomeSkipByUser                    // confirm → skip (treat as success for scheduling)
	outcomeAbort                         // confirm → abort
	outcomeDenied                        // confirm → no (triggers rollback)
)

// stepCompletion is the message DAG workers send back to the coordinator.
// Either `result` is set (command ran) or `action` is non-zero (confirm
// gate produced a flow-control outcome and no command ran).
type stepCompletion struct {
	node   *dag.Node
	step   ast.StepNode
	result *StepResult
	err    error
	action stepOutcome
}

// syncWriter serializes writes to an underlying writer. Used in DAG mode
// to protect shared stdout/stderr against concurrent writes from parallel
// step workers (bytes.Buffer in tests and the Go stdio files on POSIX
// both lack their own concurrency guarantees).
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newSyncWriter(w io.Writer) *syncWriter { return &syncWriter{w: w} }

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// RunResult holds the final outcome of a runbook execution.
type RunResult struct {
	Status         RunStatus
	Phase          RunPhase
	StepResults    []StepResult
	RollbackReport *RollbackReport
	Duration       time.Duration
	Error          string
}

// RunOptions configures a runbook execution.
type RunOptions struct {
	// FilePath is the path to the .runbook file.
	FilePath string
	// Env is the target environment (e.g. "staging", "production").
	Env string
	// Vars are CLI-provided variables (highest priority).
	Vars map[string]string
	// EnvFile is an optional path to a .env file.
	EnvFile string
	// DryRun shows the execution plan without running commands.
	DryRun bool
	// NonInteractive skips all confirmation prompts (auto-yes).
	NonInteractive bool
	// Strict treats shell metacharacter warnings in resolved variable values as
	// hard errors, causing the run to exit with RunValidationError (exit code 3).
	// Intended for CI pipelines where any injection risk should fail the build.
	Strict bool
	// Verbose enables debug-level output (variable resolution, timing, commands).
	Verbose bool
	// Shell overrides the default /bin/sh.
	Shell string
	// Stdout is the writer for real-time output (default: os.Stdout).
	Stdout io.Writer
	// Stderr is the writer for status/error output (default: os.Stderr).
	Stderr io.Writer
	// PromptInput is the reader for interactive prompts (default: os.Stdin).
	PromptInput io.Reader
	// AuditLogger is an optional audit logger. When non-nil every execution
	// is recorded. The caller is responsible for opening and closing it.
	AuditLogger *audit.Logger
	// MaxParallel caps the number of steps the DAG scheduler runs
	// concurrently. Zero or one preserves the legacy sequential
	// document-order execution. Values >1 activate the DAG scheduler,
	// running independent branches of the dependency graph in parallel.
	// The frontmatter `max_parallel` field takes precedence when both are
	// set and the frontmatter value is >0.
	MaxParallel int
}

// runContext holds the shared state for an in-progress execution,
// allowing the check and step loops to be extracted from Run.
type runContext struct {
	ctx              context.Context
	opts             RunOptions
	start            time.Time
	tree             *ast.RunbookAST
	exec             *StepExecutor
	rollbackEngine   *RollbackEngine
	rollbackMap      map[string]ast.RollbackNode
	result           *RunResult
	sigCh            <-chan SignalAction
	logStepAudit     func(sr *StepResult, blockType, command string)
	logRollbackAudit func(report *RollbackReport)
	logDebug         func(format string, args ...any)
	stderr           io.Writer
	promptInput      io.Reader

	// promptMu serializes confirm prompts and rollback-plan prompts so
	// that parallel workers never interleave their interactive I/O.
	// It also protects promptScanner, which is created once and shared
	// across every interactive read.
	promptMu sync.Mutex
	// promptScanner wraps promptInput once, so successive prompts see
	// piped multi-line input correctly (a fresh bufio.Scanner per prompt
	// would lose lines already buffered by the previous one).
	promptScanner *bufio.Scanner
	// resultMu guards StepResults appends and the RollbackReport assignment
	// during parallel execution.
	resultMu sync.Mutex
}

// Run orchestrates the full runbook lifecycle:
// INIT -> CHECKING -> RUNNING -> COMPLETE (or ROLLING_BACK / ABORTED).
func Run(ctx context.Context, opts RunOptions) *RunResult {
	start := time.Now()
	stdout := orWriter(opts.Stdout, os.Stdout)
	stderr := orWriter(opts.Stderr, os.Stderr)
	promptInput := orReader(opts.PromptInput, os.Stdin)
	verbose := opts.Verbose

	logDebug := func(format string, args ...any) {
		if verbose {
			fmt.Fprintf(stderr, "[debug] "+format+"\n", args...)
		}
	}

	result := &RunResult{Phase: PhaseInit}

	// --- INIT: Parse ---
	fmt.Fprintf(stderr, "[runbook] parsing %s\n", opts.FilePath)
	tree, err := parser.ParseFile(opts.FilePath)
	if err != nil {
		return fail(result, RunInternalError, err.Error(), start)
	}
	for _, w := range tree.ParseWarnings {
		fmt.Fprintf(stderr, "[runbook] warning: %s\n", w)
	}
	logDebug("parsed %s: %d checks, %d steps, %d rollbacks, %d waits",
		opts.FilePath, len(tree.Checks), len(tree.Steps), len(tree.Rollbacks), len(tree.Waits))

	// --- INIT: Validate ---
	fmt.Fprintf(stderr, "[runbook] validating\n")
	valErrs := validator.Validate(tree, validator.Options{})
	for _, ve := range valErrs {
		if ve.Line > 0 {
			fmt.Fprintf(stderr, "[runbook] %s:%d: %s: %s\n", opts.FilePath, ve.Line, ve.Severity, ve.Message)
		} else {
			fmt.Fprintf(stderr, "[runbook] %s: %s: %s\n", opts.FilePath, ve.Severity, ve.Message)
		}
	}
	if validator.HasErrors(valErrs) {
		return fail(result, RunValidationError,
			fmt.Sprintf("%s: validation failed with %d error(s)", opts.FilePath, countErrors(valErrs)), start)
	}

	// --- INIT: Resolve variables ---
	fmt.Fprintf(stderr, "[runbook] resolving variables (env=%s)\n", opts.Env)
	logDebug("resolution priority: builtins < .env(%s) < RUNBOOK_* env < CLI vars(%d)",
		opts.EnvFile, len(opts.Vars))
	resolveOpts := resolver.Options{
		NonInteractive: opts.NonInteractive,
		DryRun:         opts.DryRun,
		Strict:         opts.Strict,
		Stderr:         stderr,
		PromptInput:    promptInput,
	}
	if err := resolver.Resolve(tree, opts.Env, opts.Vars, opts.EnvFile, resolveOpts); err != nil {
		status := RunInternalError
		var metaErr *resolver.MetacharError
		if errors.As(err, &metaErr) {
			status = RunValidationError
		}
		return fail(result, status, err.Error(), start)
	}

	// Redact any secret values that end up in error messages before returning.
	defer func() {
		result.Error = audit.Redact(result.Error, tree.ResolvedSecrets)
	}()

	// --- DRY RUN ---
	if opts.DryRun {
		maxPar := opts.MaxParallel
		if tree.Metadata.MaxParallel > 0 {
			maxPar = tree.Metadata.MaxParallel
		}
		printDryRun(stderr, tree, opts.Env, maxPar)
		result.Status = RunSuccess
		result.Phase = PhaseComplete
		result.Duration = time.Since(start)
		return result
	}

	// --- AUDIT: Start run record ---
	al := opts.AuditLogger
	runID := newRunID()
	if al != nil {
		hostname, _ := os.Hostname()
		username := ""
		if u, err := user.Current(); err == nil {
			username = u.Username
		}
		_ = al.StartRun(audit.RunRecord{
			ID: runID, Runbook: opts.FilePath, Name: tree.Metadata.Name,
			Version: tree.Metadata.Version, Environment: opts.Env,
			StartedAt: start, User: username, Hostname: hostname, Variables: opts.Vars,
		})
		logDebug("audit run started: %s", runID)
	}
	defer func() {
		if al != nil {
			_ = al.EndRun(runID, result.Status.String(), time.Now())
		}
	}()

	logStepAudit := func(sr *StepResult, blockType, command string) {
		if al == nil {
			return
		}
		_ = al.LogStep(audit.StepLog{
			RunID: runID, StepName: sr.StepName, BlockType: blockType,
			StartedAt: time.Now().Add(-sr.Duration), FinishedAt: time.Now(),
			ExitCode: sr.ExitCode, Status: sr.Status.String(),
			Stdout: sr.Stdout, Stderr: sr.Stderr, Command: command,
			Secrets: tree.ResolvedSecrets,
		})
	}

	logRollbackAudit := func(report *RollbackReport) {
		if al == nil || report == nil {
			return
		}
		for _, entry := range report.Entries {
			_ = al.LogStep(audit.StepLog{
				RunID:      runID,
				StepName:   entry.Name,
				BlockType:  "rollback",
				StartedAt:  entry.StartedAt,
				FinishedAt: entry.FinishedAt,
				ExitCode:   entry.ExitCode,
				Status:     entry.Status.String(),
				Stdout:     entry.Stdout,
				Stderr:     entry.Stderr,
				Command:    entry.Command,
				Secrets:    tree.ResolvedSecrets,
			})
		}
	}

	// Apply global timeout from frontmatter as a parent context.
	if tree.Metadata.Timeout != "" {
		if d, parseErr := time.ParseDuration(tree.Metadata.Timeout); parseErr == nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
			logDebug("global timeout: %s", d)
		}
	}

	// Build executor.
	shell := opts.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	workDir := ResolvedDir(opts.FilePath)
	logDebug("shell=%s workdir=%s", shell, workDir)

	exec := &StepExecutor{
		Shell:            shell,
		WorkDir:          workDir,
		Env:              opts.Vars,
		Stdout:           stdout,
		Stderr:           stderr,
		OrphanCheckDelay: 2 * time.Second,
	}
	if !opts.NonInteractive {
		exec.Stdin = os.Stdin
	}

	rollbackEngine := NewRollbackEngine(exec)
	rollbackEngine.Output = stderr

	rollbackMap := make(map[string]ast.RollbackNode, len(tree.Rollbacks))
	for _, rb := range tree.Rollbacks {
		rollbackMap[rb.Name] = rb
	}

	// Set up signal handling (only in interactive mode).
	var sigCh <-chan SignalAction
	if !opts.NonInteractive {
		sigHandler := &SignalHandler{Input: promptInput, Output: stderr, PromptTimeout: signalPromptTimeout}
		sigCh = sigHandler.Start()
		defer sigHandler.Stop()
	}

	rc := &runContext{
		ctx: ctx, opts: opts, start: start, tree: tree,
		exec: exec, rollbackEngine: rollbackEngine, rollbackMap: rollbackMap,
		result: result, sigCh: sigCh, logStepAudit: logStepAudit,
		logRollbackAudit: logRollbackAudit,
		logDebug:         logDebug, stderr: stderr, promptInput: promptInput,
		promptScanner: bufio.NewScanner(promptInput),
	}

	// --- CHECKING ---
	if r := rc.executeChecks(); r != nil {
		return r
	}

	// --- RUNNING ---
	if r := rc.dispatchSteps(); r != nil {
		return r
	}

	// --- COMPLETE ---
	result.Phase = PhaseComplete
	result.Status = RunSuccess
	result.Duration = time.Since(start)
	fmt.Fprintf(stderr, "[runbook] complete (%s)\n", result.Duration.Round(time.Millisecond))
	return result
}

// executeChecks runs all check blocks sequentially.
// Returns non-nil to short-circuit the caller if a check fails or a signal fires.
func (rc *runContext) executeChecks() *RunResult {
	rc.result.Phase = PhaseChecking
	fmt.Fprintf(rc.stderr, "[runbook] running %d checks\n", len(rc.tree.Checks))

	for i, check := range rc.tree.Checks {
		if action := pollSignal(rc.sigCh); action != nil {
			r := handleSignal(rc.ctx, *action, rc.result, rc.rollbackEngine, rc.start)
			rc.logRollbackAudit(rc.result.RollbackReport)
			return r
		}

		fmt.Fprintf(rc.stderr, "[runbook] check [%d/%d] %q\n", i+1, len(rc.tree.Checks), check.Name)
		rc.logDebug("check command: %s", indentCommand(check.Command))
		checkStart := time.Now()
		sr, err := rc.exec.Run(rc.ctx, "check:"+check.Name, check.Command, 0, 0)
		if err != nil {
			return fail(rc.result, RunInternalError,
				fmt.Sprintf("%s:%d: check %q: %v", rc.opts.FilePath, check.Line, check.Name, err), rc.start)
		}
		rc.logStepAudit(sr, "check", check.Command)
		rc.logDebug("check %q finished in %s (exit %d)", check.Name, time.Since(checkStart).Round(time.Millisecond), sr.ExitCode)
		if sr.Status != StatusSuccess {
			return fail(rc.result, RunCheckFailed,
				fmt.Sprintf("%s:%d: check %q failed (exit code %d)", rc.opts.FilePath, check.Line, check.Name, sr.ExitCode), rc.start)
		}
	}
	return nil
}

// dispatchSteps routes to either the sequential or DAG-parallel executor
// based on the effective max-parallel setting.
//
// The frontmatter `max_parallel` field takes precedence when >0; otherwise
// the CLI option applies. Values <=1 keep the legacy sequential path,
// preserving document-order execution for runbooks that do not opt into
// parallelism. Values >1 activate the DAG scheduler.
func (rc *runContext) dispatchSteps() *RunResult {
	rc.result.Phase = PhaseRunning
	maxPar := rc.effectiveMaxParallel()
	if maxPar <= 1 {
		return rc.executeStepsSequential()
	}
	return rc.executeStepsDAG(maxPar)
}

// effectiveMaxParallel returns the concurrency cap: frontmatter wins over
// the CLI/options value when positive, so runbook authors can mandate
// parallelism their document was written for.
func (rc *runContext) effectiveMaxParallel() int {
	if rc.tree.Metadata.MaxParallel > 0 {
		return rc.tree.Metadata.MaxParallel
	}
	return rc.opts.MaxParallel
}

// executeStepsSequential runs steps one at a time in document order.
// This is the legacy path and preserves exact prior behavior.
func (rc *runContext) executeStepsSequential() *RunResult {
	fmt.Fprintf(rc.stderr, "[runbook] running %d steps\n", len(rc.tree.Steps))

	for i, step := range rc.tree.Steps {
		if action := pollSignal(rc.sigCh); action != nil {
			r := handleSignal(rc.ctx, *action, rc.result, rc.rollbackEngine, rc.start)
			rc.logRollbackAudit(rc.result.RollbackReport)
			return r
		}

		if r := rc.handleConfirmGate(step); r != nil {
			if r == skipSentinel {
				continue
			}
			return r
		}

		fmt.Fprintf(rc.stderr, "[runbook] step [%d/%d] %q\n", i+1, len(rc.tree.Steps), step.Name)
		sr, err := rc.runStep(rc.ctx, step)
		if err != nil {
			return fail(rc.result, RunInternalError,
				fmt.Sprintf("%s:%d: step %q: %v", rc.opts.FilePath, step.Line, step.Name, err), rc.start)
		}
		rc.recordStepResult(sr, step)

		if sr.Status == StatusSuccess {
			rc.pushRollback(step)
			continue
		}

		return rc.handleStepFailure(step, sr)
	}
	return nil
}

// executeStepsDAG runs steps through the Kahn's-algorithm scheduler with
// up to maxPar workers in flight. Confirm gates are serialized; step
// failures cancel in-flight siblings and trigger rollback (LIFO by
// completion order).
func (rc *runContext) executeStepsDAG(maxPar int) *RunResult {
	graph, err := dag.Build(rc.tree.Steps)
	if err != nil {
		// Cycles are caught earlier by validator rule v21; this is
		// defense-in-depth for callers that skipped validation (e.g.
		// programmatic use of executor.Run).
		return fail(rc.result, RunValidationError,
			fmt.Sprintf("%s: %v", rc.opts.FilePath, err), rc.start)
	}

	// Parallel workers share the same stdout/stderr. Wrap them so their
	// writes are serialized; both the executor's streaming output and
	// our own status messages go through these wrappers.
	rc.stderr = newSyncWriter(rc.stderr)
	rc.exec.Stdout = newSyncWriter(rc.exec.Stdout)
	rc.exec.Stderr = newSyncWriter(rc.exec.Stderr)
	rc.rollbackEngine.Output = rc.stderr

	fmt.Fprintf(rc.stderr, "[runbook] running %d steps (DAG, up to %d in parallel)\n",
		len(rc.tree.Steps), maxPar)

	// Per-step metadata lookup for confirm/rollback handling.
	stepByName := make(map[string]ast.StepNode, len(rc.tree.Steps))
	for _, s := range rc.tree.Steps {
		stepByName[s.Name] = s
	}

	sched := dag.NewScheduler(graph)
	stepCtx, cancelSteps := context.WithCancel(rc.ctx)
	defer cancelSteps()

	done := make(chan stepCompletion, maxPar)

	var (
		inFlight   int
		firstFail  *stepCompletion // the step whose failure triggered shutdown
		aborted    bool            // user requested abort via confirm or signal
		userDenied bool            // confirm gate answered "no" with rollback decline
	)

	// drain waits for all in-flight workers to complete after a cancellation.
	// Workers that raced to success before the cancellation are recorded,
	// and their rollbacks are pushed so the rollback pass covers them.
	drain := func() {
		for inFlight > 0 {
			c := <-done
			inFlight--
			if c.err != nil || c.result == nil {
				continue
			}
			rc.recordStepResult(c.result, c.step)
			if c.result.Status == StatusSuccess {
				rc.pushRollback(c.step)
			}
		}
	}

	for sched.HasWork() && firstFail == nil && !aborted && !userDenied {
		// Handle pending signal before dispatching more work.
		if action := pollSignal(rc.sigCh); action != nil {
			cancelSteps()
			drain()
			r := handleSignal(rc.ctx, *action, rc.result, rc.rollbackEngine, rc.start)
			rc.logRollbackAudit(rc.result.RollbackReport)
			return r
		}

		// Dispatch as many ready workers as the cap allows.
		slots := maxPar - inFlight
		for _, node := range sched.PopReady(slots) {
			step, ok := stepByName[node.Name]
			if !ok {
				// Shouldn't happen — graph was built from rc.tree.Steps.
				return fail(rc.result, RunInternalError,
					fmt.Sprintf("step %q missing from lookup table", node.Name), rc.start)
			}
			inFlight++
			go rc.runDAGWorker(stepCtx, node, step, done)
		}

		if inFlight == 0 {
			// No ready nodes and none in flight — should be covered by
			// HasWork() returning false, but guard anyway.
			break
		}

		c := <-done
		inFlight--

		switch c.action {
		case outcomeSkipByUser:
			// Treat as success for scheduling purposes: dependents proceed.
			fmt.Fprintf(rc.stderr, "[runbook] step %q: skipped by user\n", c.step.Name)
			sched.CompleteSuccess(c.node.Name)
			continue
		case outcomeAbort:
			aborted = true
			cancelSteps()
			drain()
			r := handleSignal(rc.ctx, ActionQuit, rc.result, rc.rollbackEngine, rc.start)
			rc.logRollbackAudit(rc.result.RollbackReport)
			return r
		case outcomeDenied:
			// User declined confirmation. Cancel remaining, offer rollback.
			userDenied = true
			cancelSteps()
			drain()
			return rc.declineConfirm(c.step)
		}

		if c.err != nil {
			cancelSteps()
			drain()
			return fail(rc.result, RunInternalError,
				fmt.Sprintf("%s:%d: step %q: %v", rc.opts.FilePath, c.step.Line, c.step.Name, c.err), rc.start)
		}

		rc.recordStepResult(c.result, c.step)

		if c.result.Status == StatusSuccess {
			rc.pushRollback(c.step)
			sched.CompleteSuccess(c.node.Name)
			continue
		}

		// Step failed or timed out — cancel siblings, drain, rollback.
		firstFail = &c
		skipped := sched.Skip(c.node.Name)
		if len(skipped) > 0 {
			rc.logDebug("cascading skip to %d dependent step(s): %s",
				len(skipped), strings.Join(skipped, ", "))
		}
		cancelSteps()
		drain()
	}

	if firstFail != nil {
		return rc.handleStepFailure(firstFail.step, firstFail.result)
	}
	return nil
}

// runDAGWorker runs a single step in a goroutine, handling the confirm
// gate under the shared prompt mutex so parallel workers never interleave
// their I/O. The result (or flow-control action) is delivered on `done`.
func (rc *runContext) runDAGWorker(ctx context.Context, node *dag.Node, step ast.StepNode, done chan<- stepCompletion) {
	action := rc.promptGate(step)
	switch action {
	case outcomeSkipByUser, outcomeAbort, outcomeDenied:
		done <- stepCompletion{node: node, step: step, action: action}
		return
	}

	fmt.Fprintf(rc.stderr, "[runbook] step %q (starting)\n", step.Name)
	sr, err := rc.runStep(ctx, step)
	done <- stepCompletion{node: node, step: step, result: sr, err: err}
}

// promptGate is the DAG-path equivalent of handleConfirmGate's
// confirm-only prompt. It serializes access to the prompt via
// rc.promptMu so parallel workers never interleave their I/O, and
// translates the user's response into a stepOutcome. Unlike the
// sequential handleConfirmGate, this function never runs rollback
// itself — the coordinator does that after draining in-flight workers.
func (rc *runContext) promptGate(step ast.StepNode) stepOutcome {
	if step.Confirm == "" || !confirmMatches(step.Confirm, rc.opts.Env) {
		return outcomeProceed
	}
	if rc.opts.NonInteractive {
		fmt.Fprintf(rc.stderr, "[runbook] step %q: auto-confirmed (non-interactive)\n", step.Name)
		return outcomeProceed
	}

	rc.promptMu.Lock()
	defer rc.promptMu.Unlock()

	action := promptConfirm(rc.stderr, rc.promptScanner, step.Name, rc.opts.Env, step.Command, rc.tree.ResolvedSecrets)
	switch action {
	case ConfirmYes:
		return outcomeProceed
	case ConfirmSkip:
		return outcomeSkipByUser
	case ConfirmAbort:
		return outcomeAbort
	default:
		return outcomeDenied
	}
}

// runStep wraps StepExecutor.Run with logging and timeout parsing. It is
// safe to call from multiple goroutines.
func (rc *runContext) runStep(ctx context.Context, step ast.StepNode) (*StepResult, error) {
	stepTimeout := parseDuration(step.Timeout)
	stepGrace := parseDuration(step.KillGrace)
	rc.logDebug("step command: %s", indentCommand(step.Command))
	if stepTimeout > 0 {
		rc.logDebug("step timeout: %s", stepTimeout)
	}
	stepStart := time.Now()
	sr, err := rc.exec.Run(ctx, step.Name, step.Command, stepTimeout, stepGrace)
	if err == nil && sr != nil {
		rc.logDebug("step %q finished in %s (exit %d)", step.Name, time.Since(stepStart).Round(time.Millisecond), sr.ExitCode)
	}
	return sr, err
}

// recordStepResult appends a completed step result to the run summary and
// emits its audit entry. Safe for concurrent callers.
func (rc *runContext) recordStepResult(sr *StepResult, step ast.StepNode) {
	rc.resultMu.Lock()
	rc.result.StepResults = append(rc.result.StepResults, *sr)
	rc.resultMu.Unlock()
	rc.logStepAudit(sr, "step", step.Command)
}

// pushRollback adds the step's rollback to the stack when one is declared.
func (rc *runContext) pushRollback(step ast.StepNode) {
	if step.Rollback == "" {
		return
	}
	rb, ok := rc.rollbackMap[step.Rollback]
	if !ok {
		return
	}
	rc.rollbackEngine.Push(rb.Name, rb.Command)
	rc.logDebug("pushed rollback %q onto stack (depth: %d)", rb.Name, rc.rollbackEngine.Len())
}

// handleStepFailure runs the shared failure/rollback flow for both
// execution paths. The failing step's name is recorded as TriggerStep on
// the rollback report.
func (rc *runContext) handleStepFailure(step ast.StepNode, sr *StepResult) *RunResult {
	fmt.Fprintf(rc.stderr, "[runbook] %s:%d: step %q %s (exit code %d)\n",
		rc.opts.FilePath, step.Line, step.Name, sr.Status, sr.ExitCode)

	if !rc.opts.NonInteractive && rc.rollbackEngine.Len() > 0 {
		rc.promptMu.Lock()
		confirmed := promptRollbackPlan(rc.stderr, rc.promptScanner, step.Name, sr.ExitCode,
			rc.rollbackEngine.Plan(), rc.tree.ResolvedSecrets)
		rc.promptMu.Unlock()
		if !confirmed {
			rc.rollbackEngine.clearStack()
			rc.result.Status = RunStepFailed
			rc.result.Duration = time.Since(rc.start)
			return rc.result
		}
	}

	rc.result.Phase = PhaseRollingBack
	report := rc.rollbackEngine.Execute(rc.ctx, "step_failure")
	report.TriggerStep = step.Name
	rc.result.RollbackReport = report
	rc.logRollbackAudit(report)
	rc.result.Status = rollbackRunStatus(report)
	rc.result.Duration = time.Since(rc.start)
	return rc.result
}

// declineConfirm handles the user-declined-confirmation outcome from the
// DAG path: offer the rollback plan and exit with RunStepFailed or a
// rollback status.
func (rc *runContext) declineConfirm(step ast.StepNode) *RunResult {
	rc.result.Error = fmt.Sprintf("%s:%d: step %q: user declined confirmation",
		rc.opts.FilePath, step.Line, step.Name)
	if rc.rollbackEngine.Len() > 0 {
		rc.promptMu.Lock()
		confirmed := promptRollbackPlan(rc.stderr, rc.promptScanner, step.Name, 0,
			rc.rollbackEngine.Plan(), rc.tree.ResolvedSecrets)
		rc.promptMu.Unlock()
		if !confirmed {
			rc.rollbackEngine.clearStack()
			rc.result.Status = RunStepFailed
			rc.result.Duration = time.Since(rc.start)
			return rc.result
		}
	}
	rc.result.Phase = PhaseRollingBack
	report := rc.rollbackEngine.Execute(rc.ctx, "user_declined")
	report.TriggerStep = step.Name
	rc.result.RollbackReport = report
	rc.logRollbackAudit(report)
	rc.result.Status = rollbackRunStatus(report)
	rc.result.Duration = time.Since(rc.start)
	return rc.result
}

// skipSentinel is returned by handleConfirmGate to signal a `continue`.
var skipSentinel = &RunResult{}

// handleConfirmGate processes a step's confirmation gate.
// Returns nil to proceed, skipSentinel to skip, or a result to abort.
func (rc *runContext) handleConfirmGate(step ast.StepNode) *RunResult {
	if step.Confirm == "" || !confirmMatches(step.Confirm, rc.opts.Env) {
		return nil
	}
	if rc.opts.NonInteractive {
		fmt.Fprintf(rc.stderr, "[runbook] step %q: auto-confirmed (non-interactive)\n", step.Name)
		return nil
	}

	action := promptConfirm(rc.stderr, rc.promptScanner, step.Name, rc.opts.Env, step.Command, rc.tree.ResolvedSecrets)
	switch action {
	case ConfirmYes:
		return nil
	case ConfirmSkip:
		fmt.Fprintf(rc.stderr, "[runbook] step %q: skipped by user\n", step.Name)
		return skipSentinel
	case ConfirmAbort:
		r := handleSignal(rc.ctx, ActionQuit, rc.result, rc.rollbackEngine, rc.start)
		rc.logRollbackAudit(rc.result.RollbackReport)
		return r
	default: // ConfirmNo
		rc.result.Error = fmt.Sprintf("%s:%d: step %q: user declined confirmation", rc.opts.FilePath, step.Line, step.Name)
		if rc.rollbackEngine.Len() > 0 {
			if !promptRollbackPlan(rc.stderr, rc.promptScanner, step.Name, 0,
				rc.rollbackEngine.Plan(), rc.tree.ResolvedSecrets) {
				rc.rollbackEngine.stack = rc.rollbackEngine.stack[:0]
				rc.result.Status = RunStepFailed
				rc.result.Duration = time.Since(rc.start)
				return rc.result
			}
		}
		rc.result.Phase = PhaseRollingBack
		report := rc.rollbackEngine.Execute(rc.ctx, "user_declined")
		report.TriggerStep = step.Name
		rc.result.RollbackReport = report
		rc.logRollbackAudit(report)
		rc.result.Status = rollbackRunStatus(report)
		rc.result.Duration = time.Since(rc.start)
		return rc.result
	}
}

// --- helpers ---

// fail sets the result fields for an early exit and returns the result.
func fail(result *RunResult, status RunStatus, errMsg string, start time.Time) *RunResult {
	result.Status = status
	result.Error = errMsg
	result.Duration = time.Since(start)
	return result
}

func orWriter(w io.Writer, fallback io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return fallback
}

func orReader(r io.Reader, fallback io.Reader) io.Reader {
	if r != nil {
		return r
	}
	return fallback
}

func parseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, _ := time.ParseDuration(s)
	return d
}

// countErrors returns the number of validation errors with Error severity.
func countErrors(errs []validator.ValidationError) int {
	n := 0
	for _, e := range errs {
		if e.Severity == validator.Error {
			n++
		}
	}
	return n
}

// pollSignal does a non-blocking check of the signal channel.
func pollSignal(ch <-chan SignalAction) *SignalAction {
	if ch == nil {
		return nil
	}
	select {
	case action := <-ch:
		return &action
	default:
		return nil
	}
}

// handleSignal processes a signal action and returns the appropriate RunResult.
func handleSignal(ctx context.Context, action SignalAction, result *RunResult, engine *RollbackEngine, start time.Time) *RunResult {
	switch action {
	case ActionRollback:
		result.Phase = PhaseRollingBack
		report := engine.Execute(ctx, "user_abort")
		result.RollbackReport = report
		if report.Succeeded > 0 || report.Failed > 0 {
			result.Status = RunRolledBack
		} else {
			result.Status = RunAborted
		}
	case ActionContinue:
		return result
	case ActionQuit:
		result.Phase = PhaseAborted
		result.Status = RunAborted
		result.Error = "aborted by user"
	}
	result.Duration = time.Since(start)
	return result
}

// confirmMatches checks whether a step's confirm attribute applies to the target env.
func confirmMatches(confirm, targetEnv string) bool {
	if confirm == "" {
		return false
	}
	if strings.EqualFold(confirm, "always") {
		return true
	}
	return strings.EqualFold(confirm, targetEnv)
}

// rollbackRunStatus converts a RollbackReport into the appropriate RunStatus.
// All succeeded → RunRolledBack; any failure/timeout → RunPartiallyRolledBack;
// nothing ran (empty stack) → RunStepFailed.
func rollbackRunStatus(report *RollbackReport) RunStatus {
	if report == nil || (report.Succeeded == 0 && report.Failed == 0) {
		return RunStepFailed
	}
	if report.Failed > 0 {
		return RunPartiallyRolledBack
	}
	return RunRolledBack
}

// promptRollbackPlan shows the pending rollback commands and asks the user
// whether to execute them. Returns true if the user confirms, false to skip.
//
// The scanner is shared across all prompts in a run so that piped
// multi-line input (e.g. "y\ny\n") is consumed line-by-line, not
// swallowed by the first call's read-ahead buffer.
func promptRollbackPlan(w io.Writer, sc *bufio.Scanner, failedStep string, exitCode int, plan []RollbackItem, secrets map[string]string) bool {
	if exitCode != 0 {
		fmt.Fprintf(w, "\n✗ Step %q failed (exit code %d).\n", failedStep, exitCode)
	} else {
		fmt.Fprintf(w, "\n✗ Step %q declined — triggering rollback.\n", failedStep)
	}
	fmt.Fprintf(w, "\nRollback plan (most recent first):\n")
	for i, item := range plan {
		displayCmd := audit.RedactDisplay(item.Command, secrets)
		fmt.Fprintf(w, "  %d. %s: %s\n", i+1, item.Name, indentCommand(displayCmd))
	}
	fmt.Fprintf(w, "\nExecute rollback? [y]es / [n]o (skip rollback and exit)\n")

	for {
		fmt.Fprintf(w, "  Confirm [y/n]: ")
		if !sc.Scan() {
			return true // default yes on EOF / non-interactive pipe
		}
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
	}
}

// promptConfirm displays a confirmation gate and reads the user's response.
// Secret values in command are replaced with **** in the display. The user may
// type [r]eveal to see the full command before deciding.
//
// The scanner is shared across all prompts in a run (see promptRollbackPlan).
func promptConfirm(w io.Writer, sc *bufio.Scanner, stepName, env, command string, secrets map[string]string) ConfirmAction {
	hasSecrets := len(secrets) > 0
	displayCmd := audit.RedactDisplay(command, secrets)

	fmt.Fprintf(w, "\n[runbook] step %q requires confirmation for %q\n", stepName, env)
	fmt.Fprintf(w, "  Command: %s\n", indentCommand(displayCmd))
	fmt.Fprintf(w, "  [y]es   — execute this step\n")
	fmt.Fprintf(w, "  [n]o    — skip and rollback\n")
	fmt.Fprintf(w, "  [s]kip  — skip this step, continue with next\n")
	fmt.Fprintf(w, "  [a]bort — stop immediately\n")
	if hasSecrets {
		fmt.Fprintf(w, "  [r]eveal — show full command\n")
	}

	for {
		if hasSecrets {
			fmt.Fprintf(w, "  Confirm [y/n/s/a/r]: ")
		} else {
			fmt.Fprintf(w, "  Confirm [y/n/s/a]: ")
		}
		if !sc.Scan() {
			return ConfirmNo
		}
		input := strings.ToLower(strings.TrimSpace(sc.Text()))
		if hasSecrets && (input == "r" || input == "reveal") {
			fmt.Fprintf(w, "  Full command: %s\n", command)
			continue
		}
		return parseConfirmAction(input)
	}
}

// parseConfirmAction converts user input to a ConfirmAction.
func parseConfirmAction(s string) ConfirmAction {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "y", "yes":
		return ConfirmYes
	case "s", "skip":
		return ConfirmSkip
	case "a", "abort":
		return ConfirmAbort
	default:
		return ConfirmNo
	}
}

// printDryRun displays the execution plan without running any commands.
// Secret values in resolved commands are replaced with **** for display.
// When maxPar > 1 the step section is printed as DAG layers so users can
// see which groups will run in parallel.
func printDryRun(w io.Writer, tree *ast.RunbookAST, env string, maxPar int) {
	fmt.Fprintf(w, "\n[dry-run] Runbook: %s (v%s)\n", tree.Metadata.Name, tree.Metadata.Version)
	if env != "" {
		fmt.Fprintf(w, "[dry-run] Environment: %s\n", env)
	}
	if tree.Metadata.Timeout != "" {
		fmt.Fprintf(w, "[dry-run] Global timeout: %s\n", tree.Metadata.Timeout)
	}
	if maxPar > 1 {
		fmt.Fprintf(w, "[dry-run] Max parallel: %d (DAG scheduler)\n", maxPar)
	}

	if len(tree.Checks) > 0 {
		fmt.Fprintf(w, "\n[dry-run] Checks (%d):\n", len(tree.Checks))
		for i, c := range tree.Checks {
			fmt.Fprintf(w, "  %d. %s\n", i+1, c.Name)
			fmt.Fprintf(w, "     %s\n", indentCommand(audit.RedactDisplay(c.Command, tree.ResolvedSecrets)))
		}
	}

	if len(tree.Steps) > 0 {
		if maxPar > 1 {
			printDryRunDAG(w, tree, maxPar)
		} else {
			printDryRunSequential(w, tree)
		}
	}

	if len(tree.Rollbacks) > 0 {
		fmt.Fprintf(w, "\n[dry-run] Rollbacks (%d):\n", len(tree.Rollbacks))
		for _, rb := range tree.Rollbacks {
			fmt.Fprintf(w, "  - %s\n", rb.Name)
		}
	}

	fmt.Fprintf(w, "\n[dry-run] plan complete — no commands were executed\n")
}

// printDryRunSequential renders steps as a linear list (legacy format).
func printDryRunSequential(w io.Writer, tree *ast.RunbookAST) {
	fmt.Fprintf(w, "\n[dry-run] Steps (%d):\n", len(tree.Steps))
	for i, s := range tree.Steps {
		printDryRunStep(w, i+1, s, tree.ResolvedSecrets)
	}
}

// printDryRunDAG renders steps grouped by topological layer so the user
// can see which groups will run concurrently. Each layer's steps would
// run in parallel (up to maxPar workers).
func printDryRunDAG(w io.Writer, tree *ast.RunbookAST, maxPar int) {
	graph, err := dag.Build(tree.Steps)
	if err != nil {
		// Cycle or similar — fall back to sequential display.
		fmt.Fprintf(w, "\n[dry-run] Warning: could not build DAG (%v); falling back to linear plan\n", err)
		printDryRunSequential(w, tree)
		return
	}
	levels := graph.Levels()
	fmt.Fprintf(w, "\n[dry-run] Steps (%d, across %d DAG layer(s), up to %d parallel):\n",
		len(tree.Steps), len(levels), maxPar)

	stepByName := make(map[string]ast.StepNode, len(tree.Steps))
	for _, s := range tree.Steps {
		stepByName[s.Name] = s
	}

	counter := 0
	for lvl, layer := range levels {
		header := fmt.Sprintf("Layer %d (%d step)", lvl, len(layer))
		if len(layer) > 1 {
			header = fmt.Sprintf("Layer %d (%d steps, parallel)", lvl, len(layer))
		}
		fmt.Fprintf(w, "\n  %s:\n", header)
		for _, node := range layer {
			counter++
			printDryRunStep(w, counter, stepByName[node.Name], tree.ResolvedSecrets)
		}
	}
}

// printDryRunStep renders one step with its metadata and command body.
func printDryRunStep(w io.Writer, n int, s ast.StepNode, secrets map[string]string) {
	var extras []string
	if s.Timeout != "" {
		extras = append(extras, "timeout="+s.Timeout)
	}
	if s.Rollback != "" {
		extras = append(extras, "rollback="+s.Rollback)
	}
	if s.Confirm != "" {
		extras = append(extras, "confirm="+s.Confirm)
	}
	if s.DependsOn != "" {
		extras = append(extras, "depends_on="+s.DependsOn)
	}
	if len(s.Env) > 0 {
		extras = append(extras, "env=["+strings.Join(s.Env, ", ")+"]")
	}
	suffix := ""
	if len(extras) > 0 {
		suffix = " (" + strings.Join(extras, ", ") + ")"
	}
	fmt.Fprintf(w, "    %d. %s%s\n", n, s.Name, suffix)
	fmt.Fprintf(w, "       %s\n", indentCommand(audit.RedactDisplay(s.Command, secrets)))
}

// newRunID generates a unique run identifier using crypto/rand.
// Format: "run_" followed by 8 random hex characters (e.g. "run_a3f1c2d4").
func newRunID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback to timestamp-based ID if crypto/rand is unavailable.
		return fmt.Sprintf("run_%x", time.Now().UnixNano())
	}
	return "run_" + hex.EncodeToString(b[:])
}

// indentCommand returns the first line of a command, truncated for display.
func indentCommand(cmd string) string {
	line, _, _ := strings.Cut(cmd, "\n")
	if len(line) > 80 {
		line = line[:77] + "..."
	}
	return line
}
