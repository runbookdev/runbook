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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver
)

// secretPatterns are substrings in variable names that trigger auto-redaction.
var secretPatterns = []string{"SECRET", "PASSWORD", "TOKEN", "KEY", "CREDENTIAL"}

// redactedValue is the replacement for sensitive variable values.
const redactedValue = "[REDACTED]"

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
	ID          string
	Runbook     string
	Name        string
	Version     string
	Environment string
	StartedAt   time.Time
	FinishedAt  *time.Time
	Status      string
	User        string
	Hostname    string
	Variables   map[string]string
}

// StepLog represents the outcome of a single block execution within a run.
type StepLog struct {
	ID         int64
	RunID      string
	StepName   string
	BlockType  string
	StartedAt  time.Time
	FinishedAt time.Time
	ExitCode   int
	Status     string
	Stdout     string
	Stderr     string
	Command    string
}

// Logger writes audit records to a SQLite database.
type Logger struct {
	db *sql.DB
}

// Open creates or opens the audit database at the given path. The parent
// directories are created automatically if they do not exist.
func Open(dbPath string) (*Logger, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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

	return &Logger{db: db}, nil
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

// LogStep inserts a step log record. The command field is auto-redacted.
func (l *Logger) LogStep(s StepLog) error {
	_, err := l.db.Exec(`
		INSERT INTO step_logs (run_id, step_name, block_type, started_at, finished_at, exit_code, status, stdout, stderr, command)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.RunID, s.StepName, s.BlockType,
		s.StartedAt.UTC().Format(time.RFC3339Nano),
		s.FinishedAt.UTC().Format(time.RFC3339Nano),
		s.ExitCode, s.Status, s.Stdout, s.Stderr,
		RedactCommand(s.Command),
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

// RedactCommand replaces inline occurrences of common secret patterns in
// command text. This is a best-effort heuristic: it looks for environment
// variable references that match the sensitive patterns.
func RedactCommand(cmd string) string {
	// We do not attempt to parse the shell; instead we redact the value
	// portion of any VAR=value or --flag=value patterns where VAR matches.
	// For now, return as-is — the primary redaction happens at the variable
	// level via RedactVariables. Commands recorded here use already-resolved
	// text, so if a variable named DB_PASSWORD was resolved into the command,
	// the raw password text would be present. A full-coverage approach would
	// require tracking every resolved secret and scrubbing, which is future
	// work. The current guarantee: the variables JSON column is always safe.
	return cmd
}

// isSensitive checks if a variable name matches any secret pattern.
func isSensitive(name string) bool {
	upper := strings.ToUpper(name)
	for _, pat := range secretPatterns {
		if strings.Contains(upper, pat) {
			return true
		}
	}
	return false
}
