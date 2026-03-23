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

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/runbookdev/runbook/internal/cli"
	"github.com/runbookdev/runbook/internal/executor"
)

// Build-time variables injected via ldflags (see Makefile).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	warnIfRoot(os.Getuid(), os.Stderr)

	cli.Version = version
	cli.Commit = commit
	cli.Date = date

	if err := cli.New().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(executor.RunInternalError.ExitCode())
	}
}

// warnIfRoot prints an unsuppressible warning when the process is running as
// the root user (uid 0). It is a separate function so tests can call it with
// a synthetic uid without needing to run as root.
func warnIfRoot(uid int, w io.Writer) {
	if uid != 0 {
		return
	}
	fmt.Fprintf(w, "⚠️  WARNING: runbook is running as root.\n")
	fmt.Fprintf(w, "   Commands will execute with full system privileges.\n")
	fmt.Fprintf(w, "   Consider running as a non-root user instead.\n")
}
