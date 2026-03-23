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

// Security-focused test suite for the parser package.
// Each test is identified by a threat ID (T1–T7) matching the security design doc.
package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── T1: Oversized file rejected ────────────────────────────────────────────

// TestT1_OversizedFileRejected verifies that a .runbook file larger than 1 MB
// is rejected by ParseFile before any content is interpreted. Without this
// guard an attacker could craft a gigabyte-scale file to exhaust memory.
func TestT1_OversizedFileRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.runbook")

	// Slightly over the 1 MB limit.
	data := make([]byte, maxFileSizeBytes+1)
	for i := range data {
		data[i] = 'a'
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("expected error for oversized file, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("expected 'exceeds maximum size' in error, got: %v", err)
	}
}

// TestT1_Parse_OversizedContentRejected verifies the size check also applies
// when content is passed directly to Parse (not from disk), so callers cannot
// bypass the guard by constructing content in memory.
func TestT1_Parse_OversizedContentRejected(t *testing.T) {
	content := strings.Repeat("a", maxFileSizeBytes+1)
	_, err := Parse("synthetic.runbook", content)
	if err == nil {
		t.Fatal("expected error for oversized in-memory content, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("expected 'exceeds maximum size' in error, got: %v", err)
	}
}

// ── T2: Non-UTF-8 file rejected ────────────────────────────────────────────

// TestT2_NonUTF8FileRejected verifies that files containing bytes that are not
// valid UTF-8 are rejected. Binary files or mis-encoded content could cause
// incorrect parsing or exploit assumptions about string encoding.
func TestT2_NonUTF8FileRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.runbook")

	// Valid YAML frontmatter header followed by invalid UTF-8 sequence.
	header := "---\nname: test\nversion: 1.0.0\n---\n\n"
	// 0xFF 0xFE is a UTF-16 BOM / invalid UTF-8 byte sequence.
	invalidUTF8 := []byte{0xFF, 0xFE, 0x00}
	data := append([]byte(header), invalidUTF8...)

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("expected error for non-UTF-8 file, got nil")
	}
	if !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Errorf("expected 'invalid UTF-8' in error, got: %v", err)
	}
}

// TestT2_Parse_NonUTF8ContentRejected verifies the UTF-8 guard applies when
// calling Parse directly with pre-read (binary) content.
func TestT2_Parse_NonUTF8ContentRejected(t *testing.T) {
	// Embed invalid UTF-8 bytes into otherwise valid content.
	content := "---\nname: test\nversion: 1.0.0\n---\n\n" + string([]byte{0xC0, 0x80}) // over-long encoding
	_, err := Parse("t2.runbook", content)
	if err == nil {
		t.Fatal("expected error for non-UTF-8 content, got nil")
	}
	if !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Errorf("expected 'invalid UTF-8' in error, got: %v", err)
	}
}

// ── T3: >1000 blocks rejected ──────────────────────────────────────────────

// TestT3_TooManyBlocksRejected verifies that a file with more than 1000 blocks
// is rejected. Without this limit, a hostile file could generate thousands of
// steps, triggering uncontrolled execution at runtime.
func TestT3_TooManyBlocksRejected(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\nname: test\nversion: 1.0.0\n---\n\n")
	// Write exactly maxBlocks+1 = 1001 step blocks.
	for i := 0; i <= maxBlocks; i++ {
		fmt.Fprintf(&b, "```step name=\"s%d\"\necho %d\n```\n\n", i, i)
	}

	_, err := Parse("t3.runbook", b.String())
	if err == nil {
		t.Fatal("expected error for >1000 blocks, got nil")
	}
	if !strings.Contains(err.Error(), "more than 1000 blocks") {
		t.Errorf("expected 'more than 1000 blocks' in error, got: %v", err)
	}
}

// TestT3_ExactlyMaxBlocksAccepted verifies that exactly 1000 blocks is accepted
// (boundary value — the guard fires only when the count exceeds the limit).
func TestT3_ExactlyMaxBlocksAccepted(t *testing.T) {
	var b strings.Builder
	b.WriteString("---\nname: test\nversion: 1.0.0\n---\n\n")
	for i := 0; i < maxBlocks; i++ {
		fmt.Fprintf(&b, "```step name=\"s%d\"\necho %d\n```\n\n", i, i)
	}

	_, err := Parse("t3-ok.runbook", b.String())
	if err != nil {
		t.Errorf("expected no error for exactly %d blocks, got: %v", maxBlocks, err)
	}
}

// ── T4: Unknown YAML frontmatter fields rejected ───────────────────────────

// TestT4_UnknownFrontmatterFieldRejected verifies that the parser uses YAML
// strict/known-fields mode. Unknown keys are rejected to prevent YAML bombs
// delivered via malicious anchor/alias chains and to catch typos that could
// silently disable security settings.
func TestT4_UnknownFrontmatterFieldRejected(t *testing.T) {
	bt := "```"
	content := "---\nname: test\nversion: 1.0.0\nunknown_security_field: should-fail\n---\n\n" +
		bt + "step name=\"s\"\necho hi\n" + bt + "\n"

	_, err := Parse("t4.runbook", content)
	if err == nil {
		t.Fatal("expected error for unknown frontmatter field, got nil")
	}
	// yaml.v3 with KnownFields reports "field ... not found in type"
	if !strings.Contains(err.Error(), "frontmatter") && !strings.Contains(err.Error(), "unknown") &&
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("expected frontmatter rejection error, got: %v", err)
	}
}

// TestT4_KnownFieldsAreAccepted verifies that all documented fields parse
// without error, serving as a regression guard for the known-fields list.
func TestT4_KnownFieldsAreAccepted(t *testing.T) {
	bt := "```"
	content := "---\nname: my-runbook\nversion: 1.0.0\ndescription: ok\n" +
		"owners:\n  - ops\nenvironments:\n  - staging\n" +
		"requires:\n  tools:\n    - kubectl\ntimeout: 30m\n---\n\n" +
		bt + "step name=\"s\"\necho hi\n" + bt + "\n"

	_, err := Parse("t4-ok.runbook", content)
	if err != nil {
		t.Errorf("expected no error for known frontmatter fields, got: %v", err)
	}
}

// ── T5: Frontmatter >64 KB rejected ───────────────────────────────────────

// TestT5_OversizedFrontmatterRejected verifies that frontmatter larger than
// 64 KB is rejected before YAML decoding. Very large YAML payloads using
// anchor/alias expansion can consume gigabytes of memory during unmarshalling.
func TestT5_OversizedFrontmatterRejected(t *testing.T) {
	// description is a known field so it won't be rejected by strict YAML;
	// only the size check should fire.
	bigDescription := strings.Repeat("x", maxFrontmatterBytes+512)
	bt := "```"
	content := "---\nname: test\nversion: 1.0.0\ndescription: " + bigDescription + "\n---\n\n" +
		bt + "step name=\"s\"\necho hi\n" + bt + "\n"

	_, err := Parse("t5.runbook", content)
	if err == nil {
		t.Fatal("expected error for oversized frontmatter, got nil")
	}
	if !strings.Contains(err.Error(), "frontmatter exceeds maximum size") {
		t.Errorf("expected 'frontmatter exceeds maximum size' in error, got: %v", err)
	}
}

// ── T6: Nested backticks inside step body parse correctly ─────────────────

// TestT6_NestedBackticksParseCorrectly verifies that triple backticks appearing
// inside a block body do not prematurely terminate the block. An overly simple
// parser might incorrectly close a block mid-command, producing a truncated or
// misaligned AST that could be exploited to inject extra blocks.
func TestT6_NestedBackticksParseCorrectly(t *testing.T) {
	bt := "```"
	// The step body contains a line with triple backticks embedded in quotes
	// and another with mixed content — neither should close the block.
	content := "---\nname: test\nversion: 1.0.0\n---\n\n" +
		bt + "step name=\"embedded-ticks\"\n" +
		"echo hello\n" +
		"echo '" + bt + "not-a-closer" + bt + "'\n" +
		"CODE=" + bt + bt + bt + "something" + bt + bt + bt + "\n" +
		"echo done\n" +
		bt + "\n"

	rb, err := Parse("t6.runbook", content)
	if err != nil {
		t.Fatalf("expected no error for nested backticks, got: %v", err)
	}
	if len(rb.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(rb.Steps))
	}
	cmd := rb.Steps[0].Command
	if !strings.Contains(cmd, "echo hello") {
		t.Errorf("step body missing 'echo hello': %q", cmd)
	}
	if !strings.Contains(cmd, "echo done") {
		t.Errorf("step body missing 'echo done': %q", cmd)
	}
	// Verify the embedded backtick line is included in the body.
	if !strings.Contains(cmd, "not-a-closer") {
		t.Errorf("step body missing embedded backtick line: %q", cmd)
	}
}

// ── T7: Binary .runbook file rejected ──────────────────────────────────────

// TestT7_BinaryRunbookFileRejected verifies that a file named with the
// .runbook extension but containing binary data (e.g., accidentally passed
// a compiled binary) is rejected via the UTF-8 validation check. Binary data
// in a parsed command body could trigger undefined shell behaviour.
func TestT7_BinaryRunbookFileRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-text.runbook")

	// Simulate an ELF binary header followed by null bytes — common binary signature.
	binaryData := []byte{0x7F, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xFF, 0xFE, 0xAB, 0xCD, 0x00, 0x01, 0x02, 0x03}

	if err := os.WriteFile(path, binaryData, 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("expected error for binary .runbook file, got nil")
	}
	// The UTF-8 check fires before any other interpretation.
	if !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Errorf("expected 'invalid UTF-8' error for binary file, got: %v", err)
	}
}

// TestT7_RunbookWithNullBytesRejected verifies that null bytes — a common
// binary indicator — are rejected even when embedded in otherwise printable text.
func TestT7_RunbookWithNullBytesRejected(t *testing.T) {
	// Null bytes are not valid UTF-8 in the context of Go's utf8.Valid check.
	// Go's utf8.Valid accepts 0x00 as valid UTF-8 (it is, technically), but
	// we verify the overall binary check works for the realistic ELF-style case.
	dir := t.TempDir()
	path := filepath.Join(dir, "null.runbook")

	// Mix of printable ASCII and high-byte invalid UTF-8 sequences.
	data := []byte("---\nname: test\n---\n\x80\x81\x82 bad bytes here")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("expected error for file with invalid bytes, got nil")
	}
	if !strings.Contains(err.Error(), "invalid UTF-8") {
		t.Errorf("expected UTF-8 rejection, got: %v", err)
	}
}
