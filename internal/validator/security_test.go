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
	"strings"
	"testing"

	rbast "github.com/runbookdev/runbook/internal/ast"
)

// stepAST builds a minimal AST containing a single step for rule-level tests.
func stepAST(name, command, rollback, confirm string, env []string) *rbast.RunbookAST {
	return &rbast.RunbookAST{
		Metadata: rbast.Metadata{Name: "test"},
		Steps: []rbast.StepNode{
			{
				Name:     name,
				Command:  command,
				Rollback: rollback,
				Confirm:  confirm,
				Env:      env,
				Line:     10,
			},
		},
		FilePath: "test.runbook",
	}
}

// hasWarning returns true if any ValidationError contains substr in its message.
func hasWarning(errs []ValidationError, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

// severity returns the Severity of the first ValidationError whose message
// contains substr, or -1 if none found.
func firstSeverity(errs []ValidationError, substr string) Severity {
	for _, e := range errs {
		if strings.Contains(e.Message, substr) {
			return e.Severity
		}
	}
	return -1
}

// ── V15: production without confirmation gate ─────────────────────────────

func TestV15_ProductionWithoutConfirm(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		confirm string
		wantHit bool
	}{
		{
			name:    "production env no confirm — warn",
			env:     []string{"production"},
			confirm: "",
			wantHit: true,
		},
		{
			name:    "Production (capital) env no confirm — warn",
			env:     []string{"Production"},
			confirm: "",
			wantHit: true,
		},
		{
			name:    "PRODUCTION env no confirm — warn",
			env:     []string{"PRODUCTION"},
			confirm: "",
			wantHit: true,
		},
		{
			name:    "production env with confirm — no warn",
			env:     []string{"production"},
			confirm: "production",
			wantHit: false,
		},
		{
			name:    "staging env no confirm — no warn",
			env:     []string{"staging"},
			confirm: "",
			wantHit: false,
		},
		{
			name:    "no env filter no confirm — no warn",
			env:     nil,
			confirm: "",
			wantHit: false,
		},
		{
			name:    "multiple envs including production no confirm — warn",
			env:     []string{"staging", "production"},
			confirm: "",
			wantHit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast := stepAST("deploy", "echo deploy", "", tt.confirm, tt.env)
			errs := v15ProductionWithoutConfirm(ast, Options{})
			got := hasWarning(errs, "confirmation gate")
			if got != tt.wantHit {
				t.Errorf("wantHit=%v, got %v; errs=%v", tt.wantHit, got, errs)
			}
		})
	}
}

func TestV15_SecurityStrictPromotesToError(t *testing.T) {
	ast := stepAST("deploy", "echo deploy", "", "", []string{"production"})

	// Default: warning
	errs := v15ProductionWithoutConfirm(ast, Options{})
	if sev := firstSeverity(errs, "confirmation gate"); sev != Warning {
		t.Errorf("expected Warning by default, got %v", sev)
	}

	// SecurityStrict: error
	errs = v15ProductionWithoutConfirm(ast, Options{SecurityStrict: true})
	if sev := firstSeverity(errs, "confirmation gate"); sev != Error {
		t.Errorf("expected Error with SecurityStrict, got %v", sev)
	}
}

func TestV15_MessageContainsStepName(t *testing.T) {
	ast := stepAST("blue-green-deploy", "echo deploy", "", "", []string{"production"})
	errs := v15ProductionWithoutConfirm(ast, Options{})
	if !hasWarning(errs, "blue-green-deploy") {
		t.Errorf("expected step name in warning message, got %v", errs)
	}
}

// ── V16: destructive command without rollback ──────────────────────────────

func TestV16_DestructiveWithoutRollback(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		rollback string
		wantHit  bool
		wantPat  string // substring expected in message
	}{
		{
			name:     "rm -rf no rollback — warn",
			command:  "rm -rf /tmp/build",
			rollback: "",
			wantHit:  true,
			wantPat:  "rm -rf",
		},
		{
			name:     "rm -rf with rollback — no warn",
			command:  "rm -rf /tmp/build",
			rollback: "restore-build",
			wantHit:  false,
		},
		{
			name:     "DROP TABLE no rollback — warn",
			command:  "psql -c 'DROP TABLE users'",
			rollback: "",
			wantHit:  true,
			wantPat:  "DROP TABLE",
		},
		{
			name:     "drop table (lowercase) no rollback — warn",
			command:  "psql -c 'drop table users'",
			rollback: "",
			wantHit:  true,
			wantPat:  "DROP TABLE",
		},
		{
			name:     "DELETE FROM no rollback — warn",
			command:  "psql -c \"DELETE FROM sessions WHERE expired = true\"",
			rollback: "",
			wantHit:  true,
			wantPat:  "DELETE FROM",
		},
		{
			name:     "kubectl delete no rollback — warn",
			command:  "kubectl delete pod my-pod",
			rollback: "",
			wantHit:  true,
			wantPat:  "kubectl delete",
		},
		{
			name:     "docker rm no rollback — warn",
			command:  "docker rm -f old-container",
			rollback: "",
			wantHit:  true,
			wantPat:  "docker rm",
		},
		{
			name:     "terraform destroy no rollback — warn",
			command:  "terraform destroy -auto-approve",
			rollback: "",
			wantHit:  true,
			wantPat:  "terraform destroy",
		},
		{
			name:     "safe echo command — no warn",
			command:  "echo hello",
			rollback: "",
			wantHit:  false,
		},
		{
			name:     "terraform plan (not destroy) — no warn",
			command:  "terraform plan",
			rollback: "",
			wantHit:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast := stepAST("cleanup", tt.command, tt.rollback, "", nil)
			errs := v16DestructiveWithoutRollback(ast, Options{})
			got := hasWarning(errs, "rollback handler")
			if got != tt.wantHit {
				t.Errorf("wantHit=%v got=%v; errs=%v", tt.wantHit, got, errs)
			}
			if tt.wantHit && tt.wantPat != "" && !hasWarning(errs, tt.wantPat) {
				t.Errorf("expected pattern %q in message, got %v", tt.wantPat, errs)
			}
		})
	}
}

func TestV16_SecurityStrictPromotesToError(t *testing.T) {
	ast := stepAST("cleanup", "rm -rf /tmp/old", "", "", nil)

	if sev := firstSeverity(v16DestructiveWithoutRollback(ast, Options{}), "rollback handler"); sev != Warning {
		t.Errorf("expected Warning by default, got %v", sev)
	}
	if sev := firstSeverity(v16DestructiveWithoutRollback(ast, Options{SecurityStrict: true}), "rollback handler"); sev != Error {
		t.Errorf("expected Error with SecurityStrict, got %v", sev)
	}
}

// ── V17: hardcoded secrets ────────────────────────────────────────────────

func TestV17_HardcodedSecrets(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantHit bool
		wantPat string // substring in message
	}{
		// keyword=literal cases
		{
			name:    "password literal — warn",
			command: "mysql -u root --password=hunter2",
			wantHit: true,
			wantPat: "hardcoded",
		},
		{
			name:    "secret literal — warn",
			command: "curl -H 'secret=abc123def456'",
			wantHit: true,
			wantPat: "hardcoded",
		},
		{
			name:    "token literal — warn",
			command: "deploy --token=xyzABCDEF1234",
			wantHit: true,
			wantPat: "hardcoded",
		},
		{
			name:    "api_key literal — warn",
			command: "app start --api_key=realkey99",
			wantHit: true,
			wantPat: "hardcoded",
		},
		{
			name:    "password template variable — no warn",
			command: "mysql -ppassword={{db_password}}",
			wantHit: false,
		},
		{
			name:    "password shell variable — no warn",
			command: "mysql -ppassword=$DB_PASSWORD",
			wantHit: false,
		},
		{
			name:    "password shell variable braces — no warn",
			command: "mysql -ppassword=${DB_PASSWORD}",
			wantHit: false,
		},
		// AWS access key
		{
			name:    "AWS access key — warn",
			command: "aws configure set aws_access_key_id AKIAIOSFODNN7EXAMPLE",
			wantHit: true,
			wantPat: "AWS access key",
		},
		{
			name:    "partial AKIA prefix (too short) — no warn",
			command: "echo AKIA123",
			wantHit: false,
		},
		// Long hex string
		{
			name:    "long hex token — warn",
			command: "curl -H 'X-Token: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5'",
			wantHit: true,
			wantPat: "hex string",
		},
		{
			name:    "short hex string (32 chars, not >32) — no warn",
			command: "echo a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
			wantHit: false,
		},
		{
			name:    "safe echo — no warn",
			command: "echo hello world",
			wantHit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast := stepAST("connect", tt.command, "", "", nil)
			errs := v17HardcodedSecrets(ast, Options{})
			got := hasWarning(errs, "hardcoded secret")
			if got != tt.wantHit {
				t.Errorf("wantHit=%v got=%v; errs=%v", tt.wantHit, got, errs)
			}
			if tt.wantHit && tt.wantPat != "" && !hasWarning(errs, tt.wantPat) {
				t.Errorf("expected %q in message, got %v", tt.wantPat, errs)
			}
		})
	}
}

func TestV17_SecurityStrictPromotesToError(t *testing.T) {
	ast := stepAST("connect", "aws configure set aws_access_key_id AKIAIOSFODNN7EXAMPLE", "", "", nil)

	if sev := firstSeverity(v17HardcodedSecrets(ast, Options{}), "hardcoded secret"); sev != Warning {
		t.Errorf("expected Warning by default, got %v", sev)
	}
	if sev := firstSeverity(v17HardcodedSecrets(ast, Options{SecurityStrict: true}), "hardcoded secret"); sev != Error {
		t.Errorf("expected Error with SecurityStrict, got %v", sev)
	}
}

// ── V18: curl with TLS verification disabled ──────────────────────────────

func TestV18_CurlInsecure(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantHit bool
	}{
		{
			name:    "curl -k — warn",
			command: "curl -k https://internal.example.com/api",
			wantHit: true,
		},
		{
			name:    "curl --insecure — warn",
			command: "curl --insecure https://internal.example.com/api",
			wantHit: true,
		},
		{
			name:    "curl -k with other flags — warn",
			command: "curl -sSL -k -o output.json https://api.example.com",
			wantHit: true,
		},
		{
			name:    "curl --insecure with output flag — warn",
			command: "curl --insecure -o /tmp/data https://host/data",
			wantHit: true,
		},
		{
			name:    "curl without insecure flags — no warn",
			command: "curl https://api.example.com/data",
			wantHit: false,
		},
		{
			name:    "curl with -K (config file flag, not insecure) — no warn",
			command: "curl -K ~/.curlrc https://api.example.com",
			wantHit: false,
		},
		{
			name:    "wget (not curl) — no warn",
			command: "wget https://example.com/file",
			wantHit: false,
		},
		{
			name:    "safe echo — no warn",
			command: "echo done",
			wantHit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast := stepAST("fetch", tt.command, "", "", nil)
			errs := v18CurlInsecure(ast, Options{})
			got := hasWarning(errs, "TLS verification disabled")
			if got != tt.wantHit {
				t.Errorf("wantHit=%v got=%v; errs=%v", tt.wantHit, got, errs)
			}
		})
	}
}

func TestV18_SecurityStrictPromotesToError(t *testing.T) {
	ast := stepAST("fetch", "curl -k https://host/api", "", "", nil)

	if sev := firstSeverity(v18CurlInsecure(ast, Options{}), "TLS verification"); sev != Warning {
		t.Errorf("expected Warning by default, got %v", sev)
	}
	if sev := firstSeverity(v18CurlInsecure(ast, Options{SecurityStrict: true}), "TLS verification"); sev != Error {
		t.Errorf("expected Error with SecurityStrict, got %v", sev)
	}
}

// ── V19: wget with plain HTTP URL ─────────────────────────────────────────

func TestV19_WgetInsecure(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantHit bool
	}{
		{
			name:    "wget http:// — warn",
			command: "wget http://releases.example.com/app-1.0.tar.gz",
			wantHit: true,
		},
		{
			name:    "wget http:// with output flag — warn",
			command: "wget -O /tmp/app.tar.gz http://releases.example.com/app.tar.gz",
			wantHit: true,
		},
		{
			name:    "wget https:// — no warn",
			command: "wget https://releases.example.com/app-1.0.tar.gz",
			wantHit: false,
		},
		{
			name:    "curl http:// (not wget) — no warn",
			command: "curl http://example.com/file",
			wantHit: false,
		},
		{
			name:    "safe echo — no warn",
			command: "echo done",
			wantHit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast := stepAST("download", tt.command, "", "", nil)
			errs := v19WgetInsecure(ast, Options{})
			got := hasWarning(errs, "unencrypted HTTP URL")
			if got != tt.wantHit {
				t.Errorf("wantHit=%v got=%v; errs=%v", tt.wantHit, got, errs)
			}
		})
	}
}

func TestV19_SecurityStrictPromotesToError(t *testing.T) {
	ast := stepAST("download", "wget http://example.com/file", "", "", nil)

	if sev := firstSeverity(v19WgetInsecure(ast, Options{}), "unencrypted HTTP"); sev != Warning {
		t.Errorf("expected Warning by default, got %v", sev)
	}
	if sev := firstSeverity(v19WgetInsecure(ast, Options{SecurityStrict: true}), "unencrypted HTTP"); sev != Error {
		t.Errorf("expected Error with SecurityStrict, got %v", sev)
	}
}

// ── V20: pipe download directly into shell ────────────────────────────────

func TestV20_PipeToShell(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantHit bool
	}{
		{
			name:    "curl | sh — warn",
			command: "curl https://install.example.com/get.sh | sh",
			wantHit: true,
		},
		{
			name:    "curl | bash — warn",
			command: "curl -sSL https://install.example.com/install.sh | bash",
			wantHit: true,
		},
		{
			name:    "wget | sh — warn",
			command: "wget -qO- https://get.docker.com | sh",
			wantHit: true,
		},
		{
			name:    "wget | bash — warn",
			command: "wget -O - https://get.helm.sh/install.sh | bash",
			wantHit: true,
		},
		{
			name:    "curl piped to tee (not shell) — no warn",
			command: "curl https://example.com/data | tee /tmp/output.json",
			wantHit: false,
		},
		{
			name:    "curl saved to file — no warn",
			command: "curl -o /tmp/install.sh https://install.example.com/get.sh",
			wantHit: false,
		},
		{
			name:    "local script piped to sh — no warn",
			command: "cat install.sh | sh",
			wantHit: false,
		},
		{
			name:    "safe echo — no warn",
			command: "echo done",
			wantHit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast := stepAST("install", tt.command, "", "", nil)
			errs := v20PipeToShell(ast, Options{})
			got := hasWarning(errs, "pipes a download directly to shell")
			if got != tt.wantHit {
				t.Errorf("wantHit=%v got=%v; errs=%v", tt.wantHit, got, errs)
			}
		})
	}
}

func TestV20_SecurityStrictPromotesToError(t *testing.T) {
	ast := stepAST("install", "curl https://get.example.com/install.sh | bash", "", "", nil)

	if sev := firstSeverity(v20PipeToShell(ast, Options{}), "pipes a download"); sev != Warning {
		t.Errorf("expected Warning by default, got %v", sev)
	}
	if sev := firstSeverity(v20PipeToShell(ast, Options{SecurityStrict: true}), "pipes a download"); sev != Error {
		t.Errorf("expected Error with SecurityStrict, got %v", sev)
	}
}

// ── Integration: SecurityStrict via Validate ──────────────────────────────

func TestValidate_SecurityStrictPromotesAllWarningsToErrors(t *testing.T) {
	ast := &rbast.RunbookAST{
		Metadata: rbast.Metadata{Name: "security-test"},
		Steps: []rbast.StepNode{
			{
				Name:    "install",
				Command: "curl https://get.example.com/install.sh | bash",
				Env:     []string{"production"},
				Line:    10,
			},
		},
		FilePath: "test.runbook",
	}

	// Without SecurityStrict: security advisories are warnings, not errors.
	errs := Validate(ast, Options{})
	if HasErrors(errs) {
		// Check that errors come only from non-security rules (there should be none here).
		for _, e := range errs {
			if e.Severity == Error {
				t.Errorf("unexpected error without SecurityStrict: %s", e.Message)
			}
		}
	}

	// With SecurityStrict: the pipe-to-shell advisory becomes an error.
	errs = Validate(ast, Options{SecurityStrict: true})
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "pipes a download") && e.Severity == Error {
			found = true
		}
	}
	if !found {
		t.Error("expected pipe-to-shell advisory to be an Error with SecurityStrict=true")
	}
}

func TestValidate_SecurityStrictDoesNotAffectNonSecurityRules(t *testing.T) {
	// A step with a bad rollback reference is always an Error regardless of SecurityStrict.
	ast := &rbast.RunbookAST{
		Metadata: rbast.Metadata{Name: "test"},
		Steps: []rbast.StepNode{
			{Name: "deploy", Command: "echo deploy", Rollback: "nonexistent", Line: 10},
		},
		FilePath: "test.runbook",
	}

	errsNormal := Validate(ast, Options{})
	errsStrict := Validate(ast, Options{SecurityStrict: true})

	normalErrors := errorsWithSeverity(errsNormal, Error)
	strictErrors := errorsWithSeverity(errsStrict, Error)

	// Both should have at least the rollback-ref error.
	if len(normalErrors) == 0 {
		t.Error("expected rollback-ref error in normal mode")
	}
	// strict mode has all of normal's errors plus promoted security warnings
	if len(strictErrors) < len(normalErrors) {
		t.Errorf("SecurityStrict should not reduce errors: normal=%d strict=%d", len(normalErrors), len(strictErrors))
	}
}
