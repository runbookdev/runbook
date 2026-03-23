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

package detect_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runbookdev/runbook/internal/detect"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// tmpDir creates a temporary directory and returns its path. It is removed
// automatically when the test ends.
func tmpDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "detect-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// touch creates an empty file at path (creating parent directories as needed).
func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create %s: %v", path, err)
	}
	_ = f.Close()
}

// writeFile writes content to path (creating parent directories as needed).
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// ── TestDetectProjectType ─────────────────────────────────────────────────────

func TestDetectProjectType(t *testing.T) {
	cases := []struct {
		name  string
		setup func(dir string)
		want  string
	}{
		{
			name:  "go",
			setup: func(d string) { touch(t, filepath.Join(d, "go.mod")) },
			want:  "go",
		},
		{
			name:  "nodejs_npm",
			setup: func(d string) { touch(t, filepath.Join(d, "package.json")) },
			want:  "nodejs",
		},
		{
			name: "nodejs_yarn",
			setup: func(d string) {
				touch(t, filepath.Join(d, "package.json"))
				touch(t, filepath.Join(d, "yarn.lock"))
			},
			want: "nodejs",
		},
		{
			name: "nodejs_pnpm",
			setup: func(d string) {
				touch(t, filepath.Join(d, "package.json"))
				touch(t, filepath.Join(d, "pnpm-lock.yaml"))
			},
			want: "nodejs",
		},
		{
			name:  "rust",
			setup: func(d string) { touch(t, filepath.Join(d, "Cargo.toml")) },
			want:  "rust",
		},
		{
			name:  "python_pyproject",
			setup: func(d string) { touch(t, filepath.Join(d, "pyproject.toml")) },
			want:  "python",
		},
		{
			name:  "python_requirements",
			setup: func(d string) { touch(t, filepath.Join(d, "requirements.txt")) },
			want:  "python",
		},
		{
			name:  "docker",
			setup: func(d string) { touch(t, filepath.Join(d, "Dockerfile")) },
			want:  "docker",
		},
		{
			name:  "docker_compose_yml",
			setup: func(d string) { touch(t, filepath.Join(d, "docker-compose.yml")) },
			want:  "docker-compose",
		},
		{
			name:  "docker_compose_yaml",
			setup: func(d string) { touch(t, filepath.Join(d, "docker-compose.yaml")) },
			want:  "docker-compose",
		},
		{
			name:  "make",
			setup: func(d string) { touch(t, filepath.Join(d, "Makefile")) },
			want:  "make",
		},
		{
			name:  "terraform",
			setup: func(d string) { touch(t, filepath.Join(d, "main.tf")) },
			want:  "terraform",
		},
		{
			name:  "kubernetes_dir",
			setup: func(d string) { _ = os.Mkdir(filepath.Join(d, "kubernetes"), 0o755) },
			want:  "kubernetes",
		},
		{
			name:  "kubernetes_k8s",
			setup: func(d string) { _ = os.Mkdir(filepath.Join(d, "k8s"), 0o755) },
			want:  "kubernetes",
		},
		{
			name:  "github_actions",
			setup: func(d string) { _ = os.MkdirAll(filepath.Join(d, ".github", "workflows"), 0o755) },
			want:  "github-actions",
		},
		{
			name:  "jenkins",
			setup: func(d string) { touch(t, filepath.Join(d, "Jenkinsfile")) },
			want:  "jenkins",
		},
		{
			name:  "unknown",
			setup: func(d string) {},
			want:  "unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tmpDir(t)
			tc.setup(d)
			info := detect.DetectProject(d)
			if info.ProjectType != tc.want {
				t.Errorf("ProjectType = %q; want %q", info.ProjectType, tc.want)
			}
		})
	}
}

// ── TestDetectProjectDisplayName ─────────────────────────────────────────────

func TestDetectProjectDisplayName(t *testing.T) {
	d := tmpDir(t)
	touch(t, filepath.Join(d, "go.mod"))
	info := detect.DetectProject(d)
	if info.DisplayName() != "Go project" {
		t.Errorf("DisplayName() = %q; want %q", info.DisplayName(), "Go project")
	}
}

// ── TestScanRunbooks ──────────────────────────────────────────────────────────

const frontmatterA = `---
name: Deploy API
environments:
  - staging
  - production
requires:
  tools:
    - kubectl
    - jq
---
# body
`

const frontmatterB = `---
name: Rollback API
environments:
  - production
requires:
  tools:
    - kubectl
    - helm
---
# body
`

func TestScanRunbooks(t *testing.T) {
	d := tmpDir(t)
	writeFile(t, filepath.Join(d, "deploy.runbook"), frontmatterA)
	writeFile(t, filepath.Join(d, "rollback.runbook"), frontmatterB)

	info := detect.DetectProject(d)

	// Two runbooks found.
	if got := len(info.RunbookFiles); got != 2 {
		t.Fatalf("len(RunbookFiles) = %d; want 2", got)
	}

	// Names are extracted.
	nameMap := make(map[string]string)
	for _, rb := range info.RunbookFiles {
		nameMap[rb.File] = rb.Name
	}
	if nameMap["deploy.runbook"] != "Deploy API" {
		t.Errorf("deploy.runbook name = %q; want %q", nameMap["deploy.runbook"], "Deploy API")
	}
	if nameMap["rollback.runbook"] != "Rollback API" {
		t.Errorf("rollback.runbook name = %q; want %q", nameMap["rollback.runbook"], "Rollback API")
	}

	// Environments are deduplicated and sorted.
	wantEnvs := []string{"production", "staging"}
	if got := len(info.Environments); got != len(wantEnvs) {
		t.Fatalf("len(Environments) = %d; want %d", got, len(wantEnvs))
	}
	for i, e := range wantEnvs {
		if info.Environments[i] != e {
			t.Errorf("Environments[%d] = %q; want %q", i, info.Environments[i], e)
		}
	}

	// Required tools are aggregated (deduped kubectl from both files).
	wantTools := map[string]bool{"kubectl": true, "jq": true, "helm": true}
	if got := len(info.Tools.Required); got != len(wantTools) {
		t.Errorf("len(Tools.Required) = %d; want %d", got, len(wantTools))
	}
	for _, tool := range info.Tools.Required {
		if !wantTools[tool] {
			t.Errorf("unexpected required tool %q", tool)
		}
	}
}

func TestScanRunbooksEmpty(t *testing.T) {
	d := tmpDir(t)
	info := detect.DetectProject(d)

	if len(info.RunbookFiles) != 0 {
		t.Errorf("expected no runbooks, got %d", len(info.RunbookFiles))
	}
	if len(info.Environments) != 0 {
		t.Errorf("expected no environments, got %v", info.Environments)
	}
	if len(info.Tools.Required) != 0 {
		t.Errorf("expected no required tools, got %v", info.Tools.Required)
	}
}

func TestScanRunbooksFiveFiles(t *testing.T) {
	// Verify that 5 .runbook files with overlapping data are correctly aggregated.
	d := tmpDir(t)
	templates := []string{
		`---
name: Step A
environments: [alpha, beta]
requires:
  tools: [kubectl, jq]
---
`,
		`---
name: Step B
environments: [beta, gamma]
requires:
  tools: [jq, helm]
---
`,
		`---
name: Step C
environments: [gamma]
requires:
  tools: [helm]
---
`,
		`---
name: Step D
environments: [alpha]
---
`,
		`---
name: Step E
requires:
  tools: [kubectl]
---
`,
	}
	for i, content := range templates {
		writeFile(t, filepath.Join(d, "step"+string(rune('a'+i))+".runbook"), content)
	}

	info := detect.DetectProject(d)

	if got := len(info.RunbookFiles); got != 5 {
		t.Fatalf("len(RunbookFiles) = %d; want 5", got)
	}

	// Environments: alpha, beta, gamma (sorted, deduped).
	wantEnvs := []string{"alpha", "beta", "gamma"}
	if got := len(info.Environments); got != len(wantEnvs) {
		t.Fatalf("len(Environments) = %d; want %d (%v)", got, len(wantEnvs), info.Environments)
	}
	for i, e := range wantEnvs {
		if info.Environments[i] != e {
			t.Errorf("Environments[%d] = %q; want %q", i, info.Environments[i], e)
		}
	}

	// Tools: kubectl, jq, helm (deduped).
	wantTools := map[string]bool{"kubectl": true, "jq": true, "helm": true}
	if got := len(info.Tools.Required); got != len(wantTools) {
		t.Errorf("len(Tools.Required) = %d; want %d (%v)", got, len(wantTools), info.Tools.Required)
	}
}

// ── TestCheckTools ────────────────────────────────────────────────────────────

func TestCheckToolsAllPresent(t *testing.T) {
	// "true" and "false" are universally available POSIX utilities.
	report := detect.CheckTools([]string{"true", "false"})

	if len(report.Missing) != 0 {
		t.Errorf("expected no missing tools, got %v", report.Missing)
	}
	if len(report.Found) != 2 {
		t.Errorf("expected 2 found tools, got %v", report.Found)
	}
}

func TestCheckToolsMissing(t *testing.T) {
	report := detect.CheckTools([]string{"true", "__no_such_tool_xyz__"})

	if len(report.Missing) != 1 || report.Missing[0] != "__no_such_tool_xyz__" {
		t.Errorf("Missing = %v; want [__no_such_tool_xyz__]", report.Missing)
	}
	if len(report.Found) != 1 || report.Found[0] != "true" {
		t.Errorf("Found = %v; want [true]", report.Found)
	}
}

func TestCheckToolsEmpty(t *testing.T) {
	report := detect.CheckTools([]string{})
	if report.Required == nil {
		t.Error("Required should not be nil")
	}
	if report.Found == nil {
		t.Error("Found should not be nil")
	}
	if report.Missing == nil {
		t.Error("Missing should not be nil")
	}
}

func TestCheckToolsMockPath(t *testing.T) {
	// Create a temp dir with a fake tool binary and add it to PATH.
	bin := tmpDir(t)
	fakeTool := filepath.Join(bin, "my-fake-tool")
	f, err := os.Create(fakeTool)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Chmod(fakeTool, 0o755); err != nil {
		t.Fatal(err)
	}

	orig := os.Getenv("PATH")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+orig)

	report := detect.CheckTools([]string{"my-fake-tool", "__definitely_absent__"})

	found := make(map[string]bool)
	for _, t2 := range report.Found {
		found[t2] = true
	}
	missing := make(map[string]bool)
	for _, t2 := range report.Missing {
		missing[t2] = true
	}

	if !found["my-fake-tool"] {
		t.Errorf("expected my-fake-tool to be found; Found=%v", report.Found)
	}
	if !missing["__definitely_absent__"] {
		t.Errorf("expected __definitely_absent__ to be missing; Missing=%v", report.Missing)
	}
}
