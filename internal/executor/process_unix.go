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

//go:build !windows

package executor

import (
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func getProcGroupID(pid int) (int, error) {
	return syscall.Getpgid(pid)
}

// killProcessGroup sends SIGTERM to the process group (falling back to the
// process itself if group operations fail), waits up to grace for the process
// to exit (using kill-0 polling to avoid racing with cmd.Wait()), then
// unconditionally sends SIGKILL if the process is still alive.
func killProcessGroup(cmd *exec.Cmd, grace time.Duration, pgidVerified bool, w io.Writer, stepName string) {
	if cmd.Process == nil {
		return
	}

	pid := cmd.Process.Pid
	pgid, pgidErr := syscall.Getpgid(pid)
	groupKillOK := pgidVerified && pgidErr == nil

	// Send SIGTERM.
	if groupKillOK {
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
			fmt.Fprintf(w, "[runbook] warning: step %q: SIGTERM to process group -%d failed (%v); killing process %d directly\n",
				stepName, pgid, err, pid)
			groupKillOK = false
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
	} else {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}

	// Poll with kill-0 during the grace period. This detects process exit without
	// calling Wait (which is owned solely by the streaming goroutine).
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			// Process is gone — SIGTERM was sufficient; no SIGKILL needed.
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Grace period elapsed — send SIGKILL unconditionally.
	if groupKillOK {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			fmt.Fprintf(w, "[runbook] warning: step %q: SIGKILL to process group -%d failed (%v); killing process %d directly\n",
				stepName, pgid, err, pid)
			_ = cmd.Process.Kill()
		}
	} else {
		_ = cmd.Process.Kill()
	}
}
