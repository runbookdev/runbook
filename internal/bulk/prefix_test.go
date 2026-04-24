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
	"strings"
	"sync"
	"testing"
)

func TestPrefixWriter_FullLines(t *testing.T) {
	var buf bytes.Buffer
	pw := newPrefixWriter("a", &buf)
	if _, err := pw.Write([]byte("hello\nworld\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := "[a] hello\n[a] world\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrefixWriter_PartialLineBuffered(t *testing.T) {
	var buf bytes.Buffer
	pw := newPrefixWriter("run", &buf)

	if _, err := pw.Write([]byte("partial")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("partial line should stay buffered, got %q", buf.String())
	}

	if _, err := pw.Write([]byte(" line\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := "[run] partial line\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrefixWriter_FlushAddsNewline(t *testing.T) {
	var buf bytes.Buffer
	pw := newPrefixWriter("x", &buf)
	_, _ = pw.Write([]byte("no newline"))
	if err := pw.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	want := "[x] no newline\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrefixWriter_FlushPreservesExistingNewline(t *testing.T) {
	var buf bytes.Buffer
	pw := newPrefixWriter("x", &buf)
	// Partial line ending with \n inside the buffer should flush cleanly.
	// Normal Write already handles trailing \n; Flush is a no-op then.
	_, _ = pw.Write([]byte("done\n"))
	if err := pw.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if got, want := buf.String(), "[x] done\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrefixWriter_ConcurrentWritersSameSink(t *testing.T) {
	// Two prefix writers share a single downstream buffer. No line
	// should be split across prefix boundaries even under interleaved
	// writes — prefixWriter holds its own mutex across label+payload.
	var buf bytes.Buffer
	var sink lockedBuffer
	sink.w = &buf

	a := newPrefixWriter("a", &sink)
	b := newPrefixWriter("b", &sink)

	done := make(chan struct{})
	go func() {
		for range 50 {
			_, _ = a.Write([]byte("aaa\n"))
		}
		done <- struct{}{}
	}()

	go func() {
		for range 50 {
			_, _ = b.Write([]byte("bbb\n"))
		}
		done <- struct{}{}
	}()
	<-done
	<-done

	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line != "[a] aaa" && line != "[b] bbb" {
			t.Errorf("line got interleaved or mis-labelled: %q", line)
		}
	}
}

// lockedBuffer serializes writes to an inner buffer the way the
// coordinator's shared stdout/stderr would (bytes.Buffer itself is
// not safe for concurrent writers).
type lockedBuffer struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
