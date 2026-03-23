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
	"regexp"
	"strings"

	rbast "github.com/runbookdev/runbook/internal/ast"
)

// securitySev returns Warning normally and Error when SecurityStrict is enabled.
// All v15–v20 rules call this helper so that --security-strict promotes every
// advisory to an error in one place.
func securitySev(opts Options) Severity {
	if opts.SecurityStrict {
		return Error
	}
	return Warning
}

// ── compiled patterns ──────────────────────────────────────────────────────

// destructivePatterns lists command substrings (and the canonical name shown in
// the warning) that indicate a potentially irreversible operation.
var destructivePatterns = []struct {
	pattern string // lowercase for case-insensitive matching
	label   string // shown verbatim in the warning message
}{
	{"rm -rf", "rm -rf"},
	{"drop table", "DROP TABLE"},
	{"delete from", "DELETE FROM"},
	{"kubectl delete", "kubectl delete"},
	{"docker rm", "docker rm"},
	{"terraform destroy", "terraform destroy"},
}

// credAssignRe detects password/secret/token/api_key assignment with a literal
// (non-variable) value on the right-hand side.
//
// Capture groups:
//
//	1 – keyword (password, secret, token, api_key)
//	2 – everything after the = up to the first whitespace or end-of-line
var credAssignRe = regexp.MustCompile(
	`(?i)\b(password|secret|token|api_key)\s*=\s*(\S+)`)

// templateVarRe matches a bare {{identifier}} template variable.
var templateVarRe = regexp.MustCompile(`^\{\{[a-zA-Z_][a-zA-Z0-9_]*\}\}$`)

// shellVarRe matches a shell variable reference such as $VAR or ${VAR}.
var shellVarRe = regexp.MustCompile(`^\$\{?[a-zA-Z_]\w*\}?$`)

// awsKeyRe matches an AWS access key ID (AKIA + 16 uppercase alphanumerics).
var awsKeyRe = regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`)

// longHexRe matches a standalone hex string longer than 32 characters.
// Such strings can be raw API tokens, secret keys, or other credentials.
var longHexRe = regexp.MustCompile(`\b[a-fA-F0-9]{33,}\b`)

// curlInsecureRe matches curl invocations with TLS verification disabled.
var curlInsecureRe = regexp.MustCompile(`\bcurl\b[^\n]*(?:\s-k\b|\s--insecure\b)`)

// wgetHTTPRe matches wget fetching a plain http:// URL (not https://).
// Note: "http://" does not appear in "https://", so this is unambiguous.
var wgetHTTPRe = regexp.MustCompile(`\bwget\b[^\n]*\bhttp://`)

// pipeToShellRe matches a curl/wget output being piped directly into a shell.
var pipeToShellRe = regexp.MustCompile(`\b(?:curl|wget)\b[^\n]*\|\s*(?:ba)?sh\b`)

// ── v15: production step without confirmation gate ─────────────────────────

// v15ProductionWithoutConfirm warns when a step targets the "production"
// environment but has no confirm attribute. Without a confirmation gate an
// operator could accidentally execute destructive commands in production.
func v15ProductionWithoutConfirm(ast *rbast.RunbookAST, opts Options) []ValidationError {
	var errs []ValidationError
	sev := securitySev(opts)

	for _, step := range ast.Steps {
		if !targetsProduction(step.Env) {
			continue
		}
		if step.Confirm != "" {
			continue
		}
		errs = append(errs, ValidationError{
			Severity: sev,
			Message: fmt.Sprintf(
				"step %q targets production but has no confirmation gate. Add confirm: production.",
				step.Name,
			),
			Line: step.Line,
		})
	}
	return errs
}

// targetsProduction returns true if the env list contains "production"
// (case-insensitive, so "Production" and "PRODUCTION" also match).
func targetsProduction(envs []string) bool {
	for _, e := range envs {
		if strings.EqualFold(e, "production") {
			return true
		}
	}
	return false
}

// ── v16: destructive command without rollback ──────────────────────────────

// v16DestructiveWithoutRollback warns when a step's command contains a
// destructive operation pattern but the step has no rollback handler.
func v16DestructiveWithoutRollback(ast *rbast.RunbookAST, opts Options) []ValidationError {
	var errs []ValidationError
	sev := securitySev(opts)

	for _, step := range ast.Steps {
		if step.Rollback != "" {
			continue
		}
		label := firstDestructivePattern(step.Command)
		if label == "" {
			continue
		}
		errs = append(errs, ValidationError{
			Severity: sev,
			Message: fmt.Sprintf(
				"step %q contains potentially destructive command %q but has no rollback handler.",
				step.Name, label,
			),
			Line: step.Line,
		})
	}
	return errs
}

// firstDestructivePattern returns the label of the first destructive pattern
// found in cmd, or "" if none match. Matching is case-insensitive.
func firstDestructivePattern(cmd string) string {
	lower := strings.ToLower(cmd)
	for _, dp := range destructivePatterns {
		if strings.Contains(lower, dp.pattern) {
			return dp.label
		}
	}
	return ""
}

// ── v17: hardcoded secrets in commands ────────────────────────────────────

// v17HardcodedSecrets warns (or errors under --security-strict) when a step
// command appears to contain a hardcoded credential value. Three patterns are
// detected:
//
//  1. A keyword assignment such as password=literal (not a template or shell var)
//  2. An AWS access key ID (AKIA…)
//  3. A long (>32 char) hex string that looks like a raw token or secret
func v17HardcodedSecrets(ast *rbast.RunbookAST, opts Options) []ValidationError {
	var errs []ValidationError
	sev := securitySev(opts)

	for _, step := range ast.Steps {
		if reason := hardcodedSecretReason(step.Command); reason != "" {
			errs = append(errs, ValidationError{
				Severity: sev,
				Message: fmt.Sprintf(
					"step %q appears to contain a hardcoded secret (%s). Use a template variable instead.",
					step.Name, reason,
				),
				Line: step.Line,
			})
		}
	}
	return errs
}

// hardcodedSecretReason returns a short description of why the command is
// considered to contain a hardcoded secret, or "" if no pattern matched.
func hardcodedSecretReason(cmd string) string {
	// 1. keyword=literal assignments
	for _, m := range credAssignRe.FindAllStringSubmatch(cmd, -1) {
		val := m[2]
		// Strip surrounding quotes so {{var}} inside quotes is handled.
		val = strings.Trim(val, `'"`)
		if templateVarRe.MatchString(val) || shellVarRe.MatchString(val) {
			continue
		}
		// Ignore very short values like password= (empty or 1-char placeholder).
		if len(val) < 2 {
			continue
		}
		return fmt.Sprintf("possible hardcoded %s", strings.ToLower(m[1]))
	}

	// 2. AWS access key ID
	if awsKeyRe.MatchString(cmd) {
		return "AWS access key ID (AKIA…)"
	}

	// 3. Long hex string (>32 chars) — potential raw token or secret
	if m := longHexRe.FindString(cmd); m != "" {
		return fmt.Sprintf("long hex string (%d chars)", len(m))
	}

	return ""
}

// ── v18: curl with TLS verification disabled ──────────────────────────────

// v18CurlInsecure warns when a step uses curl -k or curl --insecure, which
// disables TLS certificate verification and enables MITM attacks.
func v18CurlInsecure(ast *rbast.RunbookAST, opts Options) []ValidationError {
	var errs []ValidationError
	sev := securitySev(opts)

	for _, step := range ast.Steps {
		if !curlInsecureRe.MatchString(step.Command) {
			continue
		}
		errs = append(errs, ValidationError{
			Severity: sev,
			Message:  fmt.Sprintf("step %q uses curl with TLS verification disabled.", step.Name),
			Line:     step.Line,
		})
	}
	return errs
}

// ── v19: wget fetching an unencrypted HTTP URL ────────────────────────────

// v19WgetInsecure warns when a step uses wget with a plain http:// URL.
// Fetching over unencrypted HTTP exposes downloaded content to tampering.
func v19WgetInsecure(ast *rbast.RunbookAST, opts Options) []ValidationError {
	var errs []ValidationError
	sev := securitySev(opts)

	for _, step := range ast.Steps {
		if !wgetHTTPRe.MatchString(step.Command) {
			continue
		}
		errs = append(errs, ValidationError{
			Severity: sev,
			Message:  fmt.Sprintf("step %q fetches from an unencrypted HTTP URL.", step.Name),
			Line:     step.Line,
		})
	}
	return errs
}

// ── v20: pipe download directly into shell ────────────────────────────────

// v20PipeToShell warns when a step pipes the output of curl or wget directly
// into sh or bash. This pattern executes arbitrary remote code without any
// verification step and is a well-known supply-chain attack vector.
func v20PipeToShell(ast *rbast.RunbookAST, opts Options) []ValidationError {
	var errs []ValidationError
	sev := securitySev(opts)

	for _, step := range ast.Steps {
		if !pipeToShellRe.MatchString(step.Command) {
			continue
		}
		errs = append(errs, ValidationError{
			Severity: sev,
			Message: fmt.Sprintf(
				"step %q pipes a download directly to shell. This is a security risk — download first, verify, then execute.",
				step.Name,
			),
			Line: step.Line,
		})
	}
	return errs
}
