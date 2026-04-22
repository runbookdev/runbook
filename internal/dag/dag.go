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

// Package dag builds and schedules a directed acyclic graph of runbook steps.
//
// Each step's `depends_on` attribute defines parent edges. Build validates
// the graph (cycle detection via DFS) and returns a Graph that a Scheduler
// drives: ready steps are popped in deterministic document order, and when
// a step finishes, any dependents that now have all their parents satisfied
// become ready. A step failure causes its transitive descendants to be
// marked skipped so they never dispatch.
package dag

import (
	"fmt"
	"sort"
	"strings"

	"github.com/runbookdev/runbook/internal/ast"
)

// nodeState tracks a node's progress through the Scheduler.
type nodeState int

const (
	// statePending means parents are still running or have not started.
	statePending nodeState = iota
	// stateReady means all parents completed and the node awaits dispatch.
	stateReady
	// stateDispatched means the node was handed out via PopReady.
	stateDispatched
	// stateCompleted means CompleteSuccess has been called.
	stateCompleted
	// stateSkipped means the node is transitively dependent on a failed step.
	stateSkipped
)

// DFS color constants used by detectCycle.
const (
	colorWhite = 0 // unvisited
	colorGray  = 1 // on the current DFS path
	colorBlack = 2 // fully explored
)

// Node is a schedulable step in the DAG.
type Node struct {
	// Name is the step name (unique across a runbook).
	Name string
	// Parents are the names of steps this node depends on. Empty for roots.
	Parents []string
	// Index is the node's position in the original document. Used as a
	// stable tiebreaker when multiple nodes are ready at the same time.
	Index int
}

// Graph is an immutable, validated DAG of step dependencies.
type Graph struct {
	// nodes lists every step in original document order.
	nodes []*Node
	// byName resolves a step name to its node.
	byName map[string]*Node
	// children maps a parent name to the nodes that depend on it.
	children map[string][]*Node
}

// Scheduler drives a Graph using Kahn's algorithm. It is NOT safe for
// concurrent use: callers must serialize access (typically from a single
// coordinator goroutine).
type Scheduler struct {
	// graph is the underlying dependency graph being scheduled.
	graph *Graph
	// state tracks each node's lifecycle phase.
	state map[string]nodeState
	// pending records the count of unresolved parents per node.
	pending map[string]int
	// ready is the queue of dispatchable nodes, kept sorted by Index.
	ready []*Node
}

// CycleError is returned by Build when the graph contains a cycle.
type CycleError struct {
	// Cycle is the list of step names forming the cycle, closed: the first
	// and last entries are equal (e.g. [A, B, A]).
	Cycle []string
}

// Error formats the cycle as a human-readable arrow chain.
func (e *CycleError) Error() string {
	return fmt.Sprintf("dependency cycle: %s", strings.Join(e.Cycle, " -> "))
}

// Build constructs a DAG from the given step list.
//
// Dependencies referencing steps not present in the list are silently dropped.
// This is deliberate: after env-based filtering a dependent may outlive its
// parent in the current environment and should run as a root. Callers that
// need strict reference checking should run the validator first.
//
// Returns a *CycleError if a cycle is detected.
func Build(steps []ast.StepNode) (*Graph, error) {
	g := &Graph{
		nodes:    make([]*Node, 0, len(steps)),
		byName:   make(map[string]*Node, len(steps)),
		children: make(map[string][]*Node),
	}
	for i, s := range steps {
		n := &Node{Name: s.Name, Index: i}
		g.nodes = append(g.nodes, n)
		g.byName[s.Name] = n
	}
	for i, s := range steps {
		for _, dep := range parseDeps(s.DependsOn) {
			if _, ok := g.byName[dep]; !ok {
				// Dangling dep — parent filtered out. Skip.
				continue
			}
			g.nodes[i].Parents = append(g.nodes[i].Parents, dep)
			g.children[dep] = append(g.children[dep], g.nodes[i])
		}
	}
	if cycle := detectCycle(g); cycle != nil {
		return nil, &CycleError{Cycle: cycle}
	}
	return g, nil
}

// Nodes returns the nodes in original document order. The returned slice
// must not be mutated.
func (g *Graph) Nodes() []*Node { return g.nodes }

// Node returns the node for the given step name, or nil if absent.
func (g *Graph) Node(name string) *Node { return g.byName[name] }

// parseDeps splits a depends_on value. Accepts a single name or a
// comma-separated list. Whitespace around names is trimmed. Empty parts
// are dropped.
func parseDeps(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// detectCycle walks the graph and returns a cycle path if found. The returned
// slice is closed: cycle[0] == cycle[len-1].
func detectCycle(g *Graph) []string {
	color := make(map[string]int, len(g.nodes))
	var stack []string

	var visit func(n *Node) []string
	visit = func(n *Node) []string {
		color[n.Name] = colorGray
		stack = append(stack, n.Name)

		for _, dep := range n.Parents {
			switch color[dep] {
			case colorGray:
				// Back-edge — slice the cycle off the stack.
				for i, name := range stack {
					if name == dep {
						out := append([]string(nil), stack[i:]...)
						return append(out, dep)
					}
				}
			case colorWhite:
				if cyc := visit(g.byName[dep]); cyc != nil {
					return cyc
				}
			}
		}

		stack = stack[:len(stack)-1]
		color[n.Name] = colorBlack
		return nil
	}

	for _, n := range g.nodes {
		if color[n.Name] == colorWhite {
			if cyc := visit(n); cyc != nil {
				return cyc
			}
		}
	}
	return nil
}

// NewScheduler creates a scheduler seeded with all root nodes (no parents).
func NewScheduler(g *Graph) *Scheduler {
	s := &Scheduler{
		graph:   g,
		state:   make(map[string]nodeState, len(g.nodes)),
		pending: make(map[string]int, len(g.nodes)),
	}
	for _, n := range g.nodes {
		s.pending[n.Name] = len(n.Parents)
		if len(n.Parents) == 0 {
			s.state[n.Name] = stateReady
			s.ready = append(s.ready, n)
		}
	}
	s.sortReady()
	return s
}

// sortReady keeps the ready queue in document order (by Node.Index) so that
// PopReady returns nodes deterministically.
func (s *Scheduler) sortReady() {
	sort.SliceStable(s.ready, func(i, j int) bool {
		return s.ready[i].Index < s.ready[j].Index
	})
}

// HasWork reports whether the scheduler has nodes that are either ready
// to dispatch or already dispatched (in-flight). When HasWork returns
// false and the caller has no in-flight work, execution is complete.
func (s *Scheduler) HasWork() bool {
	if len(s.ready) > 0 {
		return true
	}
	for _, st := range s.state {
		if st == stateDispatched {
			return true
		}
	}
	return false
}

// ReadyCount returns the number of nodes currently ready to dispatch.
func (s *Scheduler) ReadyCount() int { return len(s.ready) }

// PopReady removes and returns up to n ready nodes, in document order.
// Returns nil if no nodes are ready.
func (s *Scheduler) PopReady(n int) []*Node {
	if n <= 0 || len(s.ready) == 0 {
		return nil
	}
	if n > len(s.ready) {
		n = len(s.ready)
	}
	out := make([]*Node, n)
	copy(out, s.ready[:n])
	s.ready = s.ready[n:]
	for _, node := range out {
		s.state[node.Name] = stateDispatched
	}
	return out
}

// CompleteSuccess marks a step as successfully completed and promotes any
// dependents whose parents are now all satisfied to the ready queue.
func (s *Scheduler) CompleteSuccess(name string) {
	s.state[name] = stateCompleted
	for _, child := range s.graph.children[name] {
		// Skipped children stay skipped even if a parent later completes.
		if s.state[child.Name] == stateSkipped {
			continue
		}
		s.pending[child.Name]--
		if s.pending[child.Name] == 0 && s.state[child.Name] == statePending {
			s.state[child.Name] = stateReady
			s.ready = append(s.ready, child)
		}
	}
	s.sortReady()
}

// Skip marks the given step and all its transitive dependents as skipped.
// Already-dispatched dependents are not recalled (the caller cancels them
// via context), but their descendants will not be scheduled.
//
// Returns the names of descendants that were newly marked skipped. The
// order follows DFS traversal of the children map; callers that need a
// specific presentation should sort the result themselves.
func (s *Scheduler) Skip(name string) []string {
	s.state[name] = stateSkipped
	var out []string
	var walk func(parent string)
	walk = func(parent string) {
		for _, child := range s.graph.children[parent] {
			if s.state[child.Name] == stateSkipped ||
				s.state[child.Name] == stateCompleted {
				continue
			}
			s.state[child.Name] = stateSkipped
			out = append(out, child.Name)
			walk(child.Name)
		}
	}
	walk(name)
	// Remove any newly-skipped nodes from the ready queue.
	filtered := s.ready[:0]
	for _, n := range s.ready {
		if s.state[n.Name] != stateSkipped {
			filtered = append(filtered, n)
		}
	}
	s.ready = filtered
	return out
}

// IsSkipped reports whether a step has been marked skipped.
func (s *Scheduler) IsSkipped(name string) bool {
	return s.state[name] == stateSkipped
}

// Levels returns the nodes grouped into topological layers: layer 0 is all
// nodes with no parents, layer 1 is nodes whose parents are all in layer 0,
// etc. Used for dry-run plan display. Does not mutate scheduler state.
func (g *Graph) Levels() [][]*Node {
	pending := make(map[string]int, len(g.nodes))
	level := make(map[string]int, len(g.nodes))
	var queue []*Node
	for _, n := range g.nodes {
		pending[n.Name] = len(n.Parents)
		if len(n.Parents) == 0 {
			level[n.Name] = 0
			queue = append(queue, n)
		}
	}
	maxLevel := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, child := range g.children[n.Name] {
			pending[child.Name]--
			if pending[child.Name] == 0 {
				l := level[n.Name] + 1
				if l > maxLevel {
					maxLevel = l
				}
				level[child.Name] = l
				queue = append(queue, child)
			}
		}
	}
	out := make([][]*Node, maxLevel+1)
	for _, n := range g.nodes {
		l, ok := level[n.Name]
		if !ok {
			continue // should be unreachable — Build guarantees acyclic
		}
		out[l] = append(out[l], n)
	}
	for _, layer := range out {
		sort.SliceStable(layer, func(i, j int) bool {
			return layer[i].Index < layer[j].Index
		})
	}
	return out
}
