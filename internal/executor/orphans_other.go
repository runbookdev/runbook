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

//go:build !linux && !windows

package executor

import (
	"os/exec"
	"strconv"
	"strings"
)

// findOrphans returns PIDs of processes whose PPID equals parentPID,
// using `ps -o pid,ppid -ax` on non-Linux systems (macOS, BSDs).
func findOrphans(parentPID int) []int {
	out, err := exec.Command("ps", "-o", "pid,ppid", "-ax").Output()
	if err != nil {
		return nil
	}
	target := strconv.Itoa(parentPID)
	var result []int
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == parentPID {
			continue
		}
		if fields[1] == target {
			result = append(result, pid)
		}
	}
	return result
}
