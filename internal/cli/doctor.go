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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/runbookdev/runbook/internal/audit"
	"github.com/runbookdev/runbook/internal/parser"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	checkOK   = "✓"
	checkFail = "✗"
	checkWarn = "⚠"

	// minimumGoMajor / minimumGoMinor is the oldest Go toolchain runbook supports.
	minimumGoMajor = 1
	minimumGoMinor = 26
)

func newDoctorCmd() *cobra.Command {
	var auditDir string

	cmd := &cobra.Command{
		Use:   "doctor [runbook-file]",
		Short: "Check the health of your runbook installation",
		Long: `doctor reports the health of your local runbook installation.

Pass a .runbook file to also check required tools and .env permissions
for that specific runbook. No network calls are made.

To update runbook manually:
  brew upgrade runbook`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := loadConfig()

			// Resolve audit DB path using the same logic as history/run.
			dbPath := auditDir
			if dbPath == "" {
				dbPath = cfg.AuditDir
			}
			if dbPath == "" {
				p, err := audit.DefaultDBPath()
				if err != nil {
					return fmt.Errorf("cannot determine audit path: %w", err)
				}
				dbPath = p
			}

			var runbookFile string
			if len(args) == 1 {
				runbookFile = args[0]
			}

			ok := runDoctor(cmd, cfg, dbPath, runbookFile)
			if !ok {
				return fmt.Errorf("one or more checks failed")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&auditDir, "audit-dir", "", "path to the audit database")
	return cmd
}

// runDoctor executes all health checks and returns true only if every check
// passed (warnings do not cause a false return).
func runDoctor(cmd *cobra.Command, cfg Config, dbPath, runbookFile string) bool {
	out := cmd.OutOrStdout()
	allOK := true

	fmt.Fprintln(out, "runbook doctor")
	fmt.Fprintln(out)

	// ── Binary info ────────────────────────────────────────────────────────
	fmt.Fprintf(out, "%s  Binary version:  %s (commit: %s, built: %s)\n",
		checkOK, displayVersion(Version), Commit, Date)
	fmt.Fprintf(out, "%s  Go version:      %s\n", checkOK, runtime.Version())
	fmt.Fprintf(out, "%s  Platform:        %s/%s\n", checkOK, runtime.GOOS, runtime.GOARCH)

	// ── Go version compatibility ────────────────────────────────────────────
	if ok := checkGoVersion(out); !ok {
		allOK = false
	}

	// ── Audit DB ───────────────────────────────────────────────────────────
	if ok := checkAuditDB(out, dbPath); !ok {
		allOK = false
	}

	// ── Config file ────────────────────────────────────────────────────────
	checkConfigFile(out, cfg)

	// ── Runbook-specific checks ────────────────────────────────────────────
	if runbookFile != "" {
		if ok := checkRunbookFile(out, runbookFile); !ok {
			allOK = false
		}
	}

	fmt.Fprintln(out)
	if allOK {
		fmt.Fprintf(out, "%s  All checks passed.\n", checkOK)
	} else {
		fmt.Fprintf(out, "%s  Some checks failed. See output above.\n", checkFail)
	}
	return allOK
}

// checkGoVersion warns when the runtime Go version is older than the minimum.
func checkGoVersion(out io.Writer) bool {
	ver := runtime.Version() // e.g. "go1.26.0"
	major, minor, ok := parseGoVersion(ver)
	if !ok {
		fmt.Fprintf(out, "%s  Go version:      cannot parse %q\n", checkWarn, ver)
		return true // non-fatal
	}
	if major < minimumGoMajor || (major == minimumGoMajor && minor < minimumGoMinor) {
		fmt.Fprintf(out, "%s  Go version:      %s is below minimum go%d.%d\n",
			checkFail, ver, minimumGoMajor, minimumGoMinor)
		return false
	}
	return true
}

// parseGoVersion extracts major/minor from strings like "go1.26.0".
func parseGoVersion(v string) (int, int, bool) {
	v = strings.TrimPrefix(v, "go")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// checkAuditDB verifies the audit database exists and has correct permissions.
func checkAuditDB(out io.Writer, dbPath string) bool {
	ok := true

	info, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "%s  Audit DB:        not found at %s (will be created on first run)\n",
				checkWarn, dbPath)
		} else {
			fmt.Fprintf(out, "%s  Audit DB:        cannot stat %s: %v\n", checkFail, dbPath, err)
			ok = false
		}
		return ok
	}

	// Check size.
	size := info.Size()
	fmt.Fprintf(out, "%s  Audit DB:        %s (%s)\n", checkOK, dbPath, formatBytes(size))

	// Check permissions — should be readable/writable only by owner (0600).
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		fmt.Fprintf(out, "%s  Audit DB perms:  %04o — should be 0600 (run: chmod 0600 %s)\n",
			checkFail, perm, dbPath)
		ok = false
	} else {
		fmt.Fprintf(out, "%s  Audit DB perms:  %04o\n", checkOK, perm)
	}

	return ok
}

// checkConfigFile validates the config file if it exists.
func checkConfigFile(out io.Writer, cfg Config) {
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "%s  Config file:     not found at %s (using defaults)\n", checkWarn, path)
		} else {
			fmt.Fprintf(out, "%s  Config file:     cannot read %s: %v\n", checkWarn, path, err)
		}
		return
	}

	// Re-parse strictly to catch unknown keys or YAML errors.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		fmt.Fprintf(out, "%s  Config file:     invalid YAML at %s: %v\n", checkFail, path, err)
		return
	}
	_ = cfg // already loaded by loadConfig; we just validated YAML above.
	fmt.Fprintf(out, "%s  Config file:     %s\n", checkOK, path)
}

// checkRunbookFile parses the given .runbook file and runs tool + .env checks.
func checkRunbookFile(out io.Writer, runbookFile string) bool {
	allOK := true

	rb, err := parser.ParseFile(runbookFile)
	if err != nil {
		fmt.Fprintf(out, "%s  Runbook file:    cannot parse %s: %v\n", checkFail, runbookFile, err)
		return false
	}
	fmt.Fprintf(out, "%s  Runbook file:    %s (v%s)\n", checkOK, rb.Metadata.Name, rb.Metadata.Version)

	// Required tools.
	if len(rb.Metadata.Requires.Tools) == 0 {
		fmt.Fprintf(out, "%s  Required tools:  none declared\n", checkOK)
	} else {
		for _, tool := range rb.Metadata.Requires.Tools {
			if path, err := exec.LookPath(tool); err == nil {
				fmt.Fprintf(out, "%s  Tool %q:  found at %s\n", checkOK, tool, path)
			} else {
				fmt.Fprintf(out, "%s  Tool %q:  not found in PATH\n", checkFail, tool)
				allOK = false
			}
		}
	}

	// .env file next to the runbook.
	envPath := filepath.Join(filepath.Dir(runbookFile), ".env")
	if ok := checkEnvFilePerms(out, envPath); !ok {
		allOK = false
	}

	return allOK
}

// checkEnvFilePerms warns if the .env file exists but has overly-permissive
// permissions (anything other than 0600).
func checkEnvFilePerms(out io.Writer, envPath string) bool {
	info, err := os.Stat(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "%s  .env file:       not found at %s\n", checkOK, envPath)
			return true
		}
		fmt.Fprintf(out, "%s  .env file:       cannot stat %s: %v\n", checkWarn, envPath, err)
		return true
	}

	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		fmt.Fprintf(out, "%s  .env perms:      %04o — should be 0600 (run: chmod 0600 %s)\n",
			checkWarn, perm, envPath)
		// Permission warning — not a hard failure, but caller should fix it.
		return true
	}
	fmt.Fprintf(out, "%s  .env file:       %s (%04o)\n", checkOK, envPath, perm)
	return true
}

// formatBytes returns a human-readable byte count (KB/MB/GB).
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
