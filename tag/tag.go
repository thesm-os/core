// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tag

//go:generate go tool kanon -type=Tag -canonical

// Tag is a string key and value pair. Tag is comparable and is passed by
// value.
//
// # Encoding
//
// kanon generates the codec of Tag, so the record of every consumer
// encodes a tag with the field numbers that this package records: Key 1
// and Value 2. A field keeps its number, and a new field takes a new
// number. [Tag.AppendBinary] and [Tag.MarshalBinary] write the same
// encoding.
//
// The decode of the codec accepts only the encoding that the encode
// writes. It returns an error that wraps
// [go.thesmos.sh/kanon.ErrNotCanonical] for any other input that the wire
// format lets a decoder accept, such as the two fields in the other order,
// an empty Key written as a field, or a field number that this package
// does not record. A reader built with an earlier version of this package
// rejects a field that a later version adds, so every reader upgrades
// before a writer sets a new field.
//
// The methods of the codec have pointer receivers, so encoding/gob encodes
// a Tag only when it can take its address, as it can for the value of a
// pointer or an element of a slice. gob fails for a Tag in a struct that
// it receives by value, with "gob: unaddressable value".
//
// # Allocation contract
//
// Construction, comparison and [Tag.IsZero] do not allocate. The methods
// of the codec follow the allocation contract of
// [go.thesmos.sh/kanon.Message].
type Tag struct {
	Key   string
	Value string
}

// IsZero reports whether t is the zero [Tag] (both fields empty).
func (t Tag) IsZero() bool {
	return t.Key == "" && t.Value == ""
}
