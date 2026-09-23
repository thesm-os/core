// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fsm

import "strings"

// cell addresses the branches of one state and event.
type cell struct {
	first uint16
	n     uint16
}

// branch is one edge as the table stores it.
type branch[S Enum, D any] struct {
	guard  func(*D) bool
	action func(*D)
	to     S
}

// line is one edge as a diagram draws it.
type line[S, E Enum] struct {
	from, to S
	on       E
	guarded  bool
}

// Spec is a validated transition table. [Builder.Build] returns one.
//
// # Concurrency
//
// Immutable and safe for concurrent use, so any number of machines and
// goroutines can share one Spec.
//
// # Allocation contract
//
// [Spec.Next], [Spec.Allows], [Spec.Terminal], [Spec.Known] and
// [Spec.Initial] do not allocate.
type Spec[S, E Enum, D any] struct {
	initial S

	// cells indexes branches by from*nE+on.
	cells    []cell
	branches []branch[S, D]

	// enter and exit hold one action per state, or nil.
	enter []func(*D)
	exit  []func(*D)

	// allows reports an edge by from*nS+to.
	allows   []bool
	known    []bool
	terminal []bool
	lines    []line[S, E]

	nS, nE int
}

// Initial returns the state that [Spec.Start] begins in.
func (sp *Spec[S, E, D]) Initial() S {
	return sp.initial
}

// Next returns the state that on leads to from from. It evaluates the
// guards against d in declaration order and does not run actions. It
// returns from and false when no edge accepts on, including when from
// or on is outside the Spec. d may be nil when no edge for from and on
// has a guard.
func (sp *Spec[S, E, D]) Next(from S, on E, d *D) (S, bool) {
	if int(from) >= sp.nS || int(on) >= sp.nE {
		return from, false
	}

	c := sp.cells[int(from)*sp.nE+int(on)]
	for _, br := range sp.branches[c.first : c.first+c.n] {
		if br.guard == nil || br.guard(d) {
			return br.to, true
		}
	}

	return from, false
}

// Allows reports whether an edge leads from state from to state to,
// whatever its event and guard. A caller that stores a state checks it before it
// swaps the stored value.
func (sp *Spec[S, E, D]) Allows(from, to S) bool {
	if int(from) >= sp.nS || int(to) >= sp.nS {
		return false
	}

	return sp.allows[int(from)*sp.nS+int(to)]
}

// Terminal reports whether s is a terminal state.
func (sp *Spec[S, E, D]) Terminal(s S) bool {
	return int(s) < sp.nS && sp.terminal[s]
}

// Known reports whether s belongs to the Spec: the initial state, the
// end of an edge, a terminal state or a state with an entry or exit
// action.
func (sp *Spec[S, E, D]) Known(s S) bool {
	return int(s) < sp.nS && sp.known[s]
}

// Start returns a machine in the initial state, holding data.
func (sp *Spec[S, E, D]) Start(data D) Machine[S, E, D] {
	return Machine[S, E, D]{spec: sp, data: data, state: sp.initial}
}

// Resume returns a machine in state s, holding data, such as a state
// read from storage. It returns [ErrState] when the Spec does not
// declare s.
func (sp *Spec[S, E, D]) Resume(s S, data D) (Machine[S, E, D], error) {
	if !sp.Known(s) {
		return Machine[S, E, D]{}, ErrState
	}

	return Machine[S, E, D]{spec: sp, data: data, state: s}, nil
}

// Mermaid renders the Spec as a Mermaid state diagram: the initial
// state, every edge in declaration order with its event, and every
// terminal state. A guarded edge is labelled "[guarded]", because a
// guard is a function without a name.
func (sp *Spec[S, E, D]) Mermaid() string {
	var sb strings.Builder

	sb.WriteString("stateDiagram-v2\n    [*] --> ")
	sb.WriteString(sp.initial.String())
	sb.WriteString("\n")

	for _, l := range sp.lines {
		sb.WriteString("    ")
		sb.WriteString(l.from.String())
		sb.WriteString(" --> ")
		sb.WriteString(l.to.String())
		sb.WriteString(" : ")
		sb.WriteString(l.on.String())

		if l.guarded {
			sb.WriteString(" [guarded]")
		}

		sb.WriteString("\n")
	}

	for s := range sp.nS {
		if sp.terminal[s] {
			sb.WriteString("    ")
			sb.WriteString(S(s).String())
			sb.WriteString(" --> [*]\n")
		}
	}

	return sb.String()
}
