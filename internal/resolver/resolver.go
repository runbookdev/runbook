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
	"bufio"
	"fmt"
	"io"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	rbast "github.com/runbookdev/runbook/internal/ast"
	"github.com/runbookdev/runbook/internal/audit"
)

// EnvVarPrefix is the prefix used to scope runbook-specific environment
// variables, both when the resolver reads them and when the executor injects
// RUNBOOK_* variables into subprocess environments.
const EnvVarPrefix = "RUNBOOK_"

// Built-in variable names automatically populated on every run and available
// as {{name}} substitutions in block bodies.
const (
	// builtinVarEnv is the target environment passed on the CLI.
	builtinVarEnv = "env"
	// builtinVarRunbookName mirrors metadata.name.
	builtinVarRunbookName = "runbook_name"
	// builtinVarRunbookVersion mirrors metadata.version.
	builtinVarRunbookVersion = "runbook_version"
	// builtinVarRunID is a per-execution UUID.
	builtinVarRunID = "run_id"
	// builtinVarTimestamp is the RFC-3339 UTC time of resolution.
	builtinVarTimestamp = "timestamp"
	// builtinVarUser is the OS user running the runbook.
	builtinVarUser = "user"
	// builtinVarHostname is the host the runbook runs on.
	builtinVarHostname = "hostname"
	// builtinVarCWD is the current working directory at run start.
	builtinVarCWD = "cwd"
)

// Secret provider names returned from SecretProvider.Name.
const (
	providerNameEnv    = "env"
	providerNameDotEnv = "dotenv"
)

// varPattern matches {{variable}} references in command bodies.
var varPattern = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)

// Options configures shell metacharacter scanning behaviour during resolution.
type Options struct {
	// NonInteractive skips interactive prompts; metacharacter warnings are
	// printed but execution continues.
	NonInteractive bool
	// DryRun shows metacharacter warnings but does not prompt.
	DryRun bool
	// Strict treats any metacharacter warning as a hard error and returns a
	// *MetacharError without prompting (intended for CI pipelines).
	Strict bool
	// Stderr is the writer for warning output. Defaults to os.Stderr.
	Stderr io.Writer
	// PromptInput is the reader for interactive prompts. Defaults to os.Stdin.
	PromptInput io.Reader
}

// SecretProvider resolves secret values by key.
type SecretProvider interface {
	// Resolve returns the value for the given key.
	Resolve(key string) (string, error)
	// Name returns a human-readable name for this provider.
	Name() string
}

// EnvProvider resolves secrets from environment variables with the EnvVarPrefix.
type EnvProvider struct{}

// DotEnvProvider resolves secrets from a .env file.
type DotEnvProvider struct {
	// vars holds the parsed key/value pairs loaded from the .env file.
	vars map[string]string
}

// varRef tracks where a variable is referenced.
type varRef struct {
	// name is the variable identifier (without the {{ }} delimiters).
	name string
	// blockType is the kind of block the reference appeared in.
	blockType string
	// blockName is the referencing block's name attribute.
	blockName string
	// line is the 1-based source line of the block opening fence.
	line int
}

// varPair holds a single resolved variable substitution.
type varPair struct {
	// name is the variable identifier.
	name string
	// value is the substituted value.
	value string
}

// resolvedUsage records one variable substitution with its location context.
type resolvedUsage struct {
	// varName is the variable identifier that was substituted.
	varName string
	// value is the value that replaced the reference.
	value string
	// blockType is the kind of block the substitution occurred in.
	blockType string
	// blockName is the block's name attribute.
	blockName string
	// line is the 1-based source line of the block opening fence.
	line int
}

// Resolve returns the value of the EnvVarPrefix+<key> environment variable.
func (p *EnvProvider) Resolve(key string) (string, error) {
	full := EnvVarPrefix + strings.ToUpper(key)
	val, ok := os.LookupEnv(full)
	if !ok {
		return "", fmt.Errorf("environment variable %s not set", full)
	}
	return val, nil
}

// Name returns the provider name.
func (p *EnvProvider) Name() string { return providerNameEnv }

// NewDotEnvProvider creates a DotEnvProvider by reading the given .env file.
func NewDotEnvProvider(path string) (*DotEnvProvider, error) {
	vars, err := godotenv.Read(path)
	if err != nil {
		return nil, fmt.Errorf("reading .env file %s: %w", path, err)
	}
	return &DotEnvProvider{vars: vars}, nil
}

// Resolve returns the value for the given key from the .env file.
func (p *DotEnvProvider) Resolve(key string) (string, error) {
	val, ok := p.vars[key]
	if !ok {
		return "", fmt.Errorf("key %s not found in .env file", key)
	}
	return val, nil
}

// Name returns the provider name.
func (p *DotEnvProvider) Name() string { return providerNameDotEnv }

// Resolve resolves all template variables in the AST, filters blocks by target
// environment, stores both original and resolved commands on each node, and
// scans every resolved variable value for dangerous shell metacharacters.
func Resolve(ast *rbast.RunbookAST, targetEnv string, cliVars map[string]string, envFilePath string, opts Options) error { //nolint:gocyclo // resolution pipeline has inherently high branching
	// Build resolution context in priority order (lowest to highest).
	context := make(map[string]string)

	// 1. Built-in variables (lowest priority).
	builtins := buildBuiltins(ast, targetEnv)
	maps.Copy(context, builtins)

	// 2. .env file variables.
	if envFilePath != "" {
		dotenvVars, err := godotenv.Read(envFilePath)
		if err != nil {
			return fmt.Errorf("reading env file %s: %w", envFilePath, err)
		}

		maps.Copy(context, dotenvVars)
		warnDotEnvPermissions(envFilePath, opts.Stderr)
		warnDotEnvPath(envFilePath, filepath.Dir(ast.FilePath), opts.Stderr)
	}

	// 3. Environment variables with the EnvVarPrefix.
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(k, EnvVarPrefix) {
			context[strings.ToLower(strings.TrimPrefix(k, EnvVarPrefix))] = v
		}
	}

	// 4. CLI flags (highest priority).
	maps.Copy(context, cliVars)

	// Record sensitive variable values for downstream redaction.
	secrets := make(map[string]string)
	for k, v := range context {
		if audit.IsSensitive(k) {
			secrets[k] = v
		}
	}
	ast.ResolvedSecrets = secrets

	// Filter blocks by target environment.
	if targetEnv != "" {
		filterByEnv(ast, targetEnv)
	}

	// Collect all variable references and substitute.
	var unresolved []varRef
	var allResolved []resolvedUsage

	// Process checks.
	for i := range ast.Checks {
		node := &ast.Checks[i]
		node.OriginalCommand = node.Command
		resolved, refs, pairs := substituteVars(node.Command, context)
		node.Command = resolved
		for _, r := range refs {
			unresolved = append(unresolved, varRef{
				name:      r,
				blockType: rbast.BlockTypeCheck,
				blockName: node.Name,
				line:      node.Line,
			})
		}
		for _, p := range pairs {
			allResolved = append(allResolved, resolvedUsage{
				varName:   p.name,
				value:     p.value,
				blockType: rbast.BlockTypeCheck,
				blockName: node.Name,
				line:      node.Line,
			})
		}
	}

	// Process steps.
	for i := range ast.Steps {
		node := &ast.Steps[i]
		node.OriginalCommand = node.Command
		resolved, refs, pairs := substituteVars(node.Command, context)
		node.Command = resolved
		for _, r := range refs {
			unresolved = append(unresolved, varRef{
				name:      r,
				blockType: rbast.BlockTypeStep,
				blockName: node.Name,
				line:      node.Line,
			})
		}
		for _, p := range pairs {
			allResolved = append(allResolved, resolvedUsage{
				varName:   p.name,
				value:     p.value,
				blockType: rbast.BlockTypeStep,
				blockName: node.Name,
				line:      node.Line,
			})
		}
	}

	// Process rollbacks.
	for i := range ast.Rollbacks {
		node := &ast.Rollbacks[i]
		node.OriginalCommand = node.Command
		resolved, refs, pairs := substituteVars(node.Command, context)
		node.Command = resolved
		for _, r := range refs {
			unresolved = append(unresolved, varRef{
				name:      r,
				blockType: rbast.BlockTypeRollback,
				blockName: node.Name,
				line:      node.Line,
			})
		}
		for _, p := range pairs {
			allResolved = append(allResolved, resolvedUsage{
				varName:   p.name,
				value:     p.value,
				blockType: rbast.BlockTypeRollback,
				blockName: node.Name,
				line:      node.Line,
			})
		}
	}

	// Process waits.
	for i := range ast.Waits {
		node := &ast.Waits[i]
		node.OriginalCommand = node.Command
		resolved, refs, pairs := substituteVars(node.Command, context)
		node.Command = resolved
		for _, r := range refs {
			unresolved = append(unresolved, varRef{
				name:      r,
				blockType: rbast.BlockTypeWait,
				blockName: node.Name,
				line:      node.Line,
			})
		}
		for _, p := range pairs {
			allResolved = append(allResolved, resolvedUsage{
				varName:   p.name,
				value:     p.value,
				blockType: rbast.BlockTypeWait,
				blockName: node.Name,
				line:      node.Line,
			})
		}
	}

	if len(unresolved) > 0 {
		ref := unresolved[0]
		return fmt.Errorf("%s:%d: unresolved variable {{%s}} in %s %q",
			ast.FilePath, ref.line, ref.name, ref.blockType, ref.blockName)
	}

	// Scan every resolved variable value for dangerous shell metacharacters.
	var warnings []MetacharWarning
	for _, usage := range allResolved {
		if mc := findFirstMetachar(usage.value); mc != "" {
			warnings = append(warnings, MetacharWarning{
				VarName:   usage.varName,
				Value:     usage.value,
				Metachar:  mc,
				BlockType: usage.blockType,
				BlockName: usage.blockName,
				FilePath:  ast.FilePath,
				Line:      usage.line,
			})
		}
	}

	if len(warnings) == 0 {
		return nil
	}

	w := opts.Stderr
	if w == nil {
		w = os.Stderr
	}

	for _, warn := range warnings {
		printMetacharWarning(w, warn)
	}

	// Strict mode: turn warnings into a hard error regardless of interactivity.
	if opts.Strict {
		return &MetacharError{Warnings: warnings}
	}

	// Dry-run or non-interactive: log warnings and continue.
	if opts.DryRun || opts.NonInteractive {
		return nil
	}

	// Interactive mode: prompt the user.
	r := opts.PromptInput
	if r == nil {
		r = os.Stdin
	}
	if !promptMetacharContinue(w, r) {
		return &MetacharError{Warnings: warnings}
	}
	return nil
}

// promptMetacharContinue asks the user whether to continue despite dangerous
// variable values. Returns true to continue, false to abort. Defaults to false
// (no) on empty input or EOF.
func promptMetacharContinue(w io.Writer, r io.Reader) bool {
	_, _ = fmt.Fprintf(w, "Continue with potentially dangerous values? [y/n]: ")
	scanner := bufio.NewScanner(r)
	if scanner.Scan() {
		input := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return input == "y" || input == "yes"
	}
	return false
}

// substituteVars replaces all {{variable}} references in cmd with values from
// the context. Returns the resolved string, a list of variable names that could
// not be resolved, and a list of (name, value) pairs for each resolved variable
// (deduplicated per command).
func substituteVars(cmd string, context map[string]string) (string, []string, []varPair) {
	var unresolved []string
	var resolved []varPair
	seen := make(map[string]bool)
	result := varPattern.ReplaceAllStringFunc(cmd, func(match string) string {
		// Extract variable name from {{name}}.
		name := match[2 : len(match)-2]
		if val, ok := context[name]; ok {
			if !seen[name] {
				seen[name] = true
				resolved = append(resolved, varPair{name: name, value: val})
			}
			return val
		}
		unresolved = append(unresolved, name)
		return match
	})
	return result, unresolved, resolved
}

// filterByEnv removes steps whose env list doesn't include the target environment.
// Blocks without an env filter are kept (they run in all environments).
func filterByEnv(ast *rbast.RunbookAST, targetEnv string) {
	filtered := ast.Steps[:0]
	for _, step := range ast.Steps {
		if len(step.Env) == 0 || containsEnv(step.Env, targetEnv) {
			filtered = append(filtered, step)
		}
	}
	ast.Steps = filtered
}

// containsEnv checks if the env list contains the target environment.
func containsEnv(envs []string, target string) bool {
	for _, e := range envs {
		if strings.EqualFold(e, target) {
			return true
		}
	}
	return false
}

// buildBuiltins returns the built-in variable map.
func buildBuiltins(ast *rbast.RunbookAST, targetEnv string) map[string]string {
	vars := map[string]string{
		builtinVarEnv:            targetEnv,
		builtinVarRunbookName:    ast.Metadata.Name,
		builtinVarRunbookVersion: ast.Metadata.Version,
		builtinVarRunID:          uuid.New().String(),
		builtinVarTimestamp:      time.Now().UTC().Format(time.RFC3339),
	}

	if u, err := user.Current(); err == nil {
		vars[builtinVarUser] = u.Username
	}
	if h, err := os.Hostname(); err == nil {
		vars[builtinVarHostname] = h
	}
	if cwd, err := os.Getwd(); err == nil {
		vars[builtinVarCWD] = cwd
	}

	return vars
}

// warnDotEnvPermissions checks that the .env file at path is not more
// permissive than 0600 (owner read/write only). If it is, a warning is written
// to w. The check is skipped on Windows where Unix permission bits do not apply.
func warnDotEnvPermissions(path string, w io.Writer) {
	if runtime.GOOS == "windows" {
		return
	}

	if w == nil {
		w = os.Stderr
	}

	info, err := os.Stat(path)
	if err != nil {
		return
	}

	perm := info.Mode().Perm()
	if perm&^os.FileMode(0o600) != 0 {
		fmt.Fprintf(w, "⚠ .env file has permissions %04o (world-readable). Run: chmod 600 %s\n",
			perm, path)
	}
}

// warnDotEnvPath warns when the env file resolves to a location outside the
// user's home directory and the runbook's parent directory. An env file
// pointing at (say) /etc is not necessarily malicious but it is out-of-band
// enough that the operator should confirm the path is trusted.
//
// runbookDir may be empty when no runbook is in scope; in that case only the
// home directory is considered a safe root.
func warnDotEnvPath(envFile, runbookDir string, w io.Writer) {
	if envFile == "" {
		return
	}

	if w == nil {
		w = os.Stderr
	}

	abs, err := filepath.Abs(envFile)
	if err != nil {
		return
	}

	if isUnderSafeRoot(abs, runbookDir) {
		return
	}

	fmt.Fprintf(w,
		"⚠ env_file %s resolves outside the runbook's directory and $HOME; "+
			"make sure the path is trusted\n",
		abs)
}

// isUnderSafeRoot reports whether absPath sits inside either the user's home
// directory or the given runbook directory (when non-empty).
func isUnderSafeRoot(absPath, runbookDir string) bool {
	candidates := make([]string, 0, 2)
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, home)
	}

	if runbookDir != "" {
		if rd, err := filepath.Abs(runbookDir); err == nil {
			candidates = append(candidates, rd)
		}
	}

	for _, root := range candidates {
		if pathHasPrefix(absPath, root) {
			return true
		}
	}
	return false
}

// pathHasPrefix reports whether target sits inside prefix. Both paths are
// treated as cleaned absolute paths. The check compares complete segments so
// "/foo/bar" is not considered a child of "/foo/b".
func pathHasPrefix(target, prefix string) bool {
	target = filepath.Clean(target)
	prefix = filepath.Clean(prefix)

	if target == prefix {
		return true
	}

	sep := string(filepath.Separator)
	return strings.HasPrefix(target, prefix+sep)
}
