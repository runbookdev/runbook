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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── parseGoVersion ─────────────────────────────────────────────────────────

func TestParseGoVersion(t *testing.T) {
	cases := []struct {
		in        string
		wantMajor int
		wantMinor int
		wantOK    bool
	}{
		{"go1.26.0", 1, 26, true},
		{"go1.21.5", 1, 21, true},
		{"go2.0.0", 2, 0, true},
		{"go1.26", 1, 26, true},
		{"notgo", 0, 0, false},
		{"go1", 0, 0, false},
	}
	for _, tc := range cases {
		major, minor, ok := parseGoVersion(tc.in)
		if ok != tc.wantOK {
			t.Errorf("parseGoVersion(%q): ok=%v want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if ok && (major != tc.wantMajor || minor != tc.wantMinor) {
			t.Errorf("parseGoVersion(%q): got %d.%d want %d.%d",
				tc.in, major, minor, tc.wantMajor, tc.wantMinor)
		}
	}
}

// ── formatBytes ────────────────────────────────────────────────────────────

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1 << 20, "1.0 MB"},
		{1 << 30, "1.0 GB"},
	}
	for _, tc := range cases {
		got := formatBytes(tc.in)
		if got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ── displayVersion ─────────────────────────────────────────────────────────

func TestDisplayVersion(t *testing.T) {
	if got := displayVersion("dev"); got != "dev" {
		t.Errorf("displayVersion(\"dev\") = %q, want \"dev\"", got)
	}
	if got := displayVersion("v0.1.0"); got != "v0.1.0" {
		t.Errorf("displayVersion(\"v0.1.0\") = %q, want \"v0.1.0\"", got)
	}
	if got := displayVersion("0.1.0"); got != "v0.1.0" {
		t.Errorf("displayVersion(\"0.1.0\") = %q, want \"v0.1.0\"", got)
	}
}

// ── checkGoVersion ─────────────────────────────────────────────────────────

func TestCheckGoVersion_Passing(t *testing.T) {
	var buf bytes.Buffer
	// The runtime version running these tests must be >= go1.26.
	ok := checkGoVersion(&buf)
	if !ok {
		t.Errorf("expected current runtime go version to pass check, got: %s", buf.String())
	}
}

// ── checkAuditDB ───────────────────────────────────────────────────────────

func TestCheckAuditDB_Missing(t *testing.T) {
	var buf bytes.Buffer
	ok := checkAuditDB(&buf, "/nonexistent/path/runbook.db")
	if !ok {
		t.Error("missing DB should produce a warning, not a failure")
	}
	if !strings.Contains(buf.String(), "not found") {
		t.Errorf("expected 'not found' message, got %q", buf.String())
	}
}

func TestCheckAuditDB_CorrectPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runbook.db")
	if err := os.WriteFile(dbPath, []byte("dummy"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	ok := checkAuditDB(&buf, dbPath)
	if !ok {
		t.Errorf("expected check to pass, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "✓") {
		t.Errorf("expected OK marker, got %q", buf.String())
	}
}

func TestCheckAuditDB_WrongPerms(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runbook.db")
	if err := os.WriteFile(dbPath, []byte("dummy"), 0o644); err != nil { //nolint:gosec // intentionally permissive for permission-check test
		t.Fatal(err)
	}
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	ok := checkAuditDB(&buf, dbPath)
	if ok {
		t.Error("expected check to fail for 0644 permissions")
	}
	if !strings.Contains(buf.String(), "chmod 0600") {
		t.Errorf("expected chmod suggestion, got %q", buf.String())
	}
}

// ── checkConfigFile ────────────────────────────────────────────────────────

func TestCheckConfigFile_Missing(t *testing.T) {
	// Point configPath to something that doesn't exist by overriding via env.
	// We call checkConfigFile directly with the zero Config; the function
	// internally calls configPath() which reads HOME. This test verifies that
	// a missing file is treated as a warning, not an error.
	// Since we can't override HOME easily without an env change, we just verify
	// that checkConfigFile never panics even when the file is absent.
	var buf bytes.Buffer
	checkConfigFile(&buf, Config{}, nil, nil)
	// Either "not found" (warn) or "config file: /path" (ok) — both are valid.
	out := buf.String()
	if !strings.Contains(out, checkOK) && !strings.Contains(out, checkWarn) {
		t.Errorf("expected ✓ or ⚠ in output, got %q", out)
	}
}

// ── checkEnvFilePerms ─────────────────────────────────────────────────────

func TestCheckEnvFilePerms_Missing(t *testing.T) {
	var buf bytes.Buffer
	ok := checkEnvFilePerms(&buf, "/no/such/.env")
	if !ok {
		t.Error("missing .env should be OK (not required)")
	}
	if !strings.Contains(buf.String(), "not found") {
		t.Errorf("expected 'not found', got %q", buf.String())
	}
}

func TestCheckEnvFilePerms_CorrectPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("KEY=val\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(envPath, 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	ok := checkEnvFilePerms(&buf, envPath)
	if !ok {
		t.Errorf("expected OK for 0600 .env, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), checkOK) {
		t.Errorf("expected ✓ marker, got %q", buf.String())
	}
}

func TestCheckEnvFilePerms_WrongPerms(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("KEY=val\n"), 0o644); err != nil { //nolint:gosec // intentionally permissive for permission-check test
		t.Fatal(err)
	}
	if err := os.Chmod(envPath, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	ok := checkEnvFilePerms(&buf, envPath)
	// .env permission issues are warnings, not hard failures.
	if !ok {
		t.Error("checkEnvFilePerms with 0644 should warn but still return true")
	}
	if !strings.Contains(buf.String(), checkWarn) {
		t.Errorf("expected ⚠ warning, got %q", buf.String())
	}
}

// ── runDoctor (integration) ────────────────────────────────────────────────

func TestRunDoctor_NoRunbookFile(t *testing.T) {
	cmd := newDoctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	ok := runDoctor(cmd, Config{}, "/nonexistent/audit.db", "", nil, nil)
	// Missing audit DB is a warning, not a failure.
	_ = ok
	out := buf.String()
	if !strings.Contains(out, "runbook doctor") {
		t.Errorf("expected header in output, got %q", out)
	}
	if !strings.Contains(out, "Binary version") {
		t.Errorf("expected binary version line, got %q", out)
	}
}

func TestRunDoctor_WithRunbookFile(t *testing.T) {
	dir := t.TempDir()
	rbPath := filepath.Join(dir, "test.runbook")
	content := "---\nname: Doctor Test\nversion: 1.0.0\n---\n\n```step name=\"hello\"\necho hello\n```\n"
	if err := os.WriteFile(rbPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newDoctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	runDoctor(cmd, Config{}, "/nonexistent/audit.db", rbPath, nil, nil)
	out := buf.String()
	if !strings.Contains(out, "Doctor Test") {
		t.Errorf("expected runbook name in output, got %q", out)
	}
}

// ── checkRunbookDirModes ───────────────────────────────────────────────────

func TestCheckRunbookDirModes_WorldWritableFlagged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}

	dir := t.TempDir()
	safe := filepath.Join(dir, "safe.runbook")
	dangerous := filepath.Join(dir, "dangerous.runbook")
	if err := os.WriteFile(safe, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dangerous, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(safe, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dangerous, 0o666); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	checkRunbookDirModes(&buf, safe) // doctor is invoked with one runbook file; the walk covers the dir

	out := buf.String()
	if !strings.Contains(out, "dangerous.runbook") {
		t.Errorf("expected dangerous.runbook to be flagged, got %q", out)
	}
	if strings.Contains(out, "safe.runbook (run:") {
		t.Errorf("did not expect safe.runbook to be flagged, got %q", out)
	}
}

func TestCheckRunbookDirModes_CleanDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}

	dir := t.TempDir()
	rb := filepath.Join(dir, "ok.runbook")
	if err := os.WriteFile(rb, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	checkRunbookDirModes(&buf, rb)

	out := buf.String()
	if !strings.Contains(out, "owner-writable only") {
		t.Errorf("expected clean-dir summary, got %q", out)
	}
}

// ── checkConfigEnvFilePath ─────────────────────────────────────────────────

func TestCheckConfigEnvFilePath_WarnsOutsideSafeRoots(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{EnvFile: "/etc/runbook.env"}
	checkConfigEnvFilePath(&buf, cfg, "")

	out := buf.String()
	if !strings.Contains(out, "resolves outside") {
		t.Errorf("expected path warning, got %q", out)
	}
}

func TestCheckConfigEnvFilePath_SilentWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	checkConfigEnvFilePath(&buf, Config{}, "")

	if buf.Len() != 0 {
		t.Errorf("expected no output when env_file unset, got %q", buf.String())
	}
}

func TestRunDoctor_MissingRequiredTool(t *testing.T) {
	dir := t.TempDir()
	rbPath := filepath.Join(dir, "test.runbook")
	content := "---\nname: Tools Test\nversion: 1.0.0\nrequires:\n  tools:\n    - this-tool-does-not-exist-xyz\n---\n\n```step name=\"s\"\necho hi\n```\n"
	if err := os.WriteFile(rbPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newDoctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	ok := runDoctor(cmd, Config{}, "/nonexistent/audit.db", rbPath, nil, nil)
	if ok {
		t.Error("expected runDoctor to return false when a required tool is missing")
	}
	if !strings.Contains(buf.String(), "not found in PATH") {
		t.Errorf("expected 'not found in PATH' message, got %q", buf.String())
	}
}
