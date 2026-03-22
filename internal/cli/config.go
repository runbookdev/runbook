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

	"gopkg.in/yaml.v3"
)

// Config holds persistent user settings loaded from ~/.runbook/config.yaml.
type Config struct {
	Env            string `yaml:"env"`
	EnvFile        string `yaml:"env_file"`
	AuditDir       string `yaml:"audit_dir"`
	NonInteractive bool   `yaml:"non_interactive"`
	NoColor        bool   `yaml:"no_color"`
	Shell          string `yaml:"shell"`
}

// configPath returns the default config file location.
func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".runbook", "config.yaml")
}

// loadConfig reads the config file if it exists. Returns a zero Config
// and no error if the file is absent.
func loadConfig() Config {
	var cfg Config
	path := configPath()
	if path == "" {
		return cfg
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	_ = yaml.Unmarshal(data, &cfg)
	return cfg
}
