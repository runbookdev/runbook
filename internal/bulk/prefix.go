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
	// mu guards buf and serialises flushes to w.
	mu sync.Mutex
	// label is the string prepended to every emitted line, including
	// the trailing space separator (e.g. "[deploy.runbook] ").
	label []byte
	// buf accumulates bytes until a newline is seen.
	buf []byte
	// w is the shared downstream writer (typically serialised by a
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
		if _, err := p.w.Write(p.label); err != nil {
			return n, err
		}

		if _, err := p.w.Write(line); err != nil {
			return n, err
		}
		p.buf = p.buf[idx+1:]
	}
	return n, nil
}

// Flush emits any buffered partial line with the label prepended and
// a trailing newline appended. Callers must invoke Flush before the
// writer is discarded so no output is lost on runs that don't end in
// a newline.
func (p *prefixWriter) Flush() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.buf) == 0 {
		return nil
	}

	if _, err := p.w.Write(p.label); err != nil {
		return err
	}

	if _, err := p.w.Write(p.buf); err != nil {
		return err
	}

	if p.buf[len(p.buf)-1] != '\n' {
		if _, err := p.w.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	p.buf = p.buf[:0]
	return nil
}
