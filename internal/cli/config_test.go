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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withHome temporarily points $HOME at a fresh temp directory and seeds the
// config file with the given content and permission mode. Returns the
// directory so tests can write additional fixtures next to the config.
func withHome(t *testing.T, configContent string, configPerm os.FileMode) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".runbook")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if configContent != "" {
		path := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(path, []byte(configContent), configPerm); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	return home
}

func TestLoadConfig_MissingFileIsOK(t *testing.T) {
	withHome(t, "", 0)

	cfg, warnings, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if cfg != (Config{}) {
		t.Errorf("expected zero Config, got %+v", cfg)
	}
}

func TestLoadConfig_PermissionWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}

	withHome(t, "env: staging\n", 0o644)

	_, warnings, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(warnings) == 0 {
		t.Fatal("expected a permission warning for 0644 config")
	}
	if !strings.Contains(warnings[0], "permissions") {
		t.Errorf("expected permissions warning, got %q", warnings[0])
	}
}

func TestLoadConfig_UnknownKeyWarns(t *testing.T) {
	withHome(t, "env: staging\nnot_a_real_key: hello\n", 0o600)

	cfg, warnings, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Env != "" {
		t.Errorf("expected Config to fall back to defaults, got %+v", cfg)
	}

	if len(warnings) == 0 {
		t.Fatal("expected a warning for unknown key")
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "not_a_real_key") {
		t.Errorf("expected warning to mention the unknown key, got %q", joined)
	}
}

func TestLoadConfig_ValidKnownKeys(t *testing.T) {
	withHome(t, "env: staging\nnon_interactive: true\n", 0o600)

	cfg, warnings, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, w := range warnings {
		if strings.Contains(w, "permissions") {
			t.Errorf("did not expect permission warning on 0600 file: %q", w)
		}
	}

	if cfg.Env != "staging" || !cfg.NonInteractive {
		t.Errorf("expected env=staging and non_interactive=true, got %+v", cfg)
	}
}

func TestEnvFilePathWarning(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}

	runbookDir := t.TempDir()

	cases := []struct {
		name     string
		envFile  string
		wantWarn bool
	}{
		{"empty path", "", false},
		{"inside runbook dir", filepath.Join(runbookDir, ".env"), false},
		{"inside home", filepath.Join(home, "secrets.env"), false},
		{"outside both", "/etc/runbook.env", true},
		{"traversal above runbook dir", filepath.Join(runbookDir, "..", "..", "etc", "passwd"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := envFilePathWarning(tc.envFile, runbookDir)
			if tc.wantWarn && got == "" {
				t.Errorf("expected warning for %q, got none", tc.envFile)
			}
			if !tc.wantWarn && got != "" {
				t.Errorf("expected no warning for %q, got %q", tc.envFile, got)
			}
		})
	}
}

func TestPathHasPrefix(t *testing.T) {
	cases := []struct {
		target string
		prefix string
		want   bool
	}{
		{"/foo/bar", "/foo", true},
		{"/foo", "/foo", true},
		{"/foo/bar", "/foo/b", false},
		{"/other/path", "/foo", false},
		{"/foo/../bar", "/bar", true},
	}

	for _, tc := range cases {
		got := pathHasPrefix(tc.target, tc.prefix)
		if got != tc.want {
			t.Errorf("pathHasPrefix(%q, %q) = %v, want %v", tc.target, tc.prefix, got, tc.want)
		}
	}
}
