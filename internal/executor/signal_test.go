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
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseAction(t *testing.T) {
	tests := []struct {
		input string
		want  SignalAction
	}{
		{"r", ActionRollback},
		{"R", ActionRollback},
		{"rollback", ActionRollback},
		{"ROLLBACK", ActionRollback},
		{"", ActionRollback}, // default
		{"anything", ActionRollback},
		{"c", ActionContinue},
		{"C", ActionContinue},
		{"continue", ActionContinue},
		{"CONTINUE", ActionContinue},
		{"q", ActionQuit},
		{"Q", ActionQuit},
		{"quit", ActionQuit},
		{"QUIT", ActionQuit},
		{"  r  ", ActionRollback},
		{"  c  ", ActionContinue},
		{"  q  ", ActionQuit},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseAction(tt.input)
			if got != tt.want {
				t.Errorf("parseAction(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSignalHandler_PromptRollback(t *testing.T) {
	var output bytes.Buffer
	input := strings.NewReader("r\n")

	h := &SignalHandler{
		Input:         input,
		Output:        &output,
		PromptTimeout: 5 * time.Second,
		sigCh:         make(chan os.Signal, 1),
		actionCh:      make(chan SignalAction, 1),
		done:          make(chan struct{}),
	}

	action := h.prompt()
	if action != ActionRollback {
		t.Errorf("expected rollback, got %s", action)
	}
	if !strings.Contains(output.String(), "interrupt received") {
		t.Errorf("expected prompt text, got %q", output.String())
	}
}

func TestSignalHandler_PromptContinue(t *testing.T) {
	var output bytes.Buffer
	input := strings.NewReader("c\n")

	h := &SignalHandler{
		Input:         input,
		Output:        &output,
		PromptTimeout: 5 * time.Second,
		sigCh:         make(chan os.Signal, 1),
		actionCh:      make(chan SignalAction, 1),
		done:          make(chan struct{}),
	}

	action := h.prompt()
	if action != ActionContinue {
		t.Errorf("expected continue, got %s", action)
	}
}

func TestSignalHandler_PromptQuit(t *testing.T) {
	var output bytes.Buffer
	input := strings.NewReader("q\n")

	h := &SignalHandler{
		Input:         input,
		Output:        &output,
		PromptTimeout: 5 * time.Second,
		sigCh:         make(chan os.Signal, 1),
		actionCh:      make(chan SignalAction, 1),
		done:          make(chan struct{}),
	}

	action := h.prompt()
	if action != ActionQuit {
		t.Errorf("expected quit, got %s", action)
	}
}

func TestSignalHandler_PromptTimeout(t *testing.T) {
	var output bytes.Buffer
	// Use a reader that blocks forever (never provides input).
	r, _ := io.Pipe()
	defer r.Close()

	h := &SignalHandler{
		Input:         r,
		Output:        &output,
		PromptTimeout: 200 * time.Millisecond,
		sigCh:         make(chan os.Signal, 1),
		actionCh:      make(chan SignalAction, 1),
		done:          make(chan struct{}),
	}

	start := time.Now()
	action := h.prompt()
	elapsed := time.Since(start)

	if action != ActionRollback {
		t.Errorf("expected rollback on timeout, got %s", action)
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("expected ~200ms timeout, got %s", elapsed)
	}
	if !strings.Contains(output.String(), "defaulting to rollback") {
		t.Errorf("expected timeout message, got %q", output.String())
	}
}

func TestSignalHandler_PromptEmptyDefault(t *testing.T) {
	var output bytes.Buffer
	input := strings.NewReader("\n")

	h := &SignalHandler{
		Input:         input,
		Output:        &output,
		PromptTimeout: 5 * time.Second,
		sigCh:         make(chan os.Signal, 1),
		actionCh:      make(chan SignalAction, 1),
		done:          make(chan struct{}),
	}

	action := h.prompt()
	if action != ActionRollback {
		t.Errorf("expected rollback on empty input, got %s", action)
	}
}

func TestSignalHandler_StartStop(t *testing.T) {
	h := NewSignalHandler()
	ch := h.Start()

	if ch == nil {
		t.Fatal("expected non-nil action channel")
	}

	// Stop should not panic.
	h.Stop()
	// Double stop should not panic.
	h.Stop()
}

func TestSignalHandler_SignalTriggersPrompt(t *testing.T) {
	var output bytes.Buffer
	input := strings.NewReader("q\n")

	h := &SignalHandler{
		Input:         input,
		Output:        &output,
		PromptTimeout: 5 * time.Second,
	}

	actionCh := h.Start()
	defer h.Stop()

	// Simulate a SIGINT by sending to the signal channel.
	h.sigCh <- syscall.SIGINT

	select {
	case action := <-actionCh:
		if action != ActionQuit {
			t.Errorf("expected quit, got %s", action)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for action")
	}
}

func TestSignalActionString(t *testing.T) {
	tests := []struct {
		action SignalAction
		want   string
	}{
		{ActionRollback, "rollback"},
		{ActionContinue, "continue"},
		{ActionQuit, "quit"},
		{SignalAction(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.action.String(); got != tt.want {
			t.Errorf("SignalAction(%d).String() = %q, want %q", tt.action, got, tt.want)
		}
	}
}

func TestRollbackStatusString(t *testing.T) {
	tests := []struct {
		status RollbackStatus
		want   string
	}{
		{RollbackSuccess, "success"},
		{RollbackFailed, "failed"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("RollbackStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}
