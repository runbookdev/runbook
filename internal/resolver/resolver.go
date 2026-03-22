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
	"fmt"
	"maps"
	"os"
	"os/user"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	rbast "github.com/runbookdev/runbook/internal/ast"
)

// varPattern matches {{variable}} references in command bodies.
var varPattern = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)

// SecretProvider resolves secret values by key.
type SecretProvider interface {
	// Resolve returns the value for the given key.
	Resolve(key string) (string, error)
	// Name returns a human-readable name for this provider.
	Name() string
}

// EnvProvider resolves secrets from environment variables with a RUNBOOK_ prefix.
type EnvProvider struct{}

// Resolve returns the value of the RUNBOOK_<key> environment variable.
func (p *EnvProvider) Resolve(key string) (string, error) {
	val, ok := os.LookupEnv("RUNBOOK_" + strings.ToUpper(key))
	if !ok {
		return "", fmt.Errorf("environment variable RUNBOOK_%s not set", strings.ToUpper(key))
	}
	return val, nil
}

// Name returns the provider name.
func (p *EnvProvider) Name() string { return "env" }

// DotEnvProvider resolves secrets from a .env file.
type DotEnvProvider struct {
	vars map[string]string
}

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
func (p *DotEnvProvider) Name() string { return "dotenv" }

// varRef tracks where a variable is referenced.
type varRef struct {
	name      string
	blockType string
	blockName string
	line      int
}

// Resolve resolves all template variables in the AST, filters blocks by target
// environment, and stores both original and resolved commands on each node.
func Resolve(ast *rbast.RunbookAST, targetEnv string, cliVars map[string]string, envFilePath string) error {
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
	}

	// 3. Environment variables with RUNBOOK_ prefix.
	envVars := make(map[string]string)
	for _, kv := range os.Environ() {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && strings.HasPrefix(parts[0], "RUNBOOK_") {
			key := strings.TrimPrefix(parts[0], "RUNBOOK_")
			key = strings.ToLower(key)
			envVars[key] = parts[1]
		}
	}
	maps.Copy(context, envVars)

	// 4. CLI flags (highest priority).
	maps.Copy(context, cliVars)

	// Filter blocks by target environment.
	if targetEnv != "" {
		filterByEnv(ast, targetEnv)
	}

	// Collect all variable references and substitute.
	var unresolved []varRef

	// Process checks.
	for i := range ast.Checks {
		node := &ast.Checks[i]
		node.OriginalCommand = node.Command
		resolved, refs := substituteVars(node.Command, context)
		node.Command = resolved
		for _, r := range refs {
			unresolved = append(unresolved, varRef{
				name:      r,
				blockType: "check",
				blockName: node.Name,
				line:      node.Line,
			})
		}
	}

	// Process steps.
	for i := range ast.Steps {
		node := &ast.Steps[i]
		node.OriginalCommand = node.Command
		resolved, refs := substituteVars(node.Command, context)
		node.Command = resolved
		for _, r := range refs {
			unresolved = append(unresolved, varRef{
				name:      r,
				blockType: "step",
				blockName: node.Name,
				line:      node.Line,
			})
		}
	}

	// Process rollbacks.
	for i := range ast.Rollbacks {
		node := &ast.Rollbacks[i]
		node.OriginalCommand = node.Command
		resolved, refs := substituteVars(node.Command, context)
		node.Command = resolved
		for _, r := range refs {
			unresolved = append(unresolved, varRef{
				name:      r,
				blockType: "rollback",
				blockName: node.Name,
				line:      node.Line,
			})
		}
	}

	// Process waits.
	for i := range ast.Waits {
		node := &ast.Waits[i]
		node.OriginalCommand = node.Command
		resolved, refs := substituteVars(node.Command, context)
		node.Command = resolved
		for _, r := range refs {
			unresolved = append(unresolved, varRef{
				name:      r,
				blockType: "wait",
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

	return nil
}

// substituteVars replaces all {{variable}} references in cmd with values from
// the context. Returns the resolved string and a list of variable names that
// could not be resolved.
func substituteVars(cmd string, context map[string]string) (string, []string) {
	var unresolved []string
	result := varPattern.ReplaceAllStringFunc(cmd, func(match string) string {
		// Extract variable name from {{name}}.
		name := match[2 : len(match)-2]
		if val, ok := context[name]; ok {
			return val
		}
		unresolved = append(unresolved, name)
		return match
	})
	return result, unresolved
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
		"env":             targetEnv,
		"runbook_name":    ast.Metadata.Name,
		"runbook_version": ast.Metadata.Version,
		"run_id":          uuid.New().String(),
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	}

	if u, err := user.Current(); err == nil {
		vars["user"] = u.Username
	}
	if h, err := os.Hostname(); err == nil {
		vars["hostname"] = h
	}
	if cwd, err := os.Getwd(); err == nil {
		vars["cwd"] = cwd
	}

	return vars
}
