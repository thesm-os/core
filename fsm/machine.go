// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package fsm

// Machine is one instance of a [Spec]: a current state and the data
// that guards and actions read and update. [Spec.Start] and
// [Spec.Resume] return one; the zero Machine has no Spec and must not
// be used.
//
// An owner can hold a Machine inside its own struct without a separate
// allocation, because a Machine is a value. Copying one yields an
// independent machine with a copy of the state and the data.
//
// # Concurrency
//
// Not safe for concurrent use. The owner serialises calls, typically
// under the lock that already guards the data around the machine.
//
// # Allocation contract
//
// [Machine.Fire], [Machine.State] and [Machine.Data] do not allocate.
type Machine[S, E Enum, D any] struct {
	spec *Spec[S, E, D]
	data D

	state S

	// firing is set while Fire evaluates guards and runs actions. It is
	// what makes a Fire from a guard or action of the same machine fail,
	// and it stays set after a guard or action panics.
	firing bool
}

// Fire delivers on to the machine and returns the state it moves to. It
// tries the edges out of the current state for on in declaration order
// and takes the first whose guard holds or that has no guard.
//
// The transition runs the exit action of the old state, the edge's
// action and the entry action of the new state. The machine is in the
// new state before its entry action runs, and an edge that leads back
// to its own state runs only its edge action.
//
// Error modes:
//
//   - No edge accepts on. Fire returns the current state and
//     [ErrRejected], and changes neither the state nor the data.
//   - Fire is called from a guard or action of the same machine. It
//     returns the current state and [ErrReentrant], and the outer
//     transition completes.
//   - An earlier guard or action panicked. The machine stopped
//     mid-transition, and every later Fire returns [ErrReentrant], so
//     the machine does not continue from a state that no edge
//     describes.
func (m *Machine[S, E, D]) Fire(on E) (S, error) {
	if m.firing {
		return m.state, ErrReentrant
	}

	sp := m.spec
	from := m.state

	if int(on) >= sp.nE {
		return from, ErrRejected
	}

	m.firing = true

	c := sp.cells[int(from)*sp.nE+int(on)]
	for _, br := range sp.branches[c.first : c.first+c.n] {
		if br.guard != nil && !br.guard(&m.data) {
			continue
		}

		if br.to != from {
			if exit := sp.exit[from]; exit != nil {
				exit(&m.data)
			}
		}

		if br.action != nil {
			br.action(&m.data)
		}

		m.state = br.to

		if br.to != from {
			if enter := sp.enter[br.to]; enter != nil {
				enter(&m.data)
			}
		}

		m.firing = false

		return br.to, nil
	}

	m.firing = false

	return from, ErrRejected
}

// State returns the current state.
func (m *Machine[S, E, D]) State() S {
	return m.state
}

// Data returns the machine's data, for the owner to read and update
// between events.
func (m *Machine[S, E, D]) Data() *D {
	return &m.data
}
