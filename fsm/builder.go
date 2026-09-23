// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fsm

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// maxEdges bounds the edges of one Spec, so that a cell of the
// transition table can address its branches with 16-bit offsets.
const maxEdges = math.MaxUint16

// Enum is the constraint on states and events. A value indexes the
// transition table, so a Spec over values up to n holds tables of about
// n by n entries. String names a value in errors and diagrams.
type Enum interface {
	~uint8
	String() string
}

// Option configures one edge. [If] and [Do] make one.
type Option[D any] struct {
	guard  func(d *D) bool
	action func(d *D)
}

// If makes an edge conditional on guard. A guard reads d and must not
// change it, because [Spec.Next] evaluates guards without running
// actions.
func If[D any](guard func(d *D) bool) Option[D] {
	return Option[D]{guard: guard}
}

// Do runs action when the edge is taken, after the exit action of the
// old state and before the entry action of the new one.
func Do[D any](action func(d *D)) Option[D] {
	return Option[D]{action: action}
}

// edge is one declared transition.
type edge[S, E Enum, D any] struct {
	guard    func(*D) bool
	action   func(*D)
	from, to S
	on       E
}

// hook is one entry or exit action.
type hook[S Enum, D any] struct {
	action func(*D)
	state  S
}

// Builder declares a [Spec]. Its methods record the declaration and
// return the Builder, so calls chain; [Builder.Build] validates it.
//
// # Concurrency
//
// Not safe for concurrent use.
type Builder[S, E Enum, D any] struct {
	initial S

	edges    []edge[S, E, D]
	enter    []hook[S, D]
	exit     []hook[S, D]
	terminal []S

	// problems collects what the declaring calls found wrong, so that
	// Build reports them with the rest.
	problems []error
}

// NewBuilder starts a Spec whose machines begin in initial.
func NewBuilder[S, E Enum, D any](initial S) *Builder[S, E, D] {
	return &Builder[S, E, D]{initial: initial}
}

// Edge declares that event on moves a machine from state from to
// state to.
//
// [If] makes the edge conditional, and [Do] runs an action when the
// edge is taken. Edges out of one state for one event are tried in the
// order they are declared, and an unguarded edge must come last.
func (b *Builder[S, E, D]) Edge(from S, on E, to S, opts ...Option[D]) *Builder[S, E, D] {
	e := edge[S, E, D]{from: from, on: on, to: to}

	for _, o := range opts {
		b.apply(&e, o)
	}

	b.edges = append(b.edges, e)

	return b
}

// apply sets the guard or action in o on e. It records a problem when o
// has no guard and no action, or when e already has the one o sets.
func (b *Builder[S, E, D]) apply(e *edge[S, E, D], o Option[D]) {
	if o.guard == nil && o.action == nil {
		b.edgeProblem(e, "a nil option")

		return
	}

	if o.guard != nil {
		if e.guard != nil {
			b.edgeProblem(e, "two guards")
		}

		e.guard = o.guard
	}

	if o.action != nil {
		if e.action != nil {
			b.edgeProblem(e, "two actions")
		}

		e.action = o.action
	}
}

// edgeProblem records a problem with e.
func (b *Builder[S, E, D]) edgeProblem(e *edge[S, E, D], what string) {
	b.problems = append(b.problems, fmt.Errorf("%w: edge %v on %v to %v has %s", ErrSpec, e.from, e.on, e.to, what))
}

// Edges declares the same edge from every state in from, such as a
// cancel event that more than one state accepts.
func (b *Builder[S, E, D]) Edges(from []S, on E, to S, opts ...Option[D]) *Builder[S, E, D] {
	for _, f := range from {
		b.Edge(f, on, to, opts...)
	}

	return b
}

// OnEnter runs action whenever a machine enters s from another state.
func (b *Builder[S, E, D]) OnEnter(s S, action func(d *D)) *Builder[S, E, D] {
	b.enter = b.hook(b.enter, "entry", s, action)

	return b
}

// OnExit runs action whenever a machine leaves s for another state.
func (b *Builder[S, E, D]) OnExit(s S, action func(d *D)) *Builder[S, E, D] {
	b.exit = b.hook(b.exit, "exit", s, action)

	return b
}

// hook records one entry or exit action, or a problem when action is
// nil.
func (b *Builder[S, E, D]) hook(hooks []hook[S, D], kind string, s S, action func(*D)) []hook[S, D] {
	if action == nil {
		b.problems = append(b.problems, fmt.Errorf("%w: %v has a nil %s action", ErrSpec, s, kind))

		return hooks
	}

	return append(hooks, hook[S, D]{action: action, state: s})
}

// Terminal declares states that reject every event.
func (b *Builder[S, E, D]) Terminal(states ...S) *Builder[S, E, D] {
	b.terminal = append(b.terminal, states...)

	return b
}

// Build validates the declaration and returns the [Spec].
//
// Build returns [ErrSpec], joined with one error for every problem it
// finds:
//
//   - a state from which no edge leads, and that is not terminal;
//   - a terminal state with an outgoing edge;
//   - a state that is unreachable from the initial state, with guards
//     ignored;
//   - an edge declared after an unguarded edge for the same state and
//     event, which can never be taken;
//   - an edge with two guards, two actions or a nil option, a state
//     with two entry or two exit actions, and a nil entry or exit
//     action;
//   - more edges than 16-bit offsets can address.
//
// Build does not modify the Builder, so it can be called again.
func (b *Builder[S, E, D]) Build() (*Spec[S, E, D], error) {
	problems := slices.Clone(b.problems)

	if len(b.edges) > maxEdges {
		problems = append(problems, fmt.Errorf("%w: %d edges, above the limit of %d", ErrSpec, len(b.edges), maxEdges))

		return nil, errors.Join(problems...)
	}

	nS, nE := b.size()
	sp := &Spec[S, E, D]{
		cells:    make([]cell, nS*nE),
		enter:    make([]func(*D), nS),
		exit:     make([]func(*D), nS),
		allows:   make([]bool, nS*nS),
		known:    make([]bool, nS),
		terminal: make([]bool, nS),
		nS:       nS,
		nE:       nE,
		initial:  b.initial,
	}

	problems = append(problems, b.fillEdges(sp)...)
	problems = append(problems, fillHooks(sp.enter, sp.known, b.enter, "entry")...)
	problems = append(problems, fillHooks(sp.exit, sp.known, b.exit, "exit")...)

	sp.known[b.initial] = true
	for _, t := range b.terminal {
		sp.known[t], sp.terminal[t] = true, true
	}

	problems = append(problems, b.checkShape(sp)...)

	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}

	return sp, nil
}

// size returns the number of states and events the tables must index:
// one more than the largest value declared.
func (b *Builder[S, E, D]) size() (nS, nE int) {
	nS = int(b.initial) + 1

	for _, e := range b.edges {
		nS = max(nS, int(e.from)+1, int(e.to)+1)
		nE = max(nE, int(e.on)+1)
	}

	for _, t := range b.terminal {
		nS = max(nS, int(t)+1)
	}

	for _, h := range slices.Concat(b.enter, b.exit) {
		nS = max(nS, int(h.state)+1)
	}

	return nS, nE
}

// fillEdges lays the edges out as branches grouped by cell, in
// declaration order, and fills the table of allowed transitions.
func (b *Builder[S, E, D]) fillEdges(sp *Spec[S, E, D]) []error {
	var problems []error

	// Count the branches of every cell, then turn the counts into
	// offsets, so each cell's branches are contiguous.
	for _, e := range b.edges {
		sp.cells[int(e.from)*sp.nE+int(e.on)].n++
	}

	next := uint16(0)
	for i := range sp.cells {
		sp.cells[i].first = next
		next += sp.cells[i].n
	}

	sp.branches = make([]branch[S, D], len(b.edges))
	placed := make([]uint16, len(sp.cells))
	unguarded := make([]bool, len(sp.cells))

	for _, e := range b.edges {
		i := int(e.from)*sp.nE + int(e.on)

		if unguarded[i] {
			problems = append(problems, fmt.Errorf(
				"%w: edge %v on %v to %v follows an unguarded edge and can never be taken",
				ErrSpec, e.from, e.on, e.to,
			))
		}

		unguarded[i] = e.guard == nil
		sp.branches[sp.cells[i].first+placed[i]] = branch[S, D]{guard: e.guard, action: e.action, to: e.to}
		placed[i]++

		sp.allows[int(e.from)*sp.nS+int(e.to)] = true
		sp.known[e.from], sp.known[e.to] = true, true
		sp.lines = append(sp.lines, line[S, E]{from: e.from, to: e.to, on: e.on, guarded: e.guard != nil})
	}

	return problems
}

// fillHooks places one action per state, reporting a second one.
func fillHooks[S Enum, D any](table []func(*D), known []bool, hooks []hook[S, D], kind string) []error {
	var problems []error

	for _, h := range hooks {
		if table[h.state] != nil {
			problems = append(problems, fmt.Errorf("%w: %v has two %s actions", ErrSpec, h.state, kind))
		}

		table[h.state] = h.action
		known[h.state] = true
	}

	return problems
}

// checkShape reports states that cannot be left or reached.
func (b *Builder[S, E, D]) checkShape(sp *Spec[S, E, D]) []error {
	var problems []error

	outgoing := make([]bool, sp.nS)
	for _, e := range b.edges {
		outgoing[e.from] = true
	}

	for s := range sp.nS {
		switch {
		case !sp.known[s]:
		case sp.terminal[s] && outgoing[s]:
			problems = append(problems, fmt.Errorf("%w: terminal state %v has an outgoing edge", ErrSpec, S(s)))
		case !sp.terminal[s] && !outgoing[s]:
			problems = append(
				problems,
				fmt.Errorf("%w: state %v has no outgoing edge and is not terminal", ErrSpec, S(s)),
			)
		}
	}

	reached := make([]bool, sp.nS)
	reached[b.initial] = true
	stack := []int{int(b.initial)}

	for len(stack) > 0 {
		from := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		for to := range sp.nS {
			if sp.allows[from*sp.nS+to] && !reached[to] {
				reached[to] = true
				stack = append(stack, to)
			}
		}
	}

	for s := range sp.nS {
		if sp.known[s] && !reached[s] {
			problems = append(problems, fmt.Errorf("%w: state %v is unreachable from %v", ErrSpec, S(s), b.initial))
		}
	}

	return problems
}
