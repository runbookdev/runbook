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

package bulk

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// runbookExt is the canonical file extension for runbook files. Label
// formatting strips it so summaries read `deploy[env=prod]` rather
// than `deploy.runbook[env=prod]` — the extension is constant across
// every bulk run and adds no information to the display.
const runbookExt = ".runbook"

// Binding is a single matrix cell — a concrete assignment of values to
// axis keys. Every expanded run receives one Binding, merged over the
// caller's base Template.Vars at dispatch time.
type Binding map[string]string

// Matrix is a parameter sweep definition: one or more named axes whose
// Cartesian product is the default set of runs, optionally augmented
// by Include rows and narrowed by Exclude rows. The shape mirrors the
// GitHub Actions matrix schema to minimize surprise for operators who
// already use that pattern in CI.
type Matrix struct {
	// Axes maps an axis name (a variable key) to its list of values.
	// Declaration order is stable because the parser preserves the
	// order YAML nodes appear in the source file.
	Axes []Axis
	// Include adds extra rows on top of the Cartesian product. Each
	// entry is a complete (or partial) binding; partial entries are
	// emitted verbatim (the caller decides how to handle missing keys).
	Include []Binding
	// Exclude removes rows from the Cartesian product whose assignments
	// are a superset of the exclude entry. Excluded rows never appear
	// in the final list and are not reported as "skipped".
	Exclude []Binding
}

// Axis is a single named matrix dimension. Keeping axes ordered (as
// opposed to a map) lets Expand produce deterministic binding order,
// which matters for report readability and test stability.
type Axis struct {
	// Key is the variable name (e.g. "env", "region").
	Key string
	// Values is the ordered list of values for this axis.
	Values []string
}

// ParseMatrixVar parses a single `--matrix-var` flag value in the form
// `key=v1,v2,v3` into an Axis. Whitespace around the key and values is
// trimmed. An empty value list is an error — an axis with no values
// would zero the Cartesian product and silently drop every run.
func ParseMatrixVar(s string) (Axis, error) {
	key, rest, ok := strings.Cut(s, "=")
	if !ok {
		return Axis{}, fmt.Errorf("matrix-var %q: missing '=' (want key=v1,v2)", s)
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return Axis{}, fmt.Errorf("matrix-var %q: empty key", s)
	}

	raw := strings.Split(rest, ",")
	values := make([]string, 0, len(raw))
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		values = append(values, v)
	}

	if len(values) == 0 {
		return Axis{}, fmt.Errorf("matrix-var %q: no values after '='", s)
	}
	return Axis{Key: key, Values: values}, nil
}

// matrixFile mirrors the YAML schema. Using a private intermediate
// lets us accept either map[string][]string for axes (GitHub style)
// or a slice of {key, values} pairs, and normalise both into the
// ordered Axis slice that Matrix.Expand expects.
type matrixFile struct {
	// Axes uses a yaml.Node so we can walk children in declaration
	// order rather than relying on Go map iteration.
	Axes yaml.Node `yaml:"axes"`
	// Include/Exclude are lists of string→string maps.
	Include []map[string]string `yaml:"include"`
	Exclude []map[string]string `yaml:"exclude"`
}

// ParseMatrixFile reads a YAML matrix definition from disk. The file
// shape is:
//
//	axes:
//	  env: [staging, prod]
//	  region: [us, eu]
//	include:
//	  - { env: prod, region: ap }
//	exclude:
//	  - { env: staging, region: eu }
//
// Axes is required; include and exclude are optional.
func ParseMatrixFile(path string) (Matrix, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Matrix{}, fmt.Errorf("reading matrix file %s: %w", path, err)
	}

	var mf matrixFile
	if err := yaml.Unmarshal(data, &mf); err != nil {
		return Matrix{}, fmt.Errorf("parsing matrix file %s: %w", path, err)
	}

	axes, err := decodeAxes(&mf.Axes, path)
	if err != nil {
		return Matrix{}, err
	}

	return Matrix{
		Axes:    axes,
		Include: bindingsFromMaps(mf.Include),
		Exclude: bindingsFromMaps(mf.Exclude),
	}, nil
}

// decodeAxes walks the YAML mapping node for the `axes:` key and
// produces an ordered []Axis. Using a yaml.Node (rather than a Go map)
// preserves the declaration order — which becomes the nesting order
// of the Cartesian expansion and therefore the order rows appear in
// the final report.
func decodeAxes(node *yaml.Node, path string) ([]Axis, error) {
	if node == nil || node.Kind == 0 {
		return nil, fmt.Errorf("matrix file %s: missing 'axes'", path)
	}

	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("matrix file %s: 'axes' must be a mapping of name → [values]", path)
	}

	// yaml.Node mapping: Content is [key0, value0, key1, value1, ...].
	axes := make([]Axis, 0, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		var values []string
		if err := valNode.Decode(&values); err != nil {
			return nil, fmt.Errorf("matrix file %s: axis %q: %w", path, keyNode.Value, err)
		}

		if len(values) == 0 {
			return nil, fmt.Errorf("matrix file %s: axis %q: empty value list", path, keyNode.Value)
		}
		axes = append(axes, Axis{Key: keyNode.Value, Values: values})
	}

	if len(axes) == 0 {
		return nil, fmt.Errorf("matrix file %s: 'axes' must declare at least one axis", path)
	}
	return axes, nil
}

// bindingsFromMaps converts the decoded []map[string]string into the
// internal []Binding type. Keeps Matrix consumers from importing the
// generic map alias just to spell the slice type.
func bindingsFromMaps(in []map[string]string) []Binding {
	if len(in) == 0 {
		return nil
	}
	out := make([]Binding, len(in))
	for i, m := range in {
		out[i] = Binding(m)
	}
	return out
}

// MaxMatrixRows caps the total number of bindings Expand will emit.
// Matches MaxRunbooksUpperBound so the worst-case job count remains
// bounded by the same policy ceiling as the outer concurrency dial.
// Exceeding this indicates a malformed matrix (e.g. too many axes or
// too many values per axis) that would otherwise queue millions of
// jobs into memory before the coordinator's clamp took effect.
const MaxMatrixRows = MaxRunbooksUpperBound

// Expand produces the ordered list of bindings this matrix represents.
// The algorithm is:
//  1. Reject duplicate axis keys — a key declared twice would produce
//     a row map with only the last axis's value, silently collapsing
//     the sweep in ways the operator almost certainly did not intend.
//  2. Cartesian-product the axes in declaration order (first axis
//     varies slowest, last axis varies fastest — matching how most
//     operators read a matrix table).
//  3. Drop any product row that is a superset of any Exclude entry.
//  4. Append every Include entry verbatim.
//
// An empty matrix (no axes and no includes) returns an error — a bulk
// invocation with zero effective rows is almost certainly a mistake
// and silently running nothing is worse than failing loudly. A matrix
// whose expansion exceeds MaxMatrixRows also errors rather than
// queueing an unbounded number of jobs.
func (m Matrix) Expand() ([]Binding, error) {
	if err := checkDuplicateAxes(m.Axes); err != nil {
		return nil, err
	}

	if err := checkExpansionBounds(m.Axes, len(m.Include)); err != nil {
		return nil, err
	}

	var rows []Binding
	if len(m.Axes) > 0 {
		rows = cartesian(m.Axes)
	}

	if len(m.Exclude) > 0 {
		rows = filterExcluded(rows, m.Exclude)
	}

	for _, inc := range m.Include {
		// Each include becomes its own row. We defensively copy so a
		// caller can't mutate the matrix through the returned slice.
		rows = append(rows, maps.Clone(inc))
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("matrix: expansion produced zero rows (check axes and exclude rules)")
	}
	return rows, nil
}

// checkDuplicateAxes returns an error when any axis key appears more
// than once. The layering rules in buildMatrixBindings (file axes +
// inline --matrix-var) make it easy for operators to name the same
// key twice by accident; rejecting it at Expand time gives a clear
// diagnostic instead of a silently collapsed Cartesian product.
func checkDuplicateAxes(axes []Axis) error {
	if len(axes) < 2 {
		return nil
	}

	seen := make(map[string]struct{}, len(axes))
	for _, a := range axes {
		if _, dup := seen[a.Key]; dup {
			return fmt.Errorf("matrix: axis %q declared more than once", a.Key)
		}
		seen[a.Key] = struct{}{}
	}
	return nil
}

// checkExpansionBounds enforces MaxMatrixRows before cartesian()
// allocates. The product is computed in int with overflow-guarded
// multiplication so ten 10-value axes can't silently wrap to a small
// positive number.
func checkExpansionBounds(axes []Axis, includes int) error {
	product := 1
	for _, a := range axes {
		if len(a.Values) == 0 {
			// ParseMatrixFile rejects empty axes, so this is only
			// reachable via direct struct construction; treat as
			// zero-row matrix and let the caller hit the final
			// "zero rows" error for a better message.
			return nil
		}
		// Guard against int overflow on absurd inputs.
		if product > MaxMatrixRows/len(a.Values)+1 {
			return fmt.Errorf(
				"matrix: expansion would exceed %d rows (cap: MaxMatrixRows)",
				MaxMatrixRows)
		}
		product *= len(a.Values)
	}

	if product+includes > MaxMatrixRows {
		return fmt.Errorf(
			"matrix: expansion would produce %d rows, exceeds cap of %d",
			product+includes, MaxMatrixRows)
	}
	return nil
}

// cartesian returns every combination of axis values in declaration
// order. Runtime is O(product of axis lengths) — acceptable given the
// overall MaxRunbooksUpperBound cap (256) will clamp output anyway.
func cartesian(axes []Axis) []Binding {
	total := 1
	for _, a := range axes {
		total *= len(a.Values)
	}

	out := make([]Binding, 0, total)
	for i := range total {
		row := make(Binding, len(axes))
		idx := i
		// Walk axes in reverse so the LAST axis varies fastest,
		// matching the usual "rightmost index changes first" mental
		// model used for matrix tables.
		for j := len(axes) - 1; j >= 0; j-- {
			ax := axes[j]
			row[ax.Key] = ax.Values[idx%len(ax.Values)]
			idx /= len(ax.Values)
		}
		out = append(out, row)
	}
	return out
}

// filterExcluded drops any row in rows whose assignments contain every
// key/value pair of any exclude entry. An empty exclude entry is a
// catch-all and would drop every row; we treat it as a no-op so users
// can leave stub entries in a matrix file without wiping the sweep.
func filterExcluded(rows []Binding, excludes []Binding) []Binding {
	out := rows[:0]
	for _, row := range rows {
		if matchesAnyExclude(row, excludes) {
			continue
		}
		out = append(out, row)
	}
	return out
}

// matchesAnyExclude reports whether row is a superset of any non-empty
// exclude entry.
func matchesAnyExclude(row Binding, excludes []Binding) bool {
	for _, ex := range excludes {
		if len(ex) == 0 {
			continue
		}
		if isSuperset(row, ex) {
			return true
		}
	}
	return false
}

// isSuperset reports whether a contains every key/value pair in b.
func isSuperset(a, b Binding) bool {
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

// FormatLabel renders a human-friendly tag for a binding, suitable for
// use as Job.Label or in a summary table. The format is
// `name[k=v,k=v]` with keys sorted so logs of the same binding across
// runs stay comparable. When binding is empty, the plain name is
// returned unchanged.
func FormatLabel(name string, binding Binding) string {
	if len(binding) == 0 {
		return name
	}

	keys := make([]string, 0, len(binding))
	for k := range binding {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+binding[k])
	}
	return name + "[" + strings.Join(parts, ",") + "]"
}

// BuildJobs produces the []Job for a cross-product of file paths and
// bindings. When bindings is nil or empty, one job per file is emitted
// (the phase-1 no-matrix case). When files is empty, no jobs are
// emitted — the caller is expected to validate input upstream.
//
// Label for each job is FormatLabel(filepath.Base(file), binding) so
// prefixed output stays attributable and matrix rows of the same file
// are distinguishable in the final report.
func BuildJobs(files []string, bindings []Binding) []Job {
	if len(files) == 0 {
		return nil
	}

	if len(bindings) == 0 {
		out := make([]Job, len(files))
		for i, f := range files {
			out[i] = Job{FilePath: f, Label: baseLabel(f, nil)}
		}
		return out
	}

	out := make([]Job, 0, len(files)*len(bindings))
	for _, f := range files {
		for _, b := range bindings {
			out = append(out, Job{
				FilePath: f,
				Vars:     maps.Clone(b),
				Label:    baseLabel(f, b),
			})
		}
	}
	return out
}

// baseLabel is a small wrapper so tests and BuildJobs agree on the
// exact label format without reimporting filepath at each call site.
func baseLabel(path string, binding Binding) string {
	return FormatLabel(stripRunbookExt(path), binding)
}

// stripRunbookExt returns the file's base name with a trailing
// `.runbook` extension removed. Files without that extension are
// returned unchanged (so non-standard names survive intact in labels).
func stripRunbookExt(path string) string {
	base := filepath.Base(path)
	if trimmed, ok := strings.CutSuffix(base, runbookExt); ok {
		return trimmed
	}
	return base
}
