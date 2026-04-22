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

// Package audit provides SQLite-backed audit logging for runbook executions.
// Every run and its step-level details are persisted so operators can review
// execution history via the CLI.
package audit

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver
)

// SecretPatterns are substrings in variable names (case-insensitive) that
// trigger auto-redaction. The list is exported so callers can reference the
// same set without duplicating it.
var SecretPatterns = []string{
	"SECRET", "PASSWORD", "TOKEN", "KEY", "CREDENTIAL",
	"API_KEY", "APIKEY", "AUTH", "PRIVATE", "PASSPHRASE",
	"CERT", "CONNECTION_STRING",
}

// redactedValue is the replacement for sensitive variable values.
const redactedValue = "[REDACTED]"

// maxOutputBytes is the maximum number of bytes stored for stdout/stderr.
// Outputs exceeding this limit are truncated before storage.
const maxOutputBytes = 1 << 20 // 1 MB

// outputTruncatedMarker is appended to truncated outputs.
const outputTruncatedMarker = "[OUTPUT TRUNCATED AT 1 MB]"

// DefaultDBPath returns the default audit database path: ~/.runbook/audit/runbook.db.
func DefaultDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".runbook", "audit", "runbook.db"), nil
}

// RunRecord represents a single runbook execution.
type RunRecord struct {
	// ID is the unique run identifier (e.g. "run_a3f1c2d4").
	ID string
	// Runbook is the source file path that was executed.
	Runbook string
	// Name mirrors metadata.name at the time of the run.
	Name string
	// Version mirrors metadata.version at the time of the run.
	Version string
	// Environment is the target environment ("staging", "production", …).
	Environment string
	// StartedAt is the UTC timestamp when execution began.
	StartedAt time.Time
	// FinishedAt is the UTC timestamp when execution ended; nil while running.
	FinishedAt *time.Time
	// Status is the final RunStatus label (e.g. "success", "rolled_back").
	Status string
	// User is the OS user who started the run.
	User string
	// Hostname is the host where the run executed.
	Hostname string
	// Variables holds the CLI-provided variables, with secrets redacted before storage.
	Variables map[string]string
}

// StepLog represents the outcome of a single block execution within a run.
type StepLog struct {
	// ID is the auto-assigned primary key.
	ID int64
	// RunID is the owning run's identifier.
	RunID string
	// StepName is the block's name attribute.
	StepName string
	// BlockType is one of the ast.BlockType* constants.
	BlockType string
	// StartedAt is the UTC timestamp when the block began.
	StartedAt time.Time
	// FinishedAt is the UTC timestamp when the block ended.
	FinishedAt time.Time
	// ExitCode is the subprocess exit code (-1 on timeout).
	ExitCode int
	// Status is the per-block outcome label.
	Status string
	// Stdout is the captured (possibly truncated) standard output.
	Stdout string
	// Stderr is the captured (possibly truncated) standard error.
	Stderr string
	// Command is the executed command, with secrets redacted before storage.
	Command string
	// Secrets holds the resolved secret variable values used to redact Command,
	// Stdout, and Stderr before they are persisted. Not stored in the database.
	Secrets map[string]string
}

// Logger writes audit records to a SQLite database.
type Logger struct {
	// db is the underlying SQLite connection pool.
	db *sql.DB
	// Warnings holds any security advisory messages produced during Open.
	// Callers should print these to the user after a successful open.
	Warnings []string
}

// Open creates or opens the audit database at the given path. The parent
// directories are created automatically if they do not exist.
// Security advisories (permission warnings) are stored in Logger.Warnings.
func Open(dbPath string) (*Logger, error) {
	dir := filepath.Dir(dbPath)

	// Note whether the directory and DB file already exist before we create them
	// so we can apply secure defaults to new files without warning, and warn only
	// for pre-existing files that have too-open permissions.
	_, dirStatErr := os.Stat(dir)
	_, dbStatErr := os.Stat(dbPath)
	dirIsNew := os.IsNotExist(dirStatErr)
	dbIsNew := os.IsNotExist(dbStatErr)

	// Create parent directories. New dirs get 0700 (owner-only access).
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating audit directory %s: %w", dir, err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening audit database: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating audit database: %w", err)
	}

	l := &Logger{db: db}

	// For newly created DB files, set 0600 silently so secrets are protected.
	// For pre-existing files with wrong permissions, warn the operator.
	if dbIsNew {
		_ = os.Chmod(dbPath, 0o600)
	} else {
		if info, err := os.Stat(dbPath); err == nil {
			if info.Mode().Perm()&^os.FileMode(0o600) != 0 {
				l.Warnings = append(l.Warnings, fmt.Sprintf(
					"⚠ audit database %s has permissions %04o; run: chmod 600 %s",
					dbPath, info.Mode().Perm(), dbPath,
				))
			}
		}
	}

	// For pre-existing directories with too-open permissions, warn.
	if !dirIsNew {
		if info, err := os.Stat(dir); err == nil {
			if info.Mode().Perm()&0o077 != 0 {
				l.Warnings = append(l.Warnings, fmt.Sprintf(
					"⚠ audit directory %s has permissions %04o; run: chmod 700 %s",
					dir, info.Mode().Perm(), dir,
				))
			}
		}
	}

	return l, nil
}

// Close closes the underlying database connection.
func (l *Logger) Close() error {
	if l.db != nil {
		return l.db.Close()
	}
	return nil
}

// migrate creates the schema if it does not already exist.
func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS runs (
			id          TEXT PRIMARY KEY,
			runbook     TEXT NOT NULL,
			name        TEXT NOT NULL,
			version     TEXT NOT NULL DEFAULT '',
			environment TEXT NOT NULL DEFAULT '',
			started_at  TEXT NOT NULL,
			finished_at TEXT,
			status      TEXT NOT NULL DEFAULT 'running',
			user        TEXT NOT NULL DEFAULT '',
			hostname    TEXT NOT NULL DEFAULT '',
			variables   TEXT NOT NULL DEFAULT '{}'
		);

		CREATE TABLE IF NOT EXISTS step_logs (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id      TEXT NOT NULL REFERENCES runs(id),
			step_name   TEXT NOT NULL,
			block_type  TEXT NOT NULL,
			started_at  TEXT NOT NULL,
			finished_at TEXT NOT NULL,
			exit_code   INTEGER NOT NULL DEFAULT 0,
			status      TEXT NOT NULL,
			stdout      TEXT NOT NULL DEFAULT '',
			stderr      TEXT NOT NULL DEFAULT '',
			command     TEXT NOT NULL DEFAULT ''
		);

		CREATE INDEX IF NOT EXISTS idx_step_logs_run_id ON step_logs(run_id);
		CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at);
	`)
	return err
}

// StartRun inserts a new run record and returns its ID.
func (l *Logger) StartRun(r RunRecord) error {
	vars := RedactVariables(r.Variables)
	varsJSON, err := json.Marshal(vars)
	if err != nil {
		return fmt.Errorf("marshaling variables: %w", err)
	}

	_, err = l.db.Exec(`
		INSERT INTO runs (id, runbook, name, version, environment, started_at, status, user, hostname, variables)
		VALUES (?, ?, ?, ?, ?, ?, 'running', ?, ?, ?)`,
		r.ID, r.Runbook, r.Name, r.Version, r.Environment,
		r.StartedAt.UTC().Format(time.RFC3339Nano),
		r.User, r.Hostname, string(varsJSON),
	)
	if err != nil {
		return fmt.Errorf("inserting run record: %w", err)
	}
	return nil
}

// LogStep inserts a step log record. Stdout and Stderr are truncated to 1 MB
// and then redacted using s.Secrets before being persisted.
func (l *Logger) LogStep(s StepLog) error {
	_, err := l.db.Exec(`
		INSERT INTO step_logs (run_id, step_name, block_type, started_at, finished_at, exit_code, status, stdout, stderr, command)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.RunID, s.StepName, s.BlockType,
		s.StartedAt.UTC().Format(time.RFC3339Nano),
		s.FinishedAt.UTC().Format(time.RFC3339Nano),
		s.ExitCode, s.Status,
		Redact(truncateOutput(s.Stdout), s.Secrets),
		Redact(truncateOutput(s.Stderr), s.Secrets),
		Redact(s.Command, s.Secrets),
	)
	if err != nil {
		return fmt.Errorf("inserting step log: %w", err)
	}
	return nil
}

// EndRun updates the run's finished_at timestamp and final status.
func (l *Logger) EndRun(runID, status string, finishedAt time.Time) error {
	_, err := l.db.Exec(`
		UPDATE runs SET finished_at = ?, status = ? WHERE id = ?`,
		finishedAt.UTC().Format(time.RFC3339Nano), status, runID,
	)
	if err != nil {
		return fmt.Errorf("updating run record: %w", err)
	}
	return nil
}

// GetRun queries a specific run and all its step logs. The runID can be a
// prefix — if it does not match exactly, the query falls back to a LIKE
// prefix match. This allows callers to use truncated IDs from list output.
func (l *Logger) GetRun(runID string) (*RunRecord, []StepLog, error) {
	row := l.db.QueryRow(`
		SELECT id, runbook, name, version, environment, started_at, finished_at,
		       status, user, hostname, variables
		FROM runs WHERE id = ? OR id LIKE ? ORDER BY started_at DESC LIMIT 1`,
		runID, runID+"%")

	var r RunRecord
	var startedStr, finishedStr sql.NullString
	var varsJSON string

	err := row.Scan(&r.ID, &r.Runbook, &r.Name, &r.Version, &r.Environment,
		&startedStr, &finishedStr, &r.Status, &r.User, &r.Hostname, &varsJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("querying run %s: %w", runID, err)
	}

	if startedStr.Valid {
		r.StartedAt, _ = time.Parse(time.RFC3339Nano, startedStr.String)
	}
	if finishedStr.Valid {
		t, _ := time.Parse(time.RFC3339Nano, finishedStr.String)
		r.FinishedAt = &t
	}

	r.Variables = make(map[string]string)
	_ = json.Unmarshal([]byte(varsJSON), &r.Variables)

	// Use the resolved full ID for the step logs query, not the
	// potentially truncated prefix the caller passed in.
	steps, err := l.queryStepLogs(r.ID)
	if err != nil {
		return nil, nil, err
	}

	return &r, steps, nil
}

// ListRuns returns the most recent runs, ordered by started_at descending.
func (l *Logger) ListRuns(limit int) ([]RunRecord, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := l.db.Query(`
		SELECT id, runbook, name, version, environment, started_at, finished_at,
		       status, user, hostname, variables
		FROM runs ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}
	defer rows.Close()

	runs := make([]RunRecord, 0, limit)
	for rows.Next() {
		var r RunRecord
		var startedStr, finishedStr sql.NullString
		var varsJSON string

		if err := rows.Scan(&r.ID, &r.Runbook, &r.Name, &r.Version, &r.Environment,
			&startedStr, &finishedStr, &r.Status, &r.User, &r.Hostname, &varsJSON); err != nil {
			return nil, fmt.Errorf("scanning run row: %w", err)
		}

		if startedStr.Valid {
			r.StartedAt, _ = time.Parse(time.RFC3339Nano, startedStr.String)
		}
		if finishedStr.Valid {
			t, _ := time.Parse(time.RFC3339Nano, finishedStr.String)
			r.FinishedAt = &t
		}

		r.Variables = make(map[string]string)
		_ = json.Unmarshal([]byte(varsJSON), &r.Variables)

		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// Prune deletes run records and their associated step logs that are older
// than the given retention period.
func (l *Logger) Prune(retentionDays int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Format(time.RFC3339Nano)

	// Delete step logs for old runs first (FK).
	_, err := l.db.Exec(`
		DELETE FROM step_logs WHERE run_id IN (
			SELECT id FROM runs WHERE started_at < ?
		)`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning step logs: %w", err)
	}

	res, err := l.db.Exec(`DELETE FROM runs WHERE started_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning runs: %w", err)
	}

	return res.RowsAffected()
}

// queryStepLogs returns all step logs for a run, ordered by id.
func (l *Logger) queryStepLogs(runID string) ([]StepLog, error) {
	rows, err := l.db.Query(`
		SELECT id, run_id, step_name, block_type, started_at, finished_at,
		       exit_code, status, stdout, stderr, command
		FROM step_logs WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("querying step logs for run %s: %w", runID, err)
	}
	defer rows.Close()

	var steps []StepLog
	for rows.Next() {
		var s StepLog
		var startedStr, finishedStr string

		if err := rows.Scan(&s.ID, &s.RunID, &s.StepName, &s.BlockType,
			&startedStr, &finishedStr, &s.ExitCode, &s.Status,
			&s.Stdout, &s.Stderr, &s.Command); err != nil {
			return nil, fmt.Errorf("scanning step log row: %w", err)
		}

		s.StartedAt, _ = time.Parse(time.RFC3339Nano, startedStr)
		s.FinishedAt, _ = time.Parse(time.RFC3339Nano, finishedStr)
		steps = append(steps, s)
	}
	return steps, rows.Err()
}

// RedactVariables returns a copy of the variable map with sensitive values
// replaced by [REDACTED]. A variable is sensitive if its uppercased name
// contains any of: SECRET, PASSWORD, TOKEN, KEY, CREDENTIAL.
func RedactVariables(vars map[string]string) map[string]string {
	if vars == nil {
		return nil
	}
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		if isSensitive(k) {
			out[k] = redactedValue
		} else {
			out[k] = v
		}
	}
	return out
}

// Redact replaces every occurrence of each secret value in s with [REDACTED].
// Empty values are skipped to avoid replacing all empty-string matches.
func Redact(s string, secrets map[string]string) string {
	for _, v := range secrets {
		if v == "" {
			continue
		}
		s = strings.ReplaceAll(s, v, redactedValue)
	}
	return s
}

// RedactDisplay replaces every occurrence of each secret value in s with ****.
// Use this for interactive output where the user needs a visual hint that a
// value has been hidden. Empty values are skipped.
func RedactDisplay(s string, secrets map[string]string) string {
	for _, v := range secrets {
		if v == "" {
			continue
		}
		s = strings.ReplaceAll(s, v, "****")
	}
	return s
}

// RedactError returns a new error whose message has all secret values replaced
// by [REDACTED]. Returns nil when err is nil.
func RedactError(err error, secrets map[string]string) error {
	if err == nil {
		return nil
	}
	return errors.New(Redact(err.Error(), secrets))
}

// IsSensitive reports whether name contains any of the SecretPatterns
// (case-insensitive). Exported so callers can reuse the same check.
func IsSensitive(name string) bool {
	upper := strings.ToUpper(name)
	for _, pat := range SecretPatterns {
		if strings.Contains(upper, pat) {
			return true
		}
	}
	return false
}

// isSensitive is the unexported alias kept for internal use.
func isSensitive(name string) bool { return IsSensitive(name) }

// truncateOutput limits s to maxOutputBytes. If truncated, outputTruncatedMarker
// is appended so readers know the output was cut.
func truncateOutput(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return s[:maxOutputBytes] + outputTruncatedMarker
}
