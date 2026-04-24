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
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseMatrixVar_ValidInput(t *testing.T) {
	ax, err := ParseMatrixVar("env=staging,prod")
	if err != nil {
		t.Fatalf("ParseMatrixVar: %v", err)
	}

	if ax.Key != "env" {
		t.Errorf("key = %q, want %q", ax.Key, "env")
	}

	want := []string{"staging", "prod"}
	if !reflect.DeepEqual(ax.Values, want) {
		t.Errorf("values = %v, want %v", ax.Values, want)
	}
}

func TestParseMatrixVar_TrimsWhitespace(t *testing.T) {
	ax, err := ParseMatrixVar(" region = us , eu , ap ")
	if err != nil {
		t.Fatalf("ParseMatrixVar: %v", err)
	}

	if ax.Key != "region" {
		t.Errorf("key = %q, want %q", ax.Key, "region")
	}

	want := []string{"us", "eu", "ap"}
	if !reflect.DeepEqual(ax.Values, want) {
		t.Errorf("values = %v, want %v", ax.Values, want)
	}
}

func TestParseMatrixVar_Errors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"no equals", "envstaging"},
		{"empty key", "=staging,prod"},
		{"no values", "env="},
		{"whitespace-only values", "env= , , "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMatrixVar(tc.in)
			if err == nil {
				t.Fatalf("want error for input %q, got nil", tc.in)
			}
		})
	}
}

func TestMatrix_Expand_Cartesian(t *testing.T) {
	m := Matrix{
		Axes: []Axis{
			{Key: "env", Values: []string{"staging", "prod"}},
			{Key: "region", Values: []string{"us", "eu"}},
		},
	}
	rows, err := m.Expand()
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	// Expected order: first axis varies slowest, last axis varies
	// fastest — so env=staging rows come first, then env=prod.
	want := []Binding{
		{"env": "staging", "region": "us"},
		{"env": "staging", "region": "eu"},
		{"env": "prod", "region": "us"},
		{"env": "prod", "region": "eu"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("Expand rows:\n  got  %v\n  want %v", rows, want)
	}
}

func TestMatrix_Expand_SingleAxis(t *testing.T) {
	m := Matrix{
		Axes: []Axis{{Key: "tenant", Values: []string{"a", "b", "c"}}},
	}
	rows, err := m.Expand()
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	if len(rows) != 3 {
		t.Errorf("want 3 rows, got %d", len(rows))
	}

	if rows[0]["tenant"] != "a" || rows[2]["tenant"] != "c" {
		t.Errorf("unexpected ordering: %v", rows)
	}
}

func TestMatrix_Expand_ExcludeDropsSupersets(t *testing.T) {
	m := Matrix{
		Axes: []Axis{
			{Key: "env", Values: []string{"staging", "prod"}},
			{Key: "region", Values: []string{"us", "eu"}},
		},
		Exclude: []Binding{
			{"env": "staging", "region": "eu"},
		},
	}
	rows, err := m.Expand()
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	// 4 cartesian rows minus 1 exclude = 3.
	if len(rows) != 3 {
		t.Errorf("want 3 rows after exclude, got %d: %v", len(rows), rows)
	}

	for _, r := range rows {
		if r["env"] == "staging" && r["region"] == "eu" {
			t.Errorf("excluded row %v should have been dropped", r)
		}
	}
}

func TestMatrix_Expand_ExcludePartialKey(t *testing.T) {
	// A one-key exclude drops every row matching that single key.
	m := Matrix{
		Axes: []Axis{
			{Key: "env", Values: []string{"staging", "prod"}},
			{Key: "region", Values: []string{"us", "eu"}},
		},
		Exclude: []Binding{{"env": "staging"}},
	}
	rows, err := m.Expand()
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	if len(rows) != 2 {
		t.Errorf("want 2 rows (all env=prod), got %d: %v", len(rows), rows)
	}

	for _, r := range rows {
		if r["env"] != "prod" {
			t.Errorf("expected env=prod only, got %v", r)
		}
	}
}

func TestMatrix_Expand_IncludeAppends(t *testing.T) {
	m := Matrix{
		Axes: []Axis{
			{Key: "env", Values: []string{"staging"}},
		},
		Include: []Binding{
			{"env": "prod", "region": "ap", "canary": "true"},
		},
	}
	rows, err := m.Expand()
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	if len(rows) != 2 {
		t.Errorf("want 2 rows (1 axis + 1 include), got %d: %v", len(rows), rows)
	}

	// Include row is appended last.
	last := rows[len(rows)-1]
	if last["canary"] != "true" || last["region"] != "ap" {
		t.Errorf("include row not preserved verbatim: %v", last)
	}
}

func TestMatrix_Expand_RejectsEmpty(t *testing.T) {
	_, err := Matrix{}.Expand()
	if err == nil {
		t.Fatal("empty matrix should error, got nil")
	}
}

func TestMatrix_Expand_RejectsDuplicateAxis(t *testing.T) {
	m := Matrix{
		Axes: []Axis{
			{Key: "env", Values: []string{"staging"}},
			{Key: "env", Values: []string{"prod"}},
		},
	}
	_, err := m.Expand()
	if err == nil {
		t.Fatal("duplicate axis key should error, got nil")
	}

	if !strings.Contains(err.Error(), "env") {
		t.Errorf("error should name the duplicate axis, got %v", err)
	}
}

func TestMatrix_Expand_EmptyExcludeIsNoOp(t *testing.T) {
	// An empty Exclude entry is a stub/catch-all that would otherwise
	// drop every row. Matrix files may carry one while an operator is
	// editing them; treat it as a no-op instead of wiping the sweep.
	m := Matrix{
		Axes:    []Axis{{Key: "env", Values: []string{"staging", "prod"}}},
		Exclude: []Binding{{}, {"env": "prod"}},
	}
	rows, err := m.Expand()
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	if len(rows) != 1 || rows[0]["env"] != "staging" {
		t.Errorf("want 1 row env=staging, got %v", rows)
	}
}

func TestMatrix_Expand_RejectsOversizedExpansion(t *testing.T) {
	// Six axes of ten values each would yield 1M rows — well past
	// MaxMatrixRows. Expand must reject before allocating.
	axes := make([]Axis, 6)
	for i := range axes {
		vals := make([]string, 10)
		for j := range vals {
			vals[j] = fmt.Sprintf("v%d", j)
		}
		axes[i] = Axis{Key: fmt.Sprintf("k%d", i), Values: vals}
	}

	_, err := Matrix{Axes: axes}.Expand()
	if err == nil {
		t.Fatal("oversized matrix should error, got nil")
	}

	if !strings.Contains(err.Error(), "exceed") && !strings.Contains(err.Error(), "cap") {
		t.Errorf("error should mention the cap, got %v", err)
	}
}

func TestMatrix_Expand_RejectsAllExcluded(t *testing.T) {
	m := Matrix{
		Axes:    []Axis{{Key: "env", Values: []string{"staging"}}},
		Exclude: []Binding{{"env": "staging"}},
	}
	_, err := m.Expand()
	if err == nil {
		t.Fatal("matrix that excludes all rows should error, got nil")
	}
}

func TestParseMatrixFile_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "matrix.yaml")
	src := `axes:
  env: [staging, prod]
  region: [us, eu]
include:
  - env: prod
    region: ap
exclude:
  - env: staging
    region: eu
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	m, err := ParseMatrixFile(path)
	if err != nil {
		t.Fatalf("ParseMatrixFile: %v", err)
	}

	// Axes must be in declaration order: env first, then region.
	if len(m.Axes) != 2 || m.Axes[0].Key != "env" || m.Axes[1].Key != "region" {
		t.Errorf("axes order not preserved: %+v", m.Axes)
	}

	if len(m.Include) != 1 || m.Include[0]["region"] != "ap" {
		t.Errorf("include not parsed correctly: %v", m.Include)
	}

	if len(m.Exclude) != 1 || m.Exclude[0]["region"] != "eu" {
		t.Errorf("exclude not parsed correctly: %v", m.Exclude)
	}

	rows, err := m.Expand()
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	// 2×2 cartesian = 4, minus 1 exclude = 3, plus 1 include = 4.
	if len(rows) != 4 {
		t.Errorf("want 4 expanded rows, got %d: %v", len(rows), rows)
	}
}

func TestParseMatrixFile_Errors(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		content string
	}{
		{"missing axes", "include:\n  - env: prod\n"},
		{"empty axis values", "axes:\n  env: []\n"},
		{"axes not a mapping", "axes:\n  - env\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".yaml")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("setup: %v", err)
			}

			if _, err := ParseMatrixFile(path); err == nil {
				t.Fatalf("expected error for %q, got nil", tc.name)
			}
		})
	}
}

func TestFormatLabel(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		binding Binding
		want    string
	}{
		{"no binding", "deploy", nil, "deploy"},
		{"empty binding", "deploy", Binding{}, "deploy"},
		{"single", "deploy", Binding{"env": "prod"}, "deploy[env=prod]"},
		{"sorted keys", "deploy", Binding{"region": "us", "env": "prod"}, "deploy[env=prod,region=us]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatLabel(tt.base, tt.binding); got != tt.want {
				t.Errorf("FormatLabel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildJobs_NoBindings(t *testing.T) {
	jobs := BuildJobs([]string{"a.runbook", "b.runbook"}, nil)

	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}

	if jobs[0].FilePath != "a.runbook" || jobs[1].FilePath != "b.runbook" {
		t.Errorf("unexpected file paths: %v, %v", jobs[0].FilePath, jobs[1].FilePath)
	}

	if jobs[0].Label != "a" || jobs[1].Label != "b" {
		t.Errorf("labels should strip .runbook suffix, got %q / %q", jobs[0].Label, jobs[1].Label)
	}

	if jobs[0].Vars != nil {
		t.Errorf("no-binding jobs should have nil Vars, got %v", jobs[0].Vars)
	}
}

func TestBuildJobs_CrossProduct(t *testing.T) {
	files := []string{"deploy.runbook", "verify.runbook"}
	bindings := []Binding{
		{"env": "staging"},
		{"env": "prod"},
	}
	jobs := BuildJobs(files, bindings)

	// 2 files × 2 bindings = 4 jobs. Outer loop is files, inner is
	// bindings, so order is deploy-staging, deploy-prod, verify-...
	if len(jobs) != 4 {
		t.Fatalf("want 4 jobs, got %d", len(jobs))
	}

	wantLabels := []string{
		"deploy[env=staging]",
		"deploy[env=prod]",
		"verify[env=staging]",
		"verify[env=prod]",
	}
	for i, want := range wantLabels {
		if jobs[i].Label != want {
			t.Errorf("job %d label = %q, want %q", i, jobs[i].Label, want)
		}
	}

	// Vars must be independent copies — mutating one shouldn't leak.
	jobs[0].Vars["env"] = "mutated"
	if jobs[2].Vars["env"] == "mutated" {
		t.Error("BuildJobs should clone Vars per-job to prevent aliasing")
	}
}

func TestBuildJobs_EmptyFiles(t *testing.T) {
	if jobs := BuildJobs(nil, []Binding{{"env": "prod"}}); len(jobs) != 0 {
		t.Errorf("no files should yield no jobs, got %d", len(jobs))
	}
}
