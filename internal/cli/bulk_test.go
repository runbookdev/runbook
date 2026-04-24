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

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectBulkFiles_PositionalAndGlobDedupe(t *testing.T) {
	dir := t.TempDir()
	// Create two real runbook files and one unrelated file.
	for _, name := range []string{"a.runbook", "b.runbook", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("---\nname: x\n---\n"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	aPath := filepath.Join(dir, "a.runbook")
	bPath := filepath.Join(dir, "b.runbook")

	// Pass a.runbook twice: once positional, once via glob. The glob
	// also matches b.runbook. We expect each file to appear once in
	// first-seen order (a then b).
	files, err := collectBulkFiles(
		[]string{aPath},
		[]string{filepath.Join(dir, "*.runbook")},
	)
	if err != nil {
		t.Fatalf("collectBulkFiles: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d: %v", len(files), files)
	}

	if files[0] != aPath {
		t.Errorf("want %q first (positional order preserved), got %q", aPath, files[0])
	}

	if files[1] != bPath {
		t.Errorf("want %q second, got %q", bPath, files[1])
	}
}

func TestCollectBulkFiles_MissingReported(t *testing.T) {
	_, err := collectBulkFiles([]string{"/does/not/exist.runbook"}, nil)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("want 'not found' in error, got %v", err)
	}
}

func TestCollectBulkFiles_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := collectBulkFiles([]string{dir}, nil)
	if err == nil {
		t.Fatal("expected error when arg is a directory, got nil")
	}
}

func TestCollectBulkFiles_InvalidGlob(t *testing.T) {
	_, err := collectBulkFiles(nil, []string{"[unclosed"})
	if err == nil {
		t.Fatal("expected glob syntax error, got nil")
	}

	if !strings.Contains(err.Error(), "invalid glob") {
		t.Errorf("want 'invalid glob' in error, got %v", err)
	}
}

func TestBuildMatrixBindings_NoFlags(t *testing.T) {
	b, err := buildMatrixBindings("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if b != nil {
		t.Errorf("want nil bindings when no flags set, got %v", b)
	}
}

func TestBuildMatrixBindings_InlineOnly(t *testing.T) {
	b, err := buildMatrixBindings("", []string{"env=staging,prod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(b) != 2 {
		t.Errorf("want 2 bindings, got %d: %v", len(b), b)
	}
}

func TestBuildMatrixBindings_FileAndInlineLayered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.yaml")
	src := "axes:\n  env: [staging, prod]\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// File supplies env (2 values); inline adds region (2 values).
	// Expected: 2×2 = 4 bindings.
	b, err := buildMatrixBindings(path, []string{"region=us,eu"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(b) != 4 {
		t.Errorf("want 4 bindings (2×2), got %d: %v", len(b), b)
	}
}

func TestBuildMatrixBindings_InvalidInline(t *testing.T) {
	if _, err := buildMatrixBindings("", []string{"malformed"}); err == nil {
		t.Fatal("expected error for malformed matrix-var, got nil")
	}
}

func TestBuildMatrixBindings_RejectsDuplicateAxis(t *testing.T) {
	// Inline --matrix-var env=a --matrix-var env=b both append to
	// Axes; Expand must catch the duplicate and error.
	_, err := buildMatrixBindings("", []string{"env=staging", "env=prod"})
	if err == nil {
		t.Fatal("duplicate axis keys should error, got nil")
	}

	if !strings.Contains(err.Error(), "env") {
		t.Errorf("error should name the duplicate, got %v", err)
	}
}

func TestParseReportFormat(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantEr bool
	}{
		{"", "text", false},
		{"text", "text", false},
		{"json", "json", false},
		{"yaml", "", true},
	}
	for _, tt := range tests {
		got, err := parseReportFormat(tt.in)
		if (err != nil) != tt.wantEr {
			t.Errorf("parseReportFormat(%q) err = %v, wantErr %v", tt.in, err, tt.wantEr)
			continue
		}

		if !tt.wantEr && string(got) != tt.want {
			t.Errorf("parseReportFormat(%q) = %q, want %q", tt.in, string(got), tt.want)
		}
	}
}
