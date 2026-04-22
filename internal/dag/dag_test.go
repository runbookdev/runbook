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

package dag

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/runbookdev/runbook/internal/ast"
)

func step(name, depends string) ast.StepNode {
	return ast.StepNode{Name: name, DependsOn: depends}
}

// popAll drains the scheduler, calling CompleteSuccess on each popped node
// before the next pop. Returns the order in which nodes were dispatched.
func popAll(t *testing.T, s *Scheduler) []string {
	t.Helper()
	var order []string
	for s.HasWork() {
		ready := s.PopReady(10)
		if len(ready) == 0 {
			t.Fatalf("HasWork=true but PopReady returned none (deadlock)")
		}
		for _, n := range ready {
			order = append(order, n.Name)
			s.CompleteSuccess(n.Name)
		}
	}
	return order
}

func TestBuild_LinearChain(t *testing.T) {
	g, err := Build([]ast.StepNode{
		step("a", ""),
		step("b", "a"),
		step("c", "b"),
	})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if got := len(g.Nodes()); got != 3 {
		t.Fatalf("expected 3 nodes, got %d", got)
	}
	order := popAll(t, NewScheduler(g))
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("got %v, want %v", order, want)
	}
}

func TestBuild_IndependentSteps(t *testing.T) {
	// No dependencies — all three should be ready at once.
	g, err := Build([]ast.StepNode{step("a", ""), step("b", ""), step("c", "")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sc := NewScheduler(g)
	if sc.ReadyCount() != 3 {
		t.Errorf("expected 3 ready, got %d", sc.ReadyCount())
	}
	// Document order preserved in the ready queue.
	ready := sc.PopReady(3)
	names := []string{ready[0].Name, ready[1].Name, ready[2].Name}
	if !reflect.DeepEqual(names, []string{"a", "b", "c"}) {
		t.Errorf("expected doc order, got %v", names)
	}
}

func TestBuild_Diamond(t *testing.T) {
	//    a
	//   / \
	//  b   c
	//   \ /
	//    d
	g, err := Build([]ast.StepNode{
		step("a", ""),
		step("b", "a"),
		step("c", "a"),
		step("d", "b, c"), // comma-separated multi-parent
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sc := NewScheduler(g)
	// Only 'a' ready initially.
	ready := sc.PopReady(10)
	if len(ready) != 1 || ready[0].Name != "a" {
		t.Fatalf("expected only 'a' ready, got %v", namesOf(ready))
	}
	sc.CompleteSuccess("a")
	// Now b and c ready; d still blocked.
	ready = sc.PopReady(10)
	if names := namesOf(ready); !reflect.DeepEqual(names, []string{"b", "c"}) {
		t.Fatalf("expected b,c ready, got %v", names)
	}
	sc.CompleteSuccess("b")
	if sc.ReadyCount() != 0 {
		t.Errorf("d should still be blocked after only b completes")
	}
	sc.CompleteSuccess("c")
	ready = sc.PopReady(10)
	if len(ready) != 1 || ready[0].Name != "d" {
		t.Fatalf("expected d ready, got %v", namesOf(ready))
	}
	sc.CompleteSuccess("d")
	if sc.HasWork() {
		t.Error("HasWork should be false after all complete")
	}
}

func TestBuild_CycleDetected(t *testing.T) {
	_, err := Build([]ast.StepNode{
		step("a", "c"),
		step("b", "a"),
		step("c", "b"),
	})
	var cycleErr *CycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("expected CycleError, got %v", err)
	}
	if len(cycleErr.Cycle) < 3 {
		t.Fatalf("cycle should have >=3 entries, got %v", cycleErr.Cycle)
	}
	first, last := cycleErr.Cycle[0], cycleErr.Cycle[len(cycleErr.Cycle)-1]
	if first != last {
		t.Errorf("cycle should be closed (first==last), got %v", cycleErr.Cycle)
	}
}

func TestBuild_SelfCycle(t *testing.T) {
	_, err := Build([]ast.StepNode{step("a", "a")})
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CycleError for self-dependency, got %v", err)
	}
}

func TestBuild_DanglingDepSilentlyDropped(t *testing.T) {
	// Step 'b' depends on 'ghost' which doesn't exist (filtered out by env).
	// Build should succeed and treat 'b' as a root.
	g, err := Build([]ast.StepNode{
		step("a", ""),
		step("b", "ghost"),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sc := NewScheduler(g)
	ready := sc.PopReady(10)
	names := namesOf(ready)
	// Both a and b should be ready.
	if !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Errorf("expected a,b ready, got %v", names)
	}
}

func TestScheduler_Skip_CascadesToDescendants(t *testing.T) {
	//    a
	//   / \
	//  b   c
	//      |
	//      d
	g, _ := Build([]ast.StepNode{
		step("a", ""), step("b", "a"), step("c", "a"), step("d", "c"),
	})
	sc := NewScheduler(g)
	_ = sc.PopReady(1) // dispatch a
	sc.CompleteSuccess("a")
	// Skip c — d should cascade.
	skipped := sc.Skip("c")
	if !contains(skipped, "d") {
		t.Errorf("Skip(c) should cascade to d, got %v", skipped)
	}
	if !sc.IsSkipped("d") {
		t.Error("d should be marked skipped")
	}
	if sc.IsSkipped("b") {
		t.Error("b should NOT be skipped (sibling of c)")
	}
}

func TestScheduler_Skip_RemovesFromReadyQueue(t *testing.T) {
	// Fan-out: a -> {b, c}. Complete a, both ready. Skip b — queue should
	// only hold c afterwards.
	g, _ := Build([]ast.StepNode{
		step("a", ""), step("b", "a"), step("c", "a"),
	})
	sc := NewScheduler(g)
	_ = sc.PopReady(1)
	sc.CompleteSuccess("a")
	if sc.ReadyCount() != 2 {
		t.Fatalf("expected 2 ready, got %d", sc.ReadyCount())
	}
	sc.Skip("b")
	if sc.ReadyCount() != 1 {
		t.Errorf("after Skip(b), expected 1 ready (only c), got %d", sc.ReadyCount())
	}
	ready := sc.PopReady(10)
	if len(ready) != 1 || ready[0].Name != "c" {
		t.Errorf("expected only c ready, got %v", namesOf(ready))
	}
}

func TestGraph_Levels_Diamond(t *testing.T) {
	g, _ := Build([]ast.StepNode{
		step("a", ""), step("b", "a"), step("c", "a"), step("d", "b,c"),
	})
	levels := g.Levels()
	if len(levels) != 3 {
		t.Fatalf("expected 3 levels, got %d", len(levels))
	}
	if names := namesOf(levels[0]); !reflect.DeepEqual(names, []string{"a"}) {
		t.Errorf("level 0 = %v, want [a]", names)
	}
	if names := namesOf(levels[1]); !reflect.DeepEqual(names, []string{"b", "c"}) {
		t.Errorf("level 1 = %v, want [b c]", names)
	}
	if names := namesOf(levels[2]); !reflect.DeepEqual(names, []string{"d"}) {
		t.Errorf("level 2 = %v, want [d]", names)
	}
}

func TestParseDeps_MultipleCommaSeparated(t *testing.T) {
	got := parseDeps("a,  b,c , , d")
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDeps_EmptyReturnsNil(t *testing.T) {
	if got := parseDeps(""); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	if got := parseDeps("   "); got != nil {
		t.Errorf("expected nil for whitespace, got %v", got)
	}
}

func TestCycleError_Message(t *testing.T) {
	err := &CycleError{Cycle: []string{"a", "b", "c", "a"}}
	if !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Errorf("cycle error should include full path, got %q", err.Error())
	}
}

// --- helpers ---

func namesOf(ns []*Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Name
	}
	return out
}

func contains(ss []string, want string) bool {
	return slices.Contains(ss, want)
}
