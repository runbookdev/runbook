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

package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// configFileMode is the expected permission mode of the config file. The file
// may contain paths to secret-bearing resources (env files, audit DB), so it
// is treated as sensitive.
const configFileMode = 0o600

// configPermMask matches group/other permission bits. Any of these set means
// the file is more permissive than configFileMode.
const configPermMask = 0o077

// Config holds persistent user settings loaded from ~/.runbook/config.yaml.
type Config struct {
	// Env is the default target environment (--env).
	Env string `yaml:"env"`
	// EnvFile is the default .env file path (--env-file).
	EnvFile string `yaml:"env_file"`
	// AuditDir overrides the default audit database path.
	AuditDir string `yaml:"audit_dir"`
	// NonInteractive disables every confirmation prompt (--non-interactive).
	NonInteractive bool `yaml:"non_interactive"`
	// NoColor disables ANSI colour in CLI output (--no-color).
	NoColor bool `yaml:"no_color"`
	// Shell overrides DefaultShell (--shell).
	Shell string `yaml:"shell"`
}

// configPath returns the default config file location.
func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".runbook", "config.yaml")
}

// loadConfig reads the config file if it exists. Returns a zero Config and an
// empty warning slice when the file is absent. Warnings cover recoverable
// problems (bad permissions, unknown keys) so callers can surface them once;
// a non-nil error indicates the config was unusable and defaults were applied.
func loadConfig() (Config, []string, error) {
	var cfg Config
	path := configPath()
	if path == "" {
		return cfg, nil, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil, nil
		}
		return cfg, nil, fmt.Errorf("stat %s: %w", path, err)
	}

	var warnings []string
	if w := permWarning(path, info.Mode().Perm()); w != "" {
		warnings = append(warnings, w)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, warnings, fmt.Errorf("read %s: %w", path, err)
	}

	// KnownFields(true) surfaces typos in config keys instead of silently
	// ignoring them.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		warnings = append(warnings, fmt.Sprintf(
			"⚠ config %s: %v — using defaults", path, err))
		return Config{}, warnings, nil
	}

	return cfg, warnings, nil
}

// permWarning returns a non-empty advisory when path is more permissive than
// configFileMode. The check is skipped on Windows where Unix permission bits
// do not apply.
func permWarning(path string, perm os.FileMode) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	if perm&configPermMask == 0 {
		return ""
	}
	return fmt.Sprintf(
		"⚠ config %s has permissions %04o; run: chmod %04o %s",
		path, perm, configFileMode, path)
}

// printConfigWarnings writes any advisory messages from loadConfig to stderr.
// Prefixed with "[runbook] " to match the convention used elsewhere.
func printConfigWarnings(warnings []string) {
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "[runbook] %s\n", w)
	}
}

// safeEnvFileRoots returns the directories an env_file path is expected to
// live under. runbookDir may be empty when no runbook is being executed (e.g.
// from `runbook doctor`), in which case only the home directory is returned.
func safeEnvFileRoots(runbookDir string) []string {
	roots := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, home)
	}
	if runbookDir != "" {
		if abs, err := filepath.Abs(runbookDir); err == nil {
			roots = append(roots, abs)
		}
	}
	return roots
}

// envFilePathWarning returns a non-empty advisory when envFile resolves to a
// path outside every configured safe root. Empty envFile yields no warning.
func envFilePathWarning(envFile, runbookDir string) string {
	if envFile == "" {
		return ""
	}

	abs, err := filepath.Abs(envFile)
	if err != nil {
		return ""
	}

	for _, root := range safeEnvFileRoots(runbookDir) {
		if pathHasPrefix(abs, root) {
			return ""
		}
	}

	return fmt.Sprintf(
		"⚠ env_file %s resolves outside the runbook's directory and $HOME; "+
			"make sure the path is trusted",
		abs)
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
