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
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/fatih/color"
)

// ReportFormat selects the report serialisation.
type ReportFormat string

// Supported report formats.
const (
	// ReportText renders a human-readable summary table.
	ReportText ReportFormat = "text"
	// ReportJSON renders a machine-readable report suitable for
	// downstream tooling.
	ReportJSON ReportFormat = "json"
)

// reportJSON is the wire-format for JSON reports. Keeping it distinct
// from BulkResult lets us evolve the in-memory shape without breaking
// consumers of the JSON output.
type reportJSON struct {
	// DurationMS is the overall bulk duration in milliseconds.
	DurationMS int64 `json:"duration_ms"`
	// Runs mirrors BulkResult.Runs, one entry per file.
	Runs []reportRunJSON `json:"runs"`
	// Summary holds the aggregate counters.
	Summary reportSummaryJSON `json:"summary"`
	// ExitCode is the highest-severity per-run exit code.
	ExitCode int `json:"exit_code"`
}

type reportRunJSON struct {
	File       string            `json:"file"`
	Label      string            `json:"label,omitempty"`
	Vars       map[string]string `json:"vars,omitempty"`
	Status     string            `json:"status"`
	ExitCode   int               `json:"exit_code"`
	DurationMS int64             `json:"duration_ms"`
	Error      string            `json:"error,omitempty"`
}

type reportSummaryJSON struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// WriteReport writes a summary of b to w in the requested format.
// Unknown formats fall back to text so callers never silently drop
// output.
func WriteReport(w io.Writer, b *BulkResult, format ReportFormat) error {
	if b == nil {
		return nil
	}

	switch format {
	case ReportJSON:
		return writeJSON(w, b)
	default:
		return writeText(w, b)
	}
}

// writeJSON emits the JSON report. Indentation matches the style used
// by `runbook env --json` so consumers see a consistent layout.
func writeJSON(w io.Writer, b *BulkResult) error {
	runs := make([]reportRunJSON, len(b.Runs))
	total, success := len(b.Runs), 0
	for i, r := range b.Runs {
		runs[i] = reportRunJSON{
			File:       r.FilePath,
			Label:      labelOrEmpty(r),
			Vars:       r.Vars,
			Status:     string(r.Status),
			ExitCode:   r.ExitCode,
			DurationMS: r.Duration.Milliseconds(),
			Error:      r.Error,
		}
		if r.ExitCode == 0 && r.Status != StatusSkipped {
			success++
		}
	}

	out := reportJSON{
		DurationMS: b.Duration.Milliseconds(),
		Runs:       runs,
		Summary: reportSummaryJSON{
			Total:   total,
			Success: success,
			Failed:  b.FailedCount,
			Skipped: b.SkippedCount,
		},
		ExitCode: b.ExitCode(),
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// writeText renders the human-readable summary: one line per run with
// a status glyph, then an aggregate line. Colours follow the same
// palette as printRunSummary so the two reports feel consistent when
// they appear in the same terminal.
func writeText(w io.Writer, b *BulkResult) error {
	bold := color.New(color.Bold)
	green := color.New(color.FgGreen, color.Bold)
	red := color.New(color.FgRed, color.Bold)
	yellow := color.New(color.FgYellow, color.Bold)
	dim := color.New(color.Faint)

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	if _, err := bold.Fprintln(w, "── Bulk summary ─────────────────────────────────"); err != nil {
		return err
	}

	for _, r := range b.Runs {
		glyph, colour := statusGlyph(r)
		dur := dim.Sprintf("(%s)", r.Duration.Round(time.Millisecond))
		display := displayName(r)
		if _, err := fmt.Fprintf(w, "  %s %s  %s %s\n",
			colour.Sprint(glyph), display, string(r.Status), dur); err != nil {
			return err
		}

		if r.Error != "" {
			if _, err := dim.Fprintf(w, "       %s\n", r.Error); err != nil {
				return err
			}
		}
	}

	overall := fmt.Sprintf("%d total, %d failed, %d skipped",
		len(b.Runs), b.FailedCount, b.SkippedCount)
	totalDur := dim.Sprintf("(%s)", b.Duration.Round(time.Millisecond))

	var summaryColour *color.Color
	var label string
	switch {
	case b.FailedCount > 0:
		summaryColour, label = red, "FAILED"
	case b.SkippedCount > 0:
		summaryColour, label = yellow, "PARTIAL"
	default:
		summaryColour, label = green, "SUCCESS"
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	if _, err := summaryColour.Fprintf(w, "  Result: %s — %s %s\n", label, overall, totalDur); err != nil {
		return err
	}

	if _, err := bold.Fprintln(w, "─────────────────────────────────────────────────"); err != nil {
		return err
	}
	return nil
}

// displayName picks the most informative user-facing identifier for a
// Result's row in the text report. Matrix runs (those with a non-empty
// Vars binding) show the Label because the label embeds the binding
// and is how identical file paths are told apart. Non-matrix runs show
// the raw FilePath — matching phase-1 behaviour and preserving the
// directory context that gets lost when Label is just the base name.
func displayName(r Result) string {
	if len(r.Vars) > 0 && r.Label != "" {
		return r.Label
	}
	return r.FilePath
}

// labelOrEmpty returns the Label field, or "" when Label equals the
// file's base name with no matrix suffix. Keeps the JSON output
// compact by omitting redundant labels while still including
// matrix-row labels that carry extra information.
func labelOrEmpty(r Result) string {
	if r.Label == "" {
		return ""
	}
	if len(r.Vars) == 0 {
		// No matrix binding — the label would just be the file's
		// base name, duplicating the file field. Omit.
		return ""
	}
	return r.Label
}

// statusGlyph maps a per-run result to its display glyph and colour.
func statusGlyph(r Result) (string, *color.Color) {
	switch {
	case r.Status == StatusSkipped:
		return "·", color.New(color.Faint)
	case r.ExitCode == 0:
		return "✓", color.New(color.FgGreen, color.Bold)
	default:
		return "✗", color.New(color.FgRed, color.Bold)
	}
}
