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
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/runbookdev/runbook/internal/ast"
	"github.com/runbookdev/runbook/internal/audit"
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
	RunSuccess         RunStatus = iota // exit 0
	RunStepFailed                       // exit 1
	RunRolledBack                       // exit 2
	RunValidationError                  // exit 3
	RunCheckFailed                      // exit 4
	RunAborted         RunStatus = 10   // exit 10
	RunInternalError   RunStatus = 20   // exit 20
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
}

// runContext holds the shared state for an in-progress execution,
// allowing the check and step loops to be extracted from Run.
type runContext struct {
	ctx            context.Context
	opts           RunOptions
	start          time.Time
	tree           *ast.RunbookAST
	exec           *StepExecutor
	rollbackEngine *RollbackEngine
	rollbackMap    map[string]ast.RollbackNode
	result         *RunResult
	sigCh          <-chan SignalAction
	logStepAudit   func(sr *StepResult, blockType, command string)
	logDebug       func(format string, args ...any)
	stderr         io.Writer
	promptInput    io.Reader
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
	content, err := os.ReadFile(opts.FilePath)
	if err != nil {
		return fail(result, RunInternalError, fmt.Sprintf("%s: %v", opts.FilePath, err), start)
	}

	tree, err := parser.Parse(opts.FilePath, string(content))
	if err != nil {
		return fail(result, RunInternalError, err.Error(), start)
	}
	logDebug("parsed %s: %d checks, %d steps, %d rollbacks, %d waits",
		opts.FilePath, len(tree.Checks), len(tree.Steps), len(tree.Rollbacks), len(tree.Waits))

	// --- INIT: Validate ---
	fmt.Fprintf(stderr, "[runbook] validating\n")
	valErrs := validator.Validate(tree)
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
	if err := resolver.Resolve(tree, opts.Env, opts.Vars, opts.EnvFile); err != nil {
		return fail(result, RunInternalError, err.Error(), start)
	}

	// --- DRY RUN ---
	if opts.DryRun {
		printDryRun(stderr, tree, opts.Env)
		result.Status = RunSuccess
		result.Phase = PhaseComplete
		result.Duration = time.Since(start)
		return result
	}

	// --- AUDIT: Start run record ---
	al := opts.AuditLogger
	runID := uuid.New().String()
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
		})
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

	exec := &StepExecutor{Shell: shell, WorkDir: workDir, Env: opts.Vars, Stdout: stdout, Stderr: stderr}

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
		logDebug: logDebug, stderr: stderr, promptInput: promptInput,
	}

	// --- CHECKING ---
	if r := rc.executeChecks(); r != nil {
		return r
	}

	// --- RUNNING ---
	if r := rc.executeSteps(); r != nil {
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
			return handleSignal(rc.ctx, *action, rc.result, rc.rollbackEngine, rc.start)
		}

		fmt.Fprintf(rc.stderr, "[runbook] check [%d/%d] %q\n", i+1, len(rc.tree.Checks), check.Name)
		rc.logDebug("check command: %s", indentCommand(check.Command))
		checkStart := time.Now()
		sr, err := rc.exec.Run(rc.ctx, "check:"+check.Name, check.Command, 0)
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

// executeSteps runs all step blocks sequentially with rollback tracking.
// Returns non-nil to short-circuit the caller.
func (rc *runContext) executeSteps() *RunResult {
	rc.result.Phase = PhaseRunning
	fmt.Fprintf(rc.stderr, "[runbook] running %d steps\n", len(rc.tree.Steps))

	for i, step := range rc.tree.Steps {
		if action := pollSignal(rc.sigCh); action != nil {
			return handleSignal(rc.ctx, *action, rc.result, rc.rollbackEngine, rc.start)
		}

		if r := rc.handleConfirmGate(step); r != nil {
			if r == skipSentinel {
				continue
			}
			return r
		}

		stepTimeout := parseDuration(step.Timeout)

		fmt.Fprintf(rc.stderr, "[runbook] step [%d/%d] %q\n", i+1, len(rc.tree.Steps), step.Name)
		rc.logDebug("step command: %s", indentCommand(step.Command))
		if stepTimeout > 0 {
			rc.logDebug("step timeout: %s", stepTimeout)
		}

		stepStart := time.Now()
		sr, err := rc.exec.Run(rc.ctx, step.Name, step.Command, stepTimeout)
		if err != nil {
			return fail(rc.result, RunInternalError,
				fmt.Sprintf("%s:%d: step %q: %v", rc.opts.FilePath, step.Line, step.Name, err), rc.start)
		}
		rc.result.StepResults = append(rc.result.StepResults, *sr)
		rc.logStepAudit(sr, "step", step.Command)
		rc.logDebug("step %q finished in %s (exit %d)", step.Name, time.Since(stepStart).Round(time.Millisecond), sr.ExitCode)

		if sr.Status == StatusSuccess {
			if step.Rollback != "" {
				if rb, ok := rc.rollbackMap[step.Rollback]; ok {
					rc.rollbackEngine.Push(rb.Name, rb.Command)
					rc.logDebug("pushed rollback %q onto stack (depth: %d)", rb.Name, rc.rollbackEngine.Len())
				}
			}
			continue
		}

		// Step failed or timed out — initiate rollback.
		fmt.Fprintf(rc.stderr, "[runbook] %s:%d: step %q %s (exit code %d)\n",
			rc.opts.FilePath, step.Line, step.Name, sr.Status, sr.ExitCode)
		rc.result.Phase = PhaseRollingBack
		report := rc.rollbackEngine.Execute(rc.ctx, "step_failure")
		rc.result.RollbackReport = report
		if report.Succeeded > 0 || report.Failed > 0 {
			rc.result.Status = RunRolledBack
		} else {
			rc.result.Status = RunStepFailed
		}
		rc.result.Duration = time.Since(rc.start)
		return rc.result
	}
	return nil
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

	action := promptConfirm(rc.stderr, rc.promptInput, step.Name, rc.opts.Env)
	switch action {
	case ConfirmYes:
		return nil
	case ConfirmSkip:
		fmt.Fprintf(rc.stderr, "[runbook] step %q: skipped by user\n", step.Name)
		return skipSentinel
	case ConfirmAbort:
		return handleSignal(rc.ctx, ActionQuit, rc.result, rc.rollbackEngine, rc.start)
	default: // ConfirmNo
		rc.result.Status = RunStepFailed
		rc.result.Error = fmt.Sprintf("%s:%d: step %q: user declined confirmation", rc.opts.FilePath, step.Line, step.Name)
		rc.result.Phase = PhaseRollingBack
		report := rc.rollbackEngine.Execute(rc.ctx, "user_declined")
		rc.result.RollbackReport = report
		if report.Failed > 0 {
			rc.result.Status = RunStepFailed
		} else if rc.rollbackEngine.Len() == 0 && report.Succeeded > 0 {
			rc.result.Status = RunRolledBack
		}
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

// promptConfirm displays a confirmation gate and reads the user's response.
func promptConfirm(w io.Writer, r io.Reader, stepName, env string) ConfirmAction {
	fmt.Fprintf(w, "\n[runbook] step %q requires confirmation for %q\n", stepName, env)
	fmt.Fprintf(w, "  [y]es   — execute this step\n")
	fmt.Fprintf(w, "  [n]o    — skip and rollback\n")
	fmt.Fprintf(w, "  [s]kip  — skip this step, continue with next\n")
	fmt.Fprintf(w, "  [a]bort — stop immediately\n")
	fmt.Fprintf(w, "  Confirm [y/n/s/a]: ")

	scanner := bufio.NewScanner(r)
	if scanner.Scan() {
		return parseConfirmAction(scanner.Text())
	}
	return ConfirmNo
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
func printDryRun(w io.Writer, tree *ast.RunbookAST, env string) {
	fmt.Fprintf(w, "\n[dry-run] Runbook: %s (v%s)\n", tree.Metadata.Name, tree.Metadata.Version)
	if env != "" {
		fmt.Fprintf(w, "[dry-run] Environment: %s\n", env)
	}
	if tree.Metadata.Timeout != "" {
		fmt.Fprintf(w, "[dry-run] Global timeout: %s\n", tree.Metadata.Timeout)
	}

	if len(tree.Checks) > 0 {
		fmt.Fprintf(w, "\n[dry-run] Checks (%d):\n", len(tree.Checks))
		for i, c := range tree.Checks {
			fmt.Fprintf(w, "  %d. %s\n", i+1, c.Name)
			fmt.Fprintf(w, "     %s\n", indentCommand(c.Command))
		}
	}

	if len(tree.Steps) > 0 {
		fmt.Fprintf(w, "\n[dry-run] Steps (%d):\n", len(tree.Steps))
		for i, s := range tree.Steps {
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
			if len(s.Env) > 0 {
				extras = append(extras, "env=["+strings.Join(s.Env, ", ")+"]")
			}
			suffix := ""
			if len(extras) > 0 {
				suffix = " (" + strings.Join(extras, ", ") + ")"
			}
			fmt.Fprintf(w, "  %d. %s%s\n", i+1, s.Name, suffix)
			fmt.Fprintf(w, "     %s\n", indentCommand(s.Command))
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

// indentCommand returns the first line of a command, truncated for display.
func indentCommand(cmd string) string {
	line, _, _ := strings.Cut(cmd, "\n")
	if len(line) > 80 {
		line = line[:77] + "..."
	}
	return line
}
