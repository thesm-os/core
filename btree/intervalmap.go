// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package btree

import "iter"

// unboundedRank is the rank of an Unbounded end among the ends of one
// point, after the ranks of Excluded and Included.
const unboundedRank = 2

// Bound states whether an [Interval] contains its End or has no end. The
// zero Bound is Excluded, so a zero EndBound makes a half-open interval.
type Bound uint8

// The bounds of the end of an interval.
const (
	// Excluded marks an interval that contains the points from its Start up
	// to its End, without the End.
	Excluded Bound = iota

	// Included marks an interval that contains the points from its Start to
	// its End, with the End.
	Included

	// Unbounded marks an interval without an end. Such an interval
	// contains every point at or after its Start, and does not use its End.
	Unbounded
)

// rank returns the rank of b among the ends of one point: 0 for Excluded
// and for a Bound that counts as Excluded, 1 for Included and
// unboundedRank for Unbounded.
func (b Bound) rank() int {
	switch b {
	case Included:
		return 1
	case Unbounded:
		return unboundedRank
	default:
		return 0
	}
}

// Interval is an interval of points of type P. It contains Start and the
// points after Start up to End. EndBound states whether it contains End. An
// Excluded interval whose End is at or before its Start contains no point,
// and neither does an Included interval whose End is before its Start.
//
// An [IntervalMap] orders intervals by Start, and the intervals of one Start
// by their ends. An end sorts before every later end. An Excluded end sorts
// before the Included end of the same point, and an Unbounded end sorts
// after every other end. Two Unbounded intervals of one Start are one
// interval, whatever their End.
type Interval[P any] struct {
	// Start is the first point of the interval.
	Start P

	// End is the point where the interval ends. EndBound states whether the
	// interval contains it. An Unbounded interval does not use End.
	End P

	// EndBound states how the interval ends. A Bound other than Included and
	// Unbounded counts as Excluded.
	EndBound Bound
}

// points orders the points of intervals, and the intervals by their starts
// and ends.
type points[P any] func(a, b P) int

// compare orders a and b by their starts, and two intervals of one start by
// their ends.
func (c points[P]) compare(a, b Interval[P]) int {
	if s := c(a.Start, b.Start); s != 0 {
		return s
	}

	return c.compareEnds(a, b)
}

// compareEnds orders the ends of a and b by their points. An Excluded end
// sorts before the Included end of the same point, and an Unbounded end
// sorts after every other end.
func (c points[P]) compareEnds(a, b Interval[P]) int {
	ra, rb := a.EndBound.rank(), b.EndBound.rank()
	if ra == unboundedRank || rb == unboundedRank {
		return ra - rb
	}
	if e := c(a.End, b.End); e != 0 {
		return e
	}

	return ra - rb
}

// before reports whether p comes before the end of iv, so that iv contains p
// when p is at or after the start of iv.
func (c points[P]) before(p P, iv Interval[P]) bool {
	switch iv.EndBound {
	case Unbounded:
		return true
	case Included:
		return c(p, iv.End) <= 0
	default:
		return c(p, iv.End) < 0
	}
}

// IntervalMap is a map from intervals to values, ordered by the function
// that [NewIntervalMap] receives. [IntervalMap.Containing] finds the
// intervals that contain a point, and [IntervalMap.Overlapping] the
// intervals that overlap an interval.
//
// An internal node keeps the max end of each of its children. The max end
// of a child is the interval under the child whose end is the latest. A
// query skips each child whose max end comes before the start of the query.
// It ends at the first interval that starts after the end of the query.
//
// Get, Has, Set, Update, Delete, Len, Clear, Reset and All have the
// contract of the method of [MapFunc] with the same name. An interval takes
// the place of a key.
//
// The zero IntervalMap has no order, and a method that compares two
// intervals panics on it. A caller creates an IntervalMap with
// NewIntervalMap.
//
// # Concurrency
//
// Not safe for concurrent use. Any number of goroutines may read a map that
// no goroutine writes. A query only reads the map.
//
// # Allocation contract
//
// The queries allocate as [Map.All] does. A write allocates as [Map.Set]
// does: the array of max ends of an internal node shares the allocation of
// its child array. A map whose size does not change reuses the nodes that
// its deletes free, and does not allocate.
type IntervalMap[P, V any] struct {
	cmp points[P]
	t   tree[Interval[P], V, custom[Interval[P]]]
}

// NewIntervalMap returns an empty map whose intervals have points ordered
// by cmp. cmp returns a negative number when a sorts before b, zero when a
// and b are the same point, and a positive number when a sorts after b. It
// must define a total order: a function that does not makes lookups miss
// intervals and queries yield intervals out of order, and the map does not
// detect it.
//
// NewIntervalMap panics when cmp is nil.
func NewIntervalMap[P, V any](cmp func(a, b P) int) *IntervalMap[P, V] {
	if cmp == nil {
		//nolint:forbidigo // a map without an order is a programmer error at construction
		panic("btree: NewIntervalMap with a nil comparison function")
	}

	m := &IntervalMap[P, V]{cmp: cmp}
	m.t.order = m.cmp.compare
	m.t.endsBefore = func(a, b Interval[P]) bool { return m.cmp.compareEnds(a, b) < 0 }

	return m
}

// Get returns the value of iv, and reports whether iv is present. See
// [Map.Get].
func (m *IntervalMap[P, V]) Get(iv Interval[P]) (V, bool) {
	return m.t.get(iv)
}

// Has reports whether iv is present. See [Map.Has].
func (m *IntervalMap[P, V]) Has(iv Interval[P]) bool {
	_, ok := m.t.get(iv)

	return ok
}

// Set sets the value of iv, and returns the previous value and whether iv
// was present. See [Map.Set].
func (m *IntervalMap[P, V]) Set(iv Interval[P], value V) (V, bool) {
	return m.t.set(iv, value)
}

// Update sets the value of iv to fn(old, exists), and returns the new
// value. See [Map.Update].
func (m *IntervalMap[P, V]) Update(iv Interval[P], fn func(old V, exists bool) V) V {
	return m.t.update(iv, fn)
}

// Delete removes iv, and returns its value and whether iv was present. See
// [Map.Delete].
func (m *IntervalMap[P, V]) Delete(iv Interval[P]) (V, bool) {
	return m.t.delete(iv)
}

// Len returns the number of intervals in O(1).
func (m *IntervalMap[P, V]) Len() int {
	return m.t.len
}

// Clear removes every interval. See [Map.Clear].
func (m *IntervalMap[P, V]) Clear() {
	m.t.clear()
}

// Reset removes every interval, and keeps the nodes of the map for the
// inserts that follow. See [Map.Reset].
func (m *IntervalMap[P, V]) Reset() {
	m.t.reset()
}

// All returns an iterator over the intervals and their values in the order
// of the intervals. See [Map.All].
func (m *IntervalMap[P, V]) All() iter.Seq2[Interval[P], V] {
	return m.t.walk
}

// Containing returns an iterator over the intervals that contain p and
// their values, in the order of the intervals. A write during the iteration
// has the effect that it has on [Map.All].
//
// # Allocation contract
//
// As [Map.All].
func (m *IntervalMap[P, V]) Containing(p P) iter.Seq2[Interval[P], V] {
	return m.Overlapping(Interval[P]{Start: p, End: p, EndBound: Included})
}

// Overlapping returns an iterator over the intervals that have a point in
// common with q and their values, in the order of the intervals. An empty q
// overlaps no interval. A write during the iteration has the effect that it
// has on [Map.All].
//
// The query descends from the root, and visits each child whose max end
// comes after the start of q. It ends at the first interval that starts
// after the end of q.
//
// # Allocation contract
//
// As [Map.All].
func (m *IntervalMap[P, V]) Overlapping(q Interval[P]) iter.Seq2[Interval[P], V] {
	return func(yield func(Interval[P], V) bool) {
		if !m.cmp.before(q.Start, q) {
			return
		}
		var last Interval[P]
		for resumed := false; ; resumed = true {
			var changed bool
			if last, changed = m.overlap(q, last, resumed, yield); !changed {
				return
			}
		}
	}
}

// overlap calls yield for each interval that overlaps q, a query that is
// not empty, in the order of the intervals. When resumed is set, it starts
// at the first interval after last, in the leaf where last is or would be.
// It returns the interval of its last yield, and reports whether the map
// changed after that yield, so the caller continues after it.
//
// The walk keeps the internal nodes above the current node in path, each
// with the index of the child that the walk descended into.
func (m *IntervalMap[P, V]) overlap(
	q, last Interval[P], resumed bool, yield func(Interval[P], V) bool,
) (Interval[P], bool) {
	t := &m.t
	writes := t.writes
	if t.root == nil {
		if t.leaf == nil {
			return Interval[P]{}, false
		}
		from := 0
		if resumed {
			from = t.order.route(t.leaf.keys[:t.leaf.n], last)
		}
		yielded, _, changed := m.scan(t.leaf, from, q, writes, yield)

		return yielded, changed
	}

	var c cursor[Interval[P], V]
	path := c.path[:0]
	in, i := t.root, 0
	if resumed {
		t.seekGT(&c, last)
		if yielded, stop, changed := m.scan(c.leaf, c.i, q, writes, yield); stop {
			return yielded, changed
		}
		top := c.path[c.depth-1]
		path = c.path[:c.depth-1]
		in, i = top.n, top.i+1
	}
	for {
		if i > in.n {
			if len(path) == 0 {
				return Interval[P]{}, false
			}
			top := path[len(path)-1]
			path = path[:len(path)-1]
			in, i = top.n, top.i+1

			continue
		}
		if !m.cmp.before(q.Start, in.maxEnds[i]) {
			i++

			continue
		}
		if in.leaves != nil {
			if yielded, stop, changed := m.scan(in.leaves[i], 0, q, writes, yield); stop {
				return yielded, changed
			}
			i++

			continue
		}
		path = append(path, step[Interval[P], V]{in, i})
		in, i = in.inners[i], 0
	}
}

// scan calls yield for each interval of l from index from on that overlaps
// q, a query that is not empty. It reports stop when the query ends in l:
// when yield returns false, when an interval of l starts after the end of
// q, or when the map changed after a yield, which changed reports. yielded
// is the interval of the last yield.
//
// The intervals after l start at or after the intervals of l, so the first
// interval that starts after the end of q ends the query.
func (m *IntervalMap[P, V]) scan(
	l *leaf[Interval[P], V], from int, q Interval[P], writes uint64, yield func(Interval[P], V) bool,
) (yielded Interval[P], stop, changed bool) {
	for j := from; j < l.n; j++ {
		// A write in yield can shift the intervals of l, so the interval and
		// its value are read before the call.
		iv, v := l.keys[j], l.vals[j]
		if !m.cmp.before(iv.Start, q) {
			return iv, true, false
		}
		// The first point that both intervals can share is the later start,
		// and it comes before the end of q.
		first := q.Start
		//dokimi:mutate-skip ror-boundary: the two starts are one point when cmp returns 0
		if m.cmp(iv.Start, first) > 0 {
			first = iv.Start
		}
		if !m.cmp.before(first, iv) {
			continue
		}
		if !yield(iv, v) {
			return iv, true, false
		}
		//dokimi:mutate-skip ror-true: a query that continues after each yield yields the same intervals
		if m.t.writes != writes {
			return iv, true, true
		}
	}

	return Interval[P]{}, false, false
}
