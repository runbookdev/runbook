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
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// SignalAction represents the user's chosen response to an interrupt.
type SignalAction int

const (
	// ActionRollback stops execution and runs the pushed rollback handlers.
	ActionRollback SignalAction = iota
	// ActionContinue resumes execution as if no interrupt occurred.
	ActionContinue
	// ActionQuit stops execution immediately without running rollbacks.
	ActionQuit
)

// Labels returned by SignalAction.String.
const (
	signalLabelRollback = "rollback"
	signalLabelContinue = "continue"
	signalLabelQuit     = "quit"
	signalLabelUnknown  = "unknown"
)

// Interactive prompt tokens parsed from the interrupt-response prompt.
const (
	signalInputRollback    = "r"
	signalInputRollbackAlt = "rollback"
	signalInputContinue    = "c"
	signalInputContinueAlt = "continue"
	signalInputQuit        = "q"
	signalInputQuitAlt     = "quit"
)

// signalPromptTimeout is how long to wait for user input before defaulting
// to rollback.
const signalPromptTimeout = 10 * time.Second

// SignalHandler intercepts SIGINT and prompts the user for an action.
type SignalHandler struct {
	// Input is where user responses are read from (default: os.Stdin).
	Input io.Reader
	// Output is where prompts are written (default: os.Stderr).
	Output io.Writer
	// PromptTimeout overrides the default 10s timeout for testing.
	PromptTimeout time.Duration
	// sigCh receives OS signals (set during Start).
	sigCh chan os.Signal
	// actionCh delivers the user's chosen action to the caller.
	actionCh chan SignalAction
	// done is closed when Stop is called.
	done chan struct{}
}

// String returns the lowercase label of the signal action.
func (a SignalAction) String() string {
	switch a {
	case ActionRollback:
		return signalLabelRollback
	case ActionContinue:
		return signalLabelContinue
	case ActionQuit:
		return signalLabelQuit
	default:
		return signalLabelUnknown
	}
}

// NewSignalHandler creates a SignalHandler with default stdin/stderr.
func NewSignalHandler() *SignalHandler {
	return &SignalHandler{
		Input:         os.Stdin,
		Output:        os.Stderr,
		PromptTimeout: signalPromptTimeout,
	}
}

// Start begins listening for SIGINT. Returns a channel that delivers a
// SignalAction when the user responds to the prompt (or the timeout fires).
// Call Stop() when execution is complete to clean up.
func (h *SignalHandler) Start() <-chan SignalAction {
	h.sigCh = make(chan os.Signal, 1)
	h.actionCh = make(chan SignalAction, 1)
	h.done = make(chan struct{})

	signal.Notify(h.sigCh, syscall.SIGINT)

	go h.loop()

	return h.actionCh
}

// Stop cleans up signal handling. Safe to call multiple times.
func (h *SignalHandler) Stop() {
	if h.sigCh != nil {
		signal.Stop(h.sigCh)
	}
	select {
	case <-h.done:
		// Already closed.
	default:
		close(h.done)
	}
}

// loop runs in a goroutine, waiting for signals and prompting the user.
func (h *SignalHandler) loop() {
	for {
		select {
		case <-h.done:
			return
		case <-h.sigCh:
			action := h.prompt()
			// Non-blocking send; if the channel already has a value,
			// we discard (first signal wins).
			select {
			case h.actionCh <- action:
			default:
			}
			// If the user chose continue, keep listening.
			// Otherwise, exit the loop.
			if action != ActionContinue {
				return
			}
		}
	}
}

// prompt writes the prompt and reads the user's response. Returns
// ActionRollback if no valid input is received within the timeout.
func (h *SignalHandler) prompt() SignalAction {
	out := h.Output
	if out == nil {
		out = os.Stderr
	}
	in := h.Input
	if in == nil {
		in = os.Stdin
	}

	timeout := h.PromptTimeout
	if timeout <= 0 {
		timeout = signalPromptTimeout
	}

	fmt.Fprintf(out, "\n[runbook] interrupt received. Choose action:\n")
	fmt.Fprintf(out, "  [r]ollback — stop and roll back completed steps (default)\n")
	fmt.Fprintf(out, "  [c]ontinue — resume execution\n")
	fmt.Fprintf(out, "  [q]uit     — stop immediately, no rollback\n")
	fmt.Fprintf(out, "  Action [r/c/q] (timeout %s): ", timeout)

	responseCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		if scanner.Scan() {
			responseCh <- strings.TrimSpace(scanner.Text())
		} else {
			responseCh <- ""
		}
	}()

	select {
	case response := <-responseCh:
		return parseAction(response)
	case <-time.After(timeout):
		fmt.Fprintf(out, "\n[runbook] no response, defaulting to rollback\n")
		return ActionRollback
	case <-h.done:
		return ActionRollback
	}
}

// parseAction converts a user response string to a SignalAction.
// Empty input and anything unrecognized default to ActionRollback (the safe default).
func parseAction(s string) SignalAction {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case signalInputContinue, signalInputContinueAlt:
		return ActionContinue
	case signalInputQuit, signalInputQuitAlt:
		return ActionQuit
	case signalInputRollback, signalInputRollbackAlt:
		return ActionRollback
	default:
		return ActionRollback
	}
}
