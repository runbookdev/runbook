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

package validator

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	rbast "github.com/runbookdev/runbook/internal/ast"
)

// helper to build a minimal valid AST.
func validAST() *rbast.RunbookAST {
	return &rbast.RunbookAST{
		Metadata: rbast.Metadata{Name: "Test Runbook"},
		Steps: []rbast.StepNode{
			{Name: "deploy", Command: "echo deploy", Line: 10},
		},
		FilePath: "test.runbook",
	}
}

func containsMessage(errs []ValidationError, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

func errorsWithSeverity(errs []ValidationError, sev Severity) []ValidationError {
	var result []ValidationError
	for _, e := range errs {
		if e.Severity == sev {
			result = append(result, e)
		}
	}
	return result
}

// --- V1: Unique block names ---

func TestV1_UniqueNames(t *testing.T) {
	tests := []struct {
		name    string
		ast     *rbast.RunbookAST
		wantErr bool
		substr  string
	}{
		{
			name:    "all unique names",
			ast:     validAST(),
			wantErr: false,
		},
		{
			name: "duplicate check and step name",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Checks:   []rbast.CheckNode{{Name: "deploy", Command: "true", Line: 5}},
				Steps:    []rbast.StepNode{{Name: "deploy", Command: "echo", Line: 10}},
			},
			wantErr: true,
			substr:  "duplicate block name \"deploy\"",
		},
		{
			name: "duplicate step and rollback name",
			ast: &rbast.RunbookAST{
				Metadata:  rbast.Metadata{Name: "Test"},
				Steps:     []rbast.StepNode{{Name: "cleanup", Command: "echo", Line: 5}},
				Rollbacks: []rbast.RollbackNode{{Name: "cleanup", Command: "echo", Line: 10}},
			},
			wantErr: true,
			substr:  "duplicate block name \"cleanup\"",
		},
		{
			name: "duplicate wait and check name",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Checks:   []rbast.CheckNode{{Name: "monitor", Command: "true", Line: 5}},
				Waits:    []rbast.WaitNode{{Name: "monitor", Duration: "30s", Command: "echo", Line: 10}},
			},
			wantErr: true,
			substr:  "duplicate block name \"monitor\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := v1UniqueNames(tt.ast)
			if tt.wantErr {
				if len(errs) == 0 {
					t.Fatal("expected error, got none")
				}
				if !containsMessage(errs, tt.substr) {
					t.Errorf("expected error containing %q, got %v", tt.substr, errs)
				}
			} else if len(errs) > 0 {
				t.Errorf("expected no errors, got %v", errs)
			}
		})
	}
}

// --- V2: Rollback references ---

func TestV2_RollbackRefs(t *testing.T) {
	tests := []struct {
		name    string
		ast     *rbast.RunbookAST
		wantErr bool
		substr  string
	}{
		{
			name: "valid rollback reference",
			ast: &rbast.RunbookAST{
				Metadata:  rbast.Metadata{Name: "Test"},
				Steps:     []rbast.StepNode{{Name: "deploy", Command: "echo", Rollback: "undo-deploy", Line: 10}},
				Rollbacks: []rbast.RollbackNode{{Name: "undo-deploy", Command: "echo", Line: 15}},
			},
			wantErr: false,
		},
		{
			name:    "no rollback attribute",
			ast:     validAST(),
			wantErr: false,
		},
		{
			name: "missing rollback reference",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps:    []rbast.StepNode{{Name: "deploy", Command: "echo", Rollback: "undo-deploy", Line: 10}},
			},
			wantErr: true,
			substr:  "non-existent rollback \"undo-deploy\"",
		},
		{
			name: "typo with suggestion",
			ast: &rbast.RunbookAST{
				Metadata:  rbast.Metadata{Name: "Test"},
				Steps:     []rbast.StepNode{{Name: "deploy", Command: "echo", Rollback: "rollback-deplpy", Line: 10}},
				Rollbacks: []rbast.RollbackNode{{Name: "rollback-deploy", Command: "echo", Line: 15}},
			},
			wantErr: true,
			substr:  "did you mean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := v2RollbackRefs(tt.ast)
			if tt.wantErr {
				if len(errs) == 0 {
					t.Fatal("expected error, got none")
				}
				if !containsMessage(errs, tt.substr) {
					t.Errorf("expected error containing %q, got %v", tt.substr, errs)
				}
			} else if len(errs) > 0 {
				t.Errorf("expected no errors, got %v", errs)
			}
		})
	}
}

// --- V3: Unused rollbacks ---

func TestV3_UnusedRollbacks(t *testing.T) {
	tests := []struct {
		name     string
		ast      *rbast.RunbookAST
		wantWarn bool
		substr   string
	}{
		{
			name: "all rollbacks referenced",
			ast: &rbast.RunbookAST{
				Metadata:  rbast.Metadata{Name: "Test"},
				Steps:     []rbast.StepNode{{Name: "deploy", Command: "echo", Rollback: "undo", Line: 10}},
				Rollbacks: []rbast.RollbackNode{{Name: "undo", Command: "echo", Line: 15}},
			},
			wantWarn: false,
		},
		{
			name: "unreferenced rollback",
			ast: &rbast.RunbookAST{
				Metadata:  rbast.Metadata{Name: "Test"},
				Steps:     []rbast.StepNode{{Name: "deploy", Command: "echo", Line: 10}},
				Rollbacks: []rbast.RollbackNode{{Name: "orphan", Command: "echo", Line: 15}},
			},
			wantWarn: true,
			substr:   "rollback \"orphan\" is never referenced",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := v3UnusedRollbacks(tt.ast)
			warnings := errorsWithSeverity(errs, Warning)
			if tt.wantWarn {
				if len(warnings) == 0 {
					t.Fatal("expected warning, got none")
				}
				if !containsMessage(warnings, tt.substr) {
					t.Errorf("expected warning containing %q, got %v", tt.substr, warnings)
				}
			} else if len(warnings) > 0 {
				t.Errorf("expected no warnings, got %v", warnings)
			}
		})
	}
}

// --- V4: Frontmatter name ---

func TestV4_FrontmatterName(t *testing.T) {
	tests := []struct {
		name    string
		ast     *rbast.RunbookAST
		wantErr bool
	}{
		{
			name:    "name present",
			ast:     &rbast.RunbookAST{Metadata: rbast.Metadata{Name: "Deploy"}},
			wantErr: false,
		},
		{
			name:    "name empty",
			ast:     &rbast.RunbookAST{Metadata: rbast.Metadata{Name: ""}},
			wantErr: true,
		},
		{
			name:    "name whitespace only",
			ast:     &rbast.RunbookAST{Metadata: rbast.Metadata{Name: "   "}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := v4FrontmatterName(tt.ast)
			if tt.wantErr && len(errs) == 0 {
				t.Fatal("expected error, got none")
			}
			if !tt.wantErr && len(errs) > 0 {
				t.Errorf("expected no errors, got %v", errs)
			}
		})
	}
}

// --- V5: Environment filters ---

func TestV5_EnvFilters(t *testing.T) {
	tests := []struct {
		name     string
		ast      *rbast.RunbookAST
		wantWarn bool
		substr   string
	}{
		{
			name: "valid env reference",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test", Environments: []string{"staging", "production"}},
				Steps:    []rbast.StepNode{{Name: "deploy", Command: "echo", Env: []string{"staging"}, Line: 10}},
			},
			wantWarn: false,
		},
		{
			name: "undeclared env reference",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test", Environments: []string{"staging"}},
				Steps:    []rbast.StepNode{{Name: "deploy", Command: "echo", Env: []string{"production"}, Line: 10}},
			},
			wantWarn: true,
			substr:   "environment \"production\" not declared",
		},
		{
			name: "no environments declared skips check",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps:    []rbast.StepNode{{Name: "deploy", Command: "echo", Env: []string{"production"}, Line: 10}},
			},
			wantWarn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := v5EnvFilters(tt.ast)
			warnings := errorsWithSeverity(errs, Warning)
			if tt.wantWarn {
				if len(warnings) == 0 {
					t.Fatal("expected warning, got none")
				}
				if !containsMessage(warnings, tt.substr) {
					t.Errorf("expected warning containing %q, got %v", tt.substr, warnings)
				}
			} else if len(warnings) > 0 {
				t.Errorf("expected no warnings, got %v", warnings)
			}
		})
	}
}

// --- V6: Step timeouts ---

func TestV6_StepTimeouts(t *testing.T) {
	tests := []struct {
		name     string
		timeout  string
		wantWarn bool
		wantErr  bool
	}{
		{name: "valid 30s", timeout: "30s", wantWarn: false},
		{name: "valid 1h", timeout: "1h", wantWarn: false},
		{name: "valid 1s boundary", timeout: "1s", wantWarn: false},
		{name: "valid 24h boundary", timeout: "24h0s", wantWarn: false},
		{name: "too short 500ms", timeout: "500ms", wantWarn: true},
		{name: "too long 25h", timeout: "25h", wantWarn: true},
		{name: "no timeout", timeout: "", wantWarn: false},
		{name: "invalid format", timeout: "notaduration", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast := &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps:    []rbast.StepNode{{Name: "s1", Command: "echo", Timeout: tt.timeout, Line: 10}},
			}
			errs := v6StepTimeouts(ast)
			errors := errorsWithSeverity(errs, Error)
			warnings := errorsWithSeverity(errs, Warning)

			if tt.wantErr && len(errors) == 0 {
				t.Fatal("expected error, got none")
			}
			if tt.wantWarn && len(warnings) == 0 {
				t.Fatal("expected warning, got none")
			}
			if !tt.wantErr && !tt.wantWarn && len(errs) > 0 {
				t.Errorf("expected no issues, got %v", errs)
			}
		})
	}
}

// --- V7: Template variables ---

func TestV7_TemplateVars(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantErr bool
		substr  string
	}{
		{name: "valid variable", command: "echo {{env}}", wantErr: false},
		{name: "valid underscore", command: "echo {{my_var}}", wantErr: false},
		{name: "valid multiple", command: "echo {{env}} {{version}}", wantErr: false},
		{name: "no variables", command: "echo hello", wantErr: false},
		{name: "empty variable", command: "echo {{}}", wantErr: true, substr: "empty template variable"},
		{name: "invalid chars", command: "echo {{a-b}}", wantErr: true, substr: "invalid template variable"},
		{name: "starts with digit", command: "echo {{1var}}", wantErr: true, substr: "invalid template variable"},
		{name: "spaces in name", command: "echo {{my var}}", wantErr: true, substr: "invalid template variable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast := &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps:    []rbast.StepNode{{Name: "s1", Command: tt.command, Line: 10}},
			}
			errs := v7TemplateVars(ast)
			if tt.wantErr {
				if len(errs) == 0 {
					t.Fatal("expected error, got none")
				}
				if !containsMessage(errs, tt.substr) {
					t.Errorf("expected error containing %q, got %v", tt.substr, errs)
				}
			} else if len(errs) > 0 {
				t.Errorf("expected no errors, got %v", errs)
			}
		})
	}
}

// --- V8: Rollback cycles ---

func TestV8_RollbackCycles(t *testing.T) {
	tests := []struct {
		name    string
		ast     *rbast.RunbookAST
		wantErr bool
		substr  string
	}{
		// ── Clean cases ────────────────────────────────────────────────────────
		{
			name: "no cycle — each step has its own rollback",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps: []rbast.StepNode{
					{Name: "migrate", Command: "echo migrate", Rollback: "undo-migrate", Line: 10},
					{Name: "deploy", Command: "echo deploy", Rollback: "undo-deploy", Line: 11},
				},
				Rollbacks: []rbast.RollbackNode{
					{Name: "undo-migrate", Command: "echo rolling back migrate", Line: 20},
					{Name: "undo-deploy", Command: "echo rolling back deploy", Line: 21},
				},
			},
			wantErr: false,
		},
		{
			name:    "no rollback refs at all",
			ast:     validAST(),
			wantErr: false,
		},
		{
			// Command mentions an unrelated word that happens to share a prefix.
			name: "command contains name as substring — no false positive",
			ast: &rbast.RunbookAST{
				Metadata:  rbast.Metadata{Name: "Test"},
				Steps:     []rbast.StepNode{{Name: "deploy", Command: "echo deploy", Rollback: "undo", Line: 10}},
				Rollbacks: []rbast.RollbackNode{{Name: "undo", Command: "echo undo-extra-suffix", Line: 20}},
			},
			wantErr: false,
		},

		// ── Case 1: self-reference in command body ──────────────────────────
		{
			name: "rollback block references its own name",
			ast: &rbast.RunbookAST{
				Metadata:  rbast.Metadata{Name: "Test"},
				Steps:     []rbast.StepNode{{Name: "deploy", Command: "echo deploy", Rollback: "undo-deploy", Line: 10}},
				Rollbacks: []rbast.RollbackNode{{Name: "undo-deploy", Command: "runbook run undo-deploy", Line: 20}},
			},
			wantErr: true,
			substr:  "references its own name",
		},

		// ── Case 2: two rollback blocks referencing each other ──────────────
		{
			name: "two rollback blocks reference each other",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps: []rbast.StepNode{
					{Name: "step-a", Command: "echo a", Rollback: "rb-a", Line: 10},
					{Name: "step-b", Command: "echo b", Rollback: "rb-b", Line: 11},
				},
				Rollbacks: []rbast.RollbackNode{
					{Name: "rb-a", Command: "echo rolling back; rb-b", Line: 20},
					{Name: "rb-b", Command: "echo rolling back; rb-a", Line: 21},
				},
			},
			wantErr: true,
			substr:  "circular reference chain",
		},

		// ── Case 3: two steps share the same rollback block ─────────────────
		{
			name: "step whose rollback is shared with another step",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps: []rbast.StepNode{
					{Name: "step-a", Command: "echo a", Rollback: "shared-rollback", Line: 10},
					{Name: "step-b", Command: "echo b", Rollback: "shared-rollback", Line: 11},
				},
				Rollbacks: []rbast.RollbackNode{
					{Name: "shared-rollback", Command: "echo shared", Line: 20},
				},
			},
			wantErr: true,
			substr:  "shared by multiple steps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := v8RollbackCycles(tt.ast)
			errors := errorsWithSeverity(errs, Error)
			if tt.wantErr {
				if len(errors) == 0 {
					t.Fatal("expected error, got none")
				}
				if tt.substr != "" && !containsMessage(errors, tt.substr) {
					t.Errorf("expected error containing %q, got: %v", tt.substr, errors)
				}
			} else if len(errors) > 0 {
				t.Errorf("expected no errors, got %v", errors)
			}
		})
	}
}

// --- V9: DependsOn references ---

func TestV9_DependsOnRefs(t *testing.T) {
	tests := []struct {
		name     string
		ast      *rbast.RunbookAST
		wantWarn bool
		substr   string
	}{
		{
			name: "valid depends_on",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps: []rbast.StepNode{
					{Name: "setup", Command: "echo", Line: 10},
					{Name: "deploy", Command: "echo", DependsOn: "setup", Line: 15},
				},
			},
			wantWarn: false,
		},
		{
			name:     "no depends_on",
			ast:      validAST(),
			wantWarn: false,
		},
		{
			name: "missing depends_on target",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps:    []rbast.StepNode{{Name: "deploy", Command: "echo", DependsOn: "nonexistent", Line: 10}},
			},
			wantWarn: true,
			substr:   "non-existent step \"nonexistent\"",
		},
		{
			name: "typo with suggestion",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps: []rbast.StepNode{
					{Name: "migrate-db", Command: "echo", Line: 10},
					{Name: "deploy", Command: "echo", DependsOn: "migrat-db", Line: 15},
				},
			},
			wantWarn: true,
			substr:   "did you mean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := v9DependsOnRefs(tt.ast)
			warnings := errorsWithSeverity(errs, Warning)
			if tt.wantWarn {
				if len(warnings) == 0 {
					t.Fatal("expected warning, got none")
				}
				if !containsMessage(warnings, tt.substr) {
					t.Errorf("expected warning containing %q, got %v", tt.substr, warnings)
				}
			} else if len(warnings) > 0 {
				t.Errorf("expected no warnings, got %v", warnings)
			}
		})
	}
}

// --- V10: Required tools ---

func TestV10_RequiredTools(t *testing.T) {
	// Override lookPathFunc for testing.
	origLookPath := lookPathFunc
	t.Cleanup(func() { lookPathFunc = origLookPath })

	available := map[string]bool{
		"kubectl": true,
		"jq":      true,
	}
	lookPathFunc = func(name string) (string, error) {
		if available[name] {
			return "/usr/bin/" + name, nil
		}
		return "", fmt.Errorf("not found: %s", name)
	}

	tests := []struct {
		name     string
		tools    []string
		wantWarn bool
		substr   string
	}{
		{
			name:     "all tools available",
			tools:    []string{"kubectl", "jq"},
			wantWarn: false,
		},
		{
			name:     "missing tool",
			tools:    []string{"kubectl", "nonexistent-tool"},
			wantWarn: true,
			substr:   "\"nonexistent-tool\" not found",
		},
		{
			name:     "no tools required",
			tools:    nil,
			wantWarn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast := &rbast.RunbookAST{
				Metadata: rbast.Metadata{
					Name:     "Test",
					Requires: rbast.Requirements{Tools: tt.tools},
				},
			}
			errs := v10RequiredTools(ast)
			warnings := errorsWithSeverity(errs, Warning)
			if tt.wantWarn {
				if len(warnings) == 0 {
					t.Fatal("expected warning, got none")
				}
				if !containsMessage(warnings, tt.substr) {
					t.Errorf("expected warning containing %q, got %v", tt.substr, warnings)
				}
			} else if len(warnings) > 0 {
				t.Errorf("expected no warnings, got %v", warnings)
			}
		})
	}
}

// --- V11: Non-empty commands ---

func TestV11_NonEmptyCommands(t *testing.T) {
	tests := []struct {
		name    string
		ast     *rbast.RunbookAST
		wantErr bool
		substr  string
	}{
		{
			name:    "all commands non-empty",
			ast:     validAST(),
			wantErr: false,
		},
		{
			name: "empty step command",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Steps:    []rbast.StepNode{{Name: "empty", Command: "", Line: 10}},
			},
			wantErr: true,
			substr:  "step \"empty\" has an empty command body",
		},
		{
			name: "whitespace-only check command",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Checks:   []rbast.CheckNode{{Name: "blank", Command: "   \n  ", Line: 5}},
			},
			wantErr: true,
			substr:  "check \"blank\" has an empty command body",
		},
		{
			name: "empty rollback command",
			ast: &rbast.RunbookAST{
				Metadata:  rbast.Metadata{Name: "Test"},
				Rollbacks: []rbast.RollbackNode{{Name: "undo", Command: "", Line: 10}},
			},
			wantErr: true,
			substr:  "rollback \"undo\" has an empty command body",
		},
		{
			name: "empty wait command",
			ast: &rbast.RunbookAST{
				Metadata: rbast.Metadata{Name: "Test"},
				Waits:    []rbast.WaitNode{{Name: "pause", Duration: "30s", Command: "", Line: 10}},
			},
			wantErr: true,
			substr:  "wait \"pause\" has an empty command body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := v11NonEmptyCommands(tt.ast)
			if tt.wantErr {
				if len(errs) == 0 {
					t.Fatal("expected error, got none")
				}
				if !containsMessage(errs, tt.substr) {
					t.Errorf("expected error containing %q, got %v", tt.substr, errs)
				}
			} else if len(errs) > 0 {
				t.Errorf("expected no errors, got %v", errs)
			}
		})
	}
}

// --- V12: Duplicate YAML keys ---

func TestV12_DuplicateYAMLKeys(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		substr  string
	}{
		{
			name:    "no duplicates",
			yaml:    "name: Test\nversion: 1.0.0\n",
			wantErr: false,
		},
		{
			name:    "duplicate top-level key",
			yaml:    "name: First\nversion: 1.0.0\nname: Second\n",
			wantErr: true,
			substr:  "duplicate YAML key \"name\"",
		},
		{
			name:    "empty frontmatter",
			yaml:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast := &rbast.RunbookAST{
				Metadata:       rbast.Metadata{Name: "Test"},
				RawFrontmatter: tt.yaml,
			}
			errs := v12DuplicateYAMLKeys(ast)
			if tt.wantErr {
				if len(errs) == 0 {
					t.Fatal("expected error, got none")
				}
				if !containsMessage(errs, tt.substr) {
					t.Errorf("expected error containing %q, got %v", tt.substr, errs)
				}
			} else if len(errs) > 0 {
				t.Errorf("expected no errors, got %v", errs)
			}
		})
	}
}

// --- Integration: Validate function ---

func TestValidate_ValidAST(t *testing.T) {
	ast := &rbast.RunbookAST{
		Metadata: rbast.Metadata{
			Name:         "Deploy Service",
			Version:      "1.0.0",
			Environments: []string{"staging", "production"},
		},
		Checks: []rbast.CheckNode{
			{Name: "pre-check", Command: "echo ok", Line: 5},
		},
		Steps: []rbast.StepNode{
			{Name: "deploy", Command: "echo deploy", Rollback: "undo-deploy", Timeout: "120s", Env: []string{"staging", "production"}, Line: 10},
			{Name: "verify", Command: "curl http://localhost/health", DependsOn: "deploy", Line: 20},
		},
		Rollbacks: []rbast.RollbackNode{
			{Name: "undo-deploy", Command: "echo rollback", Line: 15},
		},
		FilePath:       "deploy.runbook",
		RawFrontmatter: "name: Deploy Service\nversion: 1.0.0\nenvironments: [staging, production]\n",
	}

	errs := Validate(ast, Options{})
	errors := errorsWithSeverity(errs, Error)
	if len(errors) > 0 {
		t.Errorf("expected no errors on valid AST, got %v", errors)
	}
}

func TestValidate_MultipleIssues(t *testing.T) {
	ast := &rbast.RunbookAST{
		Metadata: rbast.Metadata{
			Name:         "",
			Environments: []string{"staging"},
		},
		Steps: []rbast.StepNode{
			{Name: "deploy", Command: "echo {{1bad}}", Rollback: "nonexistent", Env: []string{"production"}, Line: 10},
		},
		FilePath:       "bad.runbook",
		RawFrontmatter: "environments: [staging]\n",
	}

	errs := Validate(ast, Options{})
	errors := errorsWithSeverity(errs, Error)
	warnings := errorsWithSeverity(errs, Warning)

	if len(errors) < 2 {
		t.Errorf("expected at least 2 errors (name + rollback ref + var), got %d: %v", len(errors), errors)
	}
	if len(warnings) < 1 {
		t.Errorf("expected at least 1 warning (env filter), got %d: %v", len(warnings), warnings)
	}
}

func TestHasErrors(t *testing.T) {
	tests := []struct {
		name string
		errs []ValidationError
		want bool
	}{
		{"no errors", nil, false},
		{"warnings only", []ValidationError{{Severity: Warning}}, false},
		{"has error", []ValidationError{{Severity: Error}}, true},
		{"mixed", []ValidationError{{Severity: Warning}, {Severity: Error}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasErrors(tt.errs); got != tt.want {
				t.Errorf("HasErrors() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSuggestName(t *testing.T) {
	candidates := map[string]bool{
		"rollback-deploy":  true,
		"rollback-migrate": true,
		"undo-cleanup":     true,
	}

	tests := []struct {
		target string
		want   string
	}{
		{"rollback-deplpy", "rollback-deploy"},
		{"rollback-migrat", "rollback-migrate"},
		{"completely-different-name-that-matches-nothing-at-all", ""},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			got := suggestName(tt.target, candidates)
			if got != tt.want {
				t.Errorf("suggestName(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}

func TestValidationError_String(t *testing.T) {
	tests := []struct {
		err  ValidationError
		want string
	}{
		{
			ValidationError{Severity: Error, Message: "bad thing", Line: 42},
			"error: line 42: bad thing",
		},
		{
			ValidationError{Severity: Warning, Message: "heads up", Line: 0},
			"warning: heads up",
		},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- V13: .env in .gitignore ---

func TestV13_DotEnvMissingFromGitignore(t *testing.T) {
	dir := t.TempDir()

	// Write a .env file next to the runbook.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No .gitignore exists anywhere up the temp tree.
	rbPath := filepath.Join(dir, "deploy.runbook")
	ast := validAST()
	ast.FilePath = rbPath

	errs := v13DotEnvInGitignore(ast)
	if !containsMessage(errs, ".gitignore") {
		t.Errorf("expected .gitignore warning, got %v", errs)
	}
	if len(errorsWithSeverity(errs, Warning)) == 0 {
		t.Error("expected Warning severity")
	}
}

func TestV13_DotEnvListedInGitignore(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ast := validAST()
	ast.FilePath = filepath.Join(dir, "deploy.runbook")

	errs := v13DotEnvInGitignore(ast)
	if len(errs) != 0 {
		t.Errorf("expected no warning when .env is in .gitignore, got %v", errs)
	}
}

func TestV13_DotEnvListedInParentGitignore(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "ops")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	// .gitignore lives in parent, .env and runbook live in child.
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte(".env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, ".env"), []byte("SECRET=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ast := validAST()
	ast.FilePath = filepath.Join(child, "deploy.runbook")

	errs := v13DotEnvInGitignore(ast)
	if len(errs) != 0 {
		t.Errorf("expected no warning when .env is in a parent .gitignore, got %v", errs)
	}
}

func TestV13_NoDotEnvFileNoWarning(t *testing.T) {
	dir := t.TempDir()
	// No .env file — rule must stay silent regardless of .gitignore state.
	ast := validAST()
	ast.FilePath = filepath.Join(dir, "deploy.runbook")

	errs := v13DotEnvInGitignore(ast)
	if len(errs) != 0 {
		t.Errorf("expected no warning when .env absent, got %v", errs)
	}
}

func TestV13_GitignoreWithSlashPrefixPattern(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Gitignore uses "/.env" (root-anchored) — should still be recognized.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("/.env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ast := validAST()
	ast.FilePath = filepath.Join(dir, "deploy.runbook")

	errs := v13DotEnvInGitignore(ast)
	if len(errs) != 0 {
		t.Errorf("expected no warning for '/.env' pattern, got %v", errs)
	}
}

// --- V14: runbook writable by others ---

func TestV14_RunbookWritableByGroupWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	dir := t.TempDir()
	rbPath := filepath.Join(dir, "deploy.runbook")
	if err := os.WriteFile(rbPath, []byte("---\nname: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Use Chmod to bypass umask and set the exact permission bits.
	if err := os.Chmod(rbPath, 0o664); err != nil {
		t.Fatal(err)
	}
	ast := validAST()
	ast.FilePath = rbPath

	errs := v14RunbookWritableByOthers(ast)
	if !containsMessage(errs, "writable by other users") {
		t.Errorf("expected writable-by-others warning, got %v", errs)
	}
	if !containsMessage(errs, "chmod 644") {
		t.Errorf("expected chmod 644 suggestion, got %v", errs)
	}
	if len(errorsWithSeverity(errs, Warning)) == 0 {
		t.Error("expected Warning severity")
	}
}

func TestV14_RunbookWritableByOthersWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	dir := t.TempDir()
	rbPath := filepath.Join(dir, "deploy.runbook")
	if err := os.WriteFile(rbPath, []byte("---\nname: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Use Chmod to bypass umask and set the exact permission bits.
	if err := os.Chmod(rbPath, 0o666); err != nil {
		t.Fatal(err)
	}
	ast := validAST()
	ast.FilePath = rbPath

	errs := v14RunbookWritableByOthers(ast)
	if !containsMessage(errs, "writable by other users") {
		t.Errorf("expected writable-by-others warning, got %v", errs)
	}
}

func TestV14_RunbookOwnerOnlyNoWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits not applicable on Windows")
	}
	dir := t.TempDir()
	rbPath := filepath.Join(dir, "deploy.runbook")
	if err := os.WriteFile(rbPath, []byte("---\nname: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ast := validAST()
	ast.FilePath = rbPath

	errs := v14RunbookWritableByOthers(ast)
	if len(errs) != 0 {
		t.Errorf("expected no warning for 0644 runbook, got %v", errs)
	}
}

func TestV14_MissingFileNoWarning(t *testing.T) {
	// FilePath points to a non-existent file — rule must not error.
	ast := validAST()
	ast.FilePath = "/nonexistent/path/deploy.runbook"

	errs := v14RunbookWritableByOthers(ast)
	if len(errs) != 0 {
		t.Errorf("expected no warning for non-existent file, got %v", errs)
	}
}

func TestV14_EmptyFilePathNoWarning(t *testing.T) {
	ast := validAST()
	ast.FilePath = ""

	errs := v14RunbookWritableByOthers(ast)
	if len(errs) != 0 {
		t.Errorf("expected no warning when FilePath empty, got %v", errs)
	}
}
