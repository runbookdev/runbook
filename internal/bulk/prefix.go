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
	"io"
	"sync"
)

// prefixWriter tags every line written to it with a fixed label, then
// forwards the result to an underlying writer. Partial lines are buffered
// until a newline arrives so tags never split mid-line when parallel
// writers interleave.
type prefixWriter struct {
	// mu guards buf and serializes flushes to w.
	mu sync.Mutex
	// label is the string prepended to every emitted line, including
	// the trailing space separator (e.g. "[deploy.runbook] ").
	label []byte
	// buf accumulates bytes until a newline is seen.
	buf []byte
	// w is the shared downstream writer (typically serialized by a
	// mutex one layer up).
	w io.Writer
}

// newPrefixWriter returns a writer that prefixes each line with label.
// A trailing space is added automatically; label should not already end
// with one.
func newPrefixWriter(label string, w io.Writer) *prefixWriter {
	return &prefixWriter{
		label: []byte("[" + label + "] "),
		w:     w,
	}
}

// Write buffers p, splitting on newlines and flushing each complete
// line with the label prepended. Bytes after the last newline stay
// buffered until the next Write or Flush.
//
// Each emitted line is written to the downstream sink as a single
// p.w.Write call (label and payload concatenated) so two prefixWriters
// sharing a sink can't interleave mid-line — without that, writer A's
// label and writer B's label could both arrive before either's
// payload, producing `[a] [b] body` on the shared stream.
func (p *prefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(b)
	p.buf = append(p.buf, b...)
	for {
		idx := bytes.IndexByte(p.buf, '\n')
		if idx < 0 {
			break
		}

		line := p.buf[:idx+1]
		out := make([]byte, 0, len(p.label)+len(line))
		out = append(out, p.label...)
		out = append(out, line...)
		if _, err := p.w.Write(out); err != nil {
			return n, err
		}
		p.buf = p.buf[idx+1:]
	}
	return n, nil
}

// syncWriter serializes writes to an underlying writer. The bulk
// coordinator wraps the shared Stdout/Stderr in one of these before
// handing them to per-job prefixWriters, so two parallel workers
// writing into e.g. a bytes.Buffer (common in tests) or an unlocked
// file handle can't corrupt the sink's internal state — bytes.Buffer
// is explicitly not safe for concurrent goroutine writes, and even
// os.File.Write is only as atomic as the kernel's underlying write(2).
type syncWriter struct {
	// mu serializes Write calls to w.
	mu sync.Mutex
	// w is the downstream sink (os.Stdout, a bytes.Buffer in tests,
	// or any io.Writer the caller supplied).
	w io.Writer
}

// newSyncWriter returns w wrapped with a mutex. The returned writer
// is safe for concurrent Write calls from any number of goroutines.
func newSyncWriter(w io.Writer) *syncWriter {
	return &syncWriter{w: w}
}

// Write serializes each Write call against the mutex. Payloads are
// forwarded as a single call to the underlying sink — callers that
// need label+payload atomicity (e.g. prefixWriter) must already be
// emitting the whole tagged line in one Write.
func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// Flush emits any buffered partial line with the label prepended and
// a trailing newline appended. Callers must invoke Flush before the
// writer is discarded so no output is lost on runs that don't end in
// a newline. Uses a single downstream Write call for the same
// interleaving reason documented on Write.
func (p *prefixWriter) Flush() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.buf) == 0 {
		return nil
	}

	needsNewline := p.buf[len(p.buf)-1] != '\n'
	size := len(p.label) + len(p.buf)
	if needsNewline {
		size++
	}
	out := make([]byte, 0, size)
	out = append(out, p.label...)
	out = append(out, p.buf...)
	if needsNewline {
		out = append(out, '\n')
	}

	if _, err := p.w.Write(out); err != nil {
		return err
	}
	p.buf = p.buf[:0]
	return nil
}
