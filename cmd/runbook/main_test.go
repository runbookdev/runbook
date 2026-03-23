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

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWarnIfRoot_PrintsWarningWhenUIDIsZero(t *testing.T) {
	var buf bytes.Buffer
	warnIfRoot(0, &buf)

	out := buf.String()
	if !strings.Contains(out, "WARNING: runbook is running as root") {
		t.Errorf("expected root warning, got %q", out)
	}
	if !strings.Contains(out, "full system privileges") {
		t.Errorf("expected privilege message, got %q", out)
	}
	if !strings.Contains(out, "non-root user") {
		t.Errorf("expected non-root suggestion, got %q", out)
	}
}

func TestWarnIfRoot_SilentForNonRootUID(t *testing.T) {
	var buf bytes.Buffer
	warnIfRoot(1000, &buf)

	if buf.Len() != 0 {
		t.Errorf("expected no output for non-root uid, got %q", buf.String())
	}
}

func TestWarnIfRoot_SilentForUID1(t *testing.T) {
	var buf bytes.Buffer
	warnIfRoot(1, &buf)

	if buf.Len() != 0 {
		t.Errorf("expected no output for uid 1, got %q", buf.String())
	}
}
