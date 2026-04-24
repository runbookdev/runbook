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
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleResult() *BulkResult {
	return &BulkResult{
		Runs: []Result{
			{FilePath: "ok.runbook", Status: "success", ExitCode: 0, Duration: 10 * time.Millisecond},
			{FilePath: "bad.runbook", Status: "step_failed", ExitCode: 1, Duration: 20 * time.Millisecond, Error: "boom"},
			{FilePath: "pending.runbook", Status: StatusSkipped},
		},
		Duration:     40 * time.Millisecond,
		FailedCount:  1,
		SkippedCount: 1,
	}
}

func TestWriteReport_JSONShape(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteReport(&buf, sampleResult(), ReportJSON); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	var got reportJSON
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n---\n%s", err, buf.String())
	}

	if len(got.Runs) != 3 {
		t.Fatalf("want 3 runs, got %d", len(got.Runs))
	}

	if got.Summary.Total != 3 || got.Summary.Failed != 1 || got.Summary.Skipped != 1 || got.Summary.Success != 1 {
		t.Errorf("summary mismatch: %+v", got.Summary)
	}

	if got.ExitCode != 1 {
		t.Errorf("exit_code = %d, want 1", got.ExitCode)
	}

	if got.DurationMS != 40 {
		t.Errorf("duration_ms = %d, want 40", got.DurationMS)
	}
}

func TestWriteReport_TextContainsFilesAndResult(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteReport(&buf, sampleResult(), ReportText); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"ok.runbook", "bad.runbook", "pending.runbook", "FAILED", "boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("text report missing %q\n---\n%s", want, out)
		}
	}
}

func TestWriteReport_TextShowsFilePathForNonMatrixRuns(t *testing.T) {
	// Phase-1 behaviour: sequential bulk shows the full FilePath so
	// operators can tell apart runbooks that share a base name across
	// directories. Matrix runs (with Vars) show the Label instead.
	r := &BulkResult{
		Runs: []Result{
			{FilePath: "/projects/a/deploy.runbook", Label: "deploy", Status: "success"},
			{FilePath: "deploy.runbook", Label: "deploy[env=prod]", Vars: map[string]string{"env": "prod"}, Status: "success"},
		},
		Duration: time.Millisecond,
	}
	var buf bytes.Buffer
	if err := WriteReport(&buf, r, ReportText); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "/projects/a/deploy.runbook") {
		t.Errorf("non-matrix row should show full FilePath, got %q", out)
	}

	if !strings.Contains(out, "deploy[env=prod]") {
		t.Errorf("matrix row should show Label, got %q", out)
	}
}

func TestWriteReport_TextSuccessLabel(t *testing.T) {
	r := &BulkResult{
		Runs:     []Result{{FilePath: "ok.runbook", Status: "success", ExitCode: 0}},
		Duration: 5 * time.Millisecond,
	}
	var buf bytes.Buffer
	if err := WriteReport(&buf, r, ReportText); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	if !strings.Contains(buf.String(), "SUCCESS") {
		t.Errorf("expected SUCCESS label, got %q", buf.String())
	}
}

func TestWriteReport_UnknownFormatFallsBackToText(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteReport(&buf, sampleResult(), ReportFormat("xml")); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	if !strings.Contains(buf.String(), "Bulk summary") {
		t.Errorf("expected text fallback, got %q", buf.String())
	}
}

func TestWriteReport_NilResult(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteReport(&buf, nil, ReportJSON); err != nil {
		t.Fatalf("WriteReport nil: %v", err)
	}

	if buf.Len() != 0 {
		t.Errorf("nil result should produce no output, got %q", buf.String())
	}
}
