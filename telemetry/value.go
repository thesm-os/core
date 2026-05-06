// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

// AttrKind selects which field of [Value] is meaningful. The zero
// value [AttrKindUnspecified] is reserved and indicates a missing
// or unset value; implementations should not record an
// [AttrKindUnspecified] attribute.
type AttrKind uint8

// Attribute kinds.
const (
	// AttrKindUnspecified is the reserved zero value. Treated as
	// missing data rather than as any specific zero.
	AttrKindUnspecified AttrKind = iota

	// AttrKindString — [Value.Str] is meaningful.
	AttrKindString

	// AttrKindInt64 — [Value.Int] is meaningful.
	AttrKindInt64

	// AttrKindFloat64 — [Value.Float] is meaningful.
	AttrKindFloat64

	// AttrKindBool — [Value.Bool] is meaningful.
	AttrKindBool

	// AttrKindBytes — [Value.Bytes] is meaningful.
	AttrKindBytes
)

// Value is a kind-tagged primitive carrying one attribute payload
// without boxing into [any]. The active field is selected by
// [Value.Kind]; the others must be zero.
//
// Carrying primitives directly avoids the per-call allocation an
// `any` field would force on every attribute construction.
//
// # Allocation contract
//
// Value type; pass by value. Construction is zero-alloc.
type Value struct {
	Str   string
	Bytes []byte
	Int   int64
	Float float64
	Kind  AttrKind
	Bool  bool
}
