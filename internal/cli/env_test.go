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

package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runbookdev/runbook/internal/cli"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func runEnvCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := cli.New()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"env"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func writeTmpRunbook(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// ── TestEnvHuman ──────────────────────────────────────────────────────────────

func TestEnvHumanShowsProjectType(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runEnvCmd(t, d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Go project") {
		t.Errorf("expected 'Go project' in output; got:\n%s", out)
	}
}

func TestEnvHumanNoRunbooks(t *testing.T) {
	d := t.TempDir()
	out, err := runEnvCmd(t, d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "none found") {
		t.Errorf("expected 'none found' in output; got:\n%s", out)
	}
}

func TestEnvHumanShowsRunbooksAndEnvironments(t *testing.T) {
	d := t.TempDir()
	writeTmpRunbook(t, d, "deploy.runbook", `---
name: Deploy
environments:
  - staging
  - production
---
`)

	out, err := runEnvCmd(t, d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "deploy.runbook") {
		t.Errorf("expected 'deploy.runbook' in output; got:\n%s", out)
	}
	if !strings.Contains(out, "staging") {
		t.Errorf("expected 'staging' in output; got:\n%s", out)
	}
	if !strings.Contains(out, "production") {
		t.Errorf("expected 'production' in output; got:\n%s", out)
	}
}

// ── TestEnvJSON ───────────────────────────────────────────────────────────────

func TestEnvJSONStructure(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTmpRunbook(t, d, "deploy.runbook", `---
name: Deploy API
environments:
  - staging
  - production
requires:
  tools:
    - kubectl
    - jq
---
`)
	writeTmpRunbook(t, d, "rollback.runbook", `---
name: Rollback API
environments:
  - production
requires:
  tools:
    - kubectl
---
`)

	out, err := runEnvCmd(t, "--json", d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result struct {
		ProjectType string `json:"project_type"`
		Runbooks    []struct {
			File         string   `json:"file"`
			Name         string   `json:"name"`
			Environments []string `json:"environments"`
		} `json:"runbooks"`
		Tools struct {
			Required []string `json:"required"`
			Found    []string `json:"found"`
			Missing  []string `json:"missing"`
		} `json:"tools"`
		Environments []string `json:"environments"`
	}

	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("JSON parse error: %v\noutput was:\n%s", err, out)
	}

	if result.ProjectType != "go" {
		t.Errorf("project_type = %q; want %q", result.ProjectType, "go")
	}

	if len(result.Runbooks) != 2 {
		t.Fatalf("len(runbooks) = %d; want 2", len(result.Runbooks))
	}

	// Tools: required must contain kubectl and jq (deduped).
	requiredSet := make(map[string]bool)
	for _, tool := range result.Tools.Required {
		requiredSet[tool] = true
	}
	for _, want := range []string{"kubectl", "jq"} {
		if !requiredSet[want] {
			t.Errorf("required tools missing %q; got %v", want, result.Tools.Required)
		}
	}

	// Environments: sorted, deduped.
	envSet := make(map[string]bool)
	for _, e := range result.Environments {
		envSet[e] = true
	}
	for _, want := range []string{"staging", "production"} {
		if !envSet[want] {
			t.Errorf("environments missing %q; got %v", want, result.Environments)
		}
	}

	// Arrays must not be null (JSON null vs []).
	if result.Tools.Found == nil {
		t.Error("tools.found should not be null")
	}
	if result.Tools.Missing == nil {
		t.Error("tools.missing should not be null")
	}
}

func TestEnvJSONEmptyDirectory(t *testing.T) {
	d := t.TempDir()

	out, err := runEnvCmd(t, "--json", d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result struct {
		Runbooks []any `json:"runbooks"`
		Tools    struct {
			Required []string `json:"required"`
			Found    []string `json:"found"`
			Missing  []string `json:"missing"`
		} `json:"tools"`
		Environments []string `json:"environments"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("JSON parse error: %v\noutput:\n%s", err, out)
	}

	if result.Runbooks == nil {
		t.Error("runbooks should be [] not null")
	}
	if result.Tools.Required == nil {
		t.Error("tools.required should be [] not null")
	}
	if result.Environments == nil {
		t.Error("environments should be [] not null")
	}
}

func TestEnvJSONRunbookFields(t *testing.T) {
	d := t.TempDir()
	writeTmpRunbook(t, d, "deploy.runbook", `---
name: Deploy API
environments:
  - staging
  - production
---
`)

	out, err := runEnvCmd(t, "--json", d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result struct {
		Runbooks []struct {
			File         string   `json:"file"`
			Name         string   `json:"name"`
			Environments []string `json:"environments"`
		} `json:"runbooks"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("JSON parse error: %v", err)
	}
	if len(result.Runbooks) != 1 {
		t.Fatalf("len(runbooks) = %d; want 1", len(result.Runbooks))
	}
	rb := result.Runbooks[0]
	if rb.File != "deploy.runbook" {
		t.Errorf("file = %q; want %q", rb.File, "deploy.runbook")
	}
	if rb.Name != "Deploy API" {
		t.Errorf("name = %q; want %q", rb.Name, "Deploy API")
	}
	wantEnvs := map[string]bool{"staging": true, "production": true}
	for _, e := range rb.Environments {
		if !wantEnvs[e] {
			t.Errorf("unexpected env %q in runbook", e)
		}
	}
}
