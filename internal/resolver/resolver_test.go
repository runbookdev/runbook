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

package resolver

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	rbast "github.com/runbookdev/runbook/internal/ast"
)

// noopOpts returns an Options that suppresses I/O for tests that don't care
// about metacharacter scanning output.
func noopOpts() Options {
	return Options{NonInteractive: true, Stderr: os.Stderr}
}

func TestResolvePriorityOrder(t *testing.T) {
	// Set up .env file with lowest-priority value.
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("region=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Set RUNBOOK_REGION env var (medium priority).
	t.Setenv("RUNBOOK_REGION", "from-env")

	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test", Version: "1.0"},
		Steps: []rbast.StepNode{
			{Name: "deploy", Command: "deploy --region={{region}}", Line: 10},
		},
	}

	// CLI flags should win over env and dotenv.
	cliVars := map[string]string{"region": "from-cli"}
	if err := Resolve(ast, "production", cliVars, envFile, noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ast.Steps[0].Command != "deploy --region=from-cli" {
		t.Errorf("expected CLI flag to win, got %q", ast.Steps[0].Command)
	}
	if ast.Steps[0].OriginalCommand != "deploy --region={{region}}" {
		t.Errorf("original command not preserved, got %q", ast.Steps[0].OriginalCommand)
	}
}

func TestResolveEnvVarOverridesDotEnv(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("region=from-dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("RUNBOOK_REGION", "from-env")

	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test", Version: "1.0"},
		Steps: []rbast.StepNode{
			{Name: "deploy", Command: "deploy --region={{region}}", Line: 10},
		},
	}

	if err := Resolve(ast, "production", nil, envFile, noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ast.Steps[0].Command != "deploy --region=from-env" {
		t.Errorf("expected env var to override dotenv, got %q", ast.Steps[0].Command)
	}
}

func TestResolveBuiltins(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "my-runbook", Version: "2.0"},
		Steps: []rbast.StepNode{
			{Name: "info", Command: "echo {{runbook_name}} {{runbook_version}} {{env}}", Line: 5},
		},
	}

	if err := Resolve(ast, "staging", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ast.Steps[0].Command != "echo my-runbook 2.0 staging" {
		t.Errorf("built-ins not resolved, got %q", ast.Steps[0].Command)
	}
}

func TestResolveBuiltinUser(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test"},
		Steps: []rbast.StepNode{
			{Name: "whoami", Command: "echo {{user}}", Line: 1},
		},
	}

	if err := Resolve(ast, "", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be non-empty (resolved to current user).
	if strings.Contains(ast.Steps[0].Command, "{{user}}") {
		t.Error("{{user}} was not resolved")
	}
}

func TestResolveBuiltinHostname(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test"},
		Steps: []rbast.StepNode{
			{Name: "host", Command: "echo {{hostname}}", Line: 1},
		},
	}

	if err := Resolve(ast, "", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(ast.Steps[0].Command, "{{hostname}}") {
		t.Error("{{hostname}} was not resolved")
	}
}

func TestResolveBuiltinCwd(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test"},
		Steps: []rbast.StepNode{
			{Name: "pwd", Command: "echo {{cwd}}", Line: 1},
		},
	}

	if err := Resolve(ast, "", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(ast.Steps[0].Command, "{{cwd}}") {
		t.Error("{{cwd}} was not resolved")
	}
}

func TestResolveBuiltinRunID(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test"},
		Steps: []rbast.StepNode{
			{Name: "id", Command: "echo {{run_id}}", Line: 1},
		},
	}

	if err := Resolve(ast, "", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(ast.Steps[0].Command, "{{run_id}}") {
		t.Error("{{run_id}} was not resolved")
	}
}

func TestResolveBuiltinTimestamp(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test"},
		Steps: []rbast.StepNode{
			{Name: "ts", Command: "echo {{timestamp}}", Line: 1},
		},
	}

	if err := Resolve(ast, "", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(ast.Steps[0].Command, "{{timestamp}}") {
		t.Error("{{timestamp}} was not resolved")
	}
}

func TestResolveUnresolvedVariable(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "deploy.runbook",
		Metadata: rbast.Metadata{Name: "deploy"},
		Steps: []rbast.StepNode{
			{Name: "deploy-app", Command: "deploy --cluster={{cluster_name}}", Line: 42},
		},
	}

	err := Resolve(ast, "production", nil, "", noopOpts())
	if err == nil {
		t.Fatal("expected error for unresolved variable")
	}
	expected := `deploy.runbook:42: unresolved variable {{cluster_name}} in step "deploy-app"`
	if err.Error() != expected {
		t.Errorf("unexpected error message:\n  got:  %s\n  want: %s", err.Error(), expected)
	}
}

func TestResolveEnvFiltering(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test", Version: "1.0"},
		Steps: []rbast.StepNode{
			{Name: "staging-only", Command: "echo staging", Env: []string{"staging"}, Line: 1},
			{Name: "prod-only", Command: "echo prod", Env: []string{"production"}, Line: 5},
			{Name: "all-envs", Command: "echo all", Line: 10},
			{Name: "multi-env", Command: "echo multi", Env: []string{"staging", "production"}, Line: 15},
		},
	}

	if err := Resolve(ast, "staging", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := make([]string, len(ast.Steps))
	for i, s := range ast.Steps {
		names[i] = s.Name
	}

	want := []string{"staging-only", "all-envs", "multi-env"}
	if len(names) != len(want) {
		t.Fatalf("expected %d steps, got %d: %v", len(want), len(names), names)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("step[%d] = %q, want %q", i, names[i], w)
		}
	}
}

func TestResolveDotEnvFileParsing(t *testing.T) {
	envContent := `DB_HOST=localhost
DB_PORT=5432
DB_NAME=mydb
`
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}

	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test", Version: "1.0"},
		Steps: []rbast.StepNode{
			{Name: "connect", Command: "psql -h {{DB_HOST}} -p {{DB_PORT}} {{DB_NAME}}", Line: 1},
		},
	}

	if err := Resolve(ast, "", nil, envFile, noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ast.Steps[0].Command != "psql -h localhost -p 5432 mydb" {
		t.Errorf("dotenv not resolved, got %q", ast.Steps[0].Command)
	}
}

func TestResolveAllBlockTypes(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test", Version: "1.0"},
		Checks: []rbast.CheckNode{
			{Name: "pre", Command: "check {{env}}", Line: 1},
		},
		Steps: []rbast.StepNode{
			{Name: "run", Command: "run {{env}}", Line: 5},
		},
		Rollbacks: []rbast.RollbackNode{
			{Name: "undo", Command: "undo {{env}}", Line: 10},
		},
		Waits: []rbast.WaitNode{
			{Name: "pause", Command: "wait {{env}}", Duration: "30s", Line: 15},
		},
	}

	if err := Resolve(ast, "staging", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ast.Checks[0].Command != "check staging" {
		t.Errorf("check not resolved: %q", ast.Checks[0].Command)
	}
	if ast.Checks[0].OriginalCommand != "check {{env}}" {
		t.Errorf("check original not preserved: %q", ast.Checks[0].OriginalCommand)
	}
	if ast.Steps[0].Command != "run staging" {
		t.Errorf("step not resolved: %q", ast.Steps[0].Command)
	}
	if ast.Rollbacks[0].Command != "undo staging" {
		t.Errorf("rollback not resolved: %q", ast.Rollbacks[0].Command)
	}
	if ast.Rollbacks[0].OriginalCommand != "undo {{env}}" {
		t.Errorf("rollback original not preserved: %q", ast.Rollbacks[0].OriginalCommand)
	}
	if ast.Waits[0].Command != "wait staging" {
		t.Errorf("wait not resolved: %q", ast.Waits[0].Command)
	}
	if ast.Waits[0].OriginalCommand != "wait {{env}}" {
		t.Errorf("wait original not preserved: %q", ast.Waits[0].OriginalCommand)
	}
}

func TestResolveMultipleVariablesInOneCommand(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test", Version: "1.0"},
		Steps: []rbast.StepNode{
			{Name: "deploy", Command: "deploy {{app}} to {{region}} in {{env}}", Line: 1},
		},
	}

	cliVars := map[string]string{"app": "myapp", "region": "us-east-1"}
	if err := Resolve(ast, "production", cliVars, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ast.Steps[0].Command != "deploy myapp to us-east-1 in production" {
		t.Errorf("multiple vars not resolved, got %q", ast.Steps[0].Command)
	}
}

func TestResolveNoVariables(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test"},
		Steps: []rbast.StepNode{
			{Name: "simple", Command: "echo hello", Line: 1},
		},
	}

	if err := Resolve(ast, "", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ast.Steps[0].Command != "echo hello" {
		t.Errorf("command changed: %q", ast.Steps[0].Command)
	}
}

func TestResolveEnvFilteringCaseInsensitive(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test"},
		Steps: []rbast.StepNode{
			{Name: "s1", Command: "echo ok", Env: []string{"Production"}, Line: 1},
		},
	}

	if err := Resolve(ast, "production", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ast.Steps) != 1 {
		t.Error("case-insensitive env matching failed")
	}
}

func TestResolveInvalidEnvFile(t *testing.T) {
	err := Resolve(
		&rbast.RunbookAST{FilePath: "test.runbook", Metadata: rbast.Metadata{Name: "test"}},
		"", nil, "/nonexistent/.env", noopOpts(),
	)
	if err == nil {
		t.Fatal("expected error for invalid env file path")
	}
}

func TestEnvProvider(t *testing.T) {
	t.Setenv("RUNBOOK_API_KEY", "secret123")

	p := &EnvProvider{}
	if p.Name() != "env" {
		t.Errorf("unexpected name: %s", p.Name())
	}

	val, err := p.Resolve("API_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "secret123" {
		t.Errorf("unexpected value: %s", val)
	}

	_, err = p.Resolve("MISSING")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestDotEnvProvider(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("MY_KEY=my_value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := NewDotEnvProvider(envFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "dotenv" {
		t.Errorf("unexpected name: %s", p.Name())
	}

	val, err := p.Resolve("MY_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "my_value" {
		t.Errorf("unexpected value: %s", val)
	}

	_, err = p.Resolve("MISSING")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestDotEnvProviderInvalidFile(t *testing.T) {
	_, err := NewDotEnvProvider("/nonexistent/.env")
	if err == nil {
		t.Fatal("expected error for invalid file")
	}
}

func TestResolveEmptyTargetEnv(t *testing.T) {
	ast := &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test"},
		Steps: []rbast.StepNode{
			{Name: "s1", Command: "echo ok", Env: []string{"production"}, Line: 1},
			{Name: "s2", Command: "echo all", Line: 5},
		},
	}

	// With empty target env, no filtering should happen.
	if err := Resolve(ast, "", nil, "", noopOpts()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ast.Steps) != 2 {
		t.Errorf("expected 2 steps (no filtering), got %d", len(ast.Steps))
	}
}

// --- .env permission warning tests ---

func TestWarnDotEnvPermissions_WarnsWhenTooPermissive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SECRET=abc\n"), 0o644); err != nil { //nolint:gosec // intentionally permissive for permission-warning test
		t.Fatal(err)
	}

	var buf bytes.Buffer
	warnDotEnvPermissions(envFile, &buf)

	out := buf.String()
	if !strings.Contains(out, "0644") {
		t.Errorf("expected permission 0644 in warning, got %q", out)
	}
	if !strings.Contains(out, "chmod 600") {
		t.Errorf("expected chmod 600 suggestion, got %q", out)
	}
}

func TestWarnDotEnvPermissions_SilentWhenCorrectPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("SECRET=abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	warnDotEnvPermissions(envFile, &buf)

	if buf.Len() != 0 {
		t.Errorf("expected no warning for 0600 .env file, got %q", buf.String())
	}
}

func TestResolve_DotEnvPermissionWarningEmittedToStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	// Write .env with world-readable permissions.
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("region=us-east-1\n"), 0o644); err != nil { //nolint:gosec // intentionally permissive for permission-warning test
		t.Fatal(err)
	}

	ast := minimalAST()
	var stderr bytes.Buffer
	opts := Options{NonInteractive: true, Stderr: &stderr}

	if err := Resolve(ast, "", nil, envFile, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(stderr.String(), "chmod 600") {
		t.Errorf("expected .env permission warning in stderr, got %q", stderr.String())
	}
}

func TestResolve_DotEnvNoWarningForSecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("region=us-east-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ast := minimalAST()
	var stderr bytes.Buffer
	opts := Options{NonInteractive: true, Stderr: &stderr}

	if err := Resolve(ast, "", nil, envFile, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(stderr.String(), "chmod 600") {
		t.Errorf("unexpected permission warning for secure .env, got %q", stderr.String())
	}
}

// minimalAST returns a bare-minimum AST with no template variables.
func minimalAST() *rbast.RunbookAST {
	return &rbast.RunbookAST{
		FilePath: "test.runbook",
		Metadata: rbast.Metadata{Name: "test", Version: "1.0"},
		Steps:    []rbast.StepNode{{Name: "step1", Command: "echo hello", Line: 10}},
	}
}
