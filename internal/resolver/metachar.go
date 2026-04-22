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

package resolver

import (
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"
)

// metaEntry pairs a shell metacharacter sequence with its display label.
type metaEntry struct {
	// seq is the literal bytes to search for in resolved values.
	seq string
	// display is the human-readable label shown in warnings.
	display string
}

// MetacharWarning describes a dangerous metacharacter found in a resolved
// variable value.
type MetacharWarning struct {
	// VarName is the template variable name, e.g. "version".
	VarName string
	// Value is the resolved value, e.g. "1.0.0; rm -rf /".
	Value string
	// Metachar is the detected metacharacter display label, e.g. ";".
	Metachar string
	// BlockType is one of the ast.BlockType* constants.
	BlockType string
	// BlockName is the block's name attribute, e.g. "deploy".
	BlockName string
	// FilePath is the source .runbook file path.
	FilePath string
	// Line is the 1-based source line of the block opening fence.
	Line int
}

// MetacharError is returned by Resolve when --strict mode is enabled and
// dangerous metacharacters are detected in resolved variable values, or when
// the user declines to continue in interactive mode.
type MetacharError struct {
	// Warnings is the list of detected metacharacter issues.
	Warnings []MetacharWarning
}

// dangerousMetachars lists shell sequences that may allow command injection.
// $( is placed before $ so subshell syntax is identified specifically.
var dangerousMetachars = []metaEntry{
	{seq: "$(", display: "$("},
	{seq: ";", display: ";"},
	{seq: "|", display: "|"},
	{seq: "&", display: "&"},
	{seq: "$", display: "$"},
	{seq: "`", display: "`"},
	{seq: "\n", display: `\n`},
	{seq: "\r", display: `\r`},
	{seq: ">>", display: ">>"},
}

// Error returns a one-line summary of the detected metacharacter problems.
func (e *MetacharError) Error() string {
	n := len(e.Warnings)
	if n == 1 {
		return "1 resolved variable value contains dangerous shell metacharacters"
	}
	return fmt.Sprintf("%d resolved variable values contain dangerous shell metacharacters", n)
}

// findFirstMetachar returns the display label of the first dangerous shell
// metacharacter found in value, or an empty string if the value is clean.
func findFirstMetachar(value string) string {
	for _, mc := range dangerousMetachars {
		if strings.Contains(value, mc.seq) {
			return mc.display
		}
	}
	return ""
}

// printMetacharWarning writes a single yellow warning line to w.
func printMetacharWarning(w io.Writer, warn MetacharWarning) {
	yellow := color.New(color.FgYellow, color.Bold)
	yellow.Fprintf(w,
		"⚠ WARNING: Variable \"{{%s}}\" resolves to a value containing shell metacharacter \"%s\"."+
			" This could allow command injection."+
			" Resolved value: %q"+
			" Used in %s %q at %s:%d\n",
		warn.VarName, warn.Metachar, warn.Value,
		warn.BlockType, warn.BlockName, warn.FilePath, warn.Line,
	)
}
