// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package telemetry

import "log/slog"

// Attr is one key/value attribute attached to a metric data point,
// span, or log record.
//
// # Allocation contract
//
// Value type; pass by value. The constructor functions
// ([AttrString], [AttrInt], [AttrFloat], [AttrBool], [AttrBytes])
// are zero-allocation.
type Attr struct {
	Key   string
	Value Value
}

// AttrString returns an [Attr] carrying a string value.
//
// # Allocation contract
//
// Zero alloc.
func AttrString(key, value string) Attr {
	return Attr{Key: key, Value: Value{Kind: AttrKindString, Str: value}}
}

// AttrInt returns an [Attr] carrying an int64 value.
//
// # Allocation contract
//
// Zero alloc.
func AttrInt(key string, value int64) Attr {
	return Attr{Key: key, Value: Value{Kind: AttrKindInt64, Int: value}}
}

// AttrFloat returns an [Attr] carrying a float64 value.
//
// # Allocation contract
//
// Zero alloc.
func AttrFloat(key string, value float64) Attr {
	return Attr{Key: key, Value: Value{Kind: AttrKindFloat64, Float: value}}
}

// AttrBool returns an [Attr] carrying a bool value.
//
// # Allocation contract
//
// Zero alloc.
func AttrBool(key string, value bool) Attr {
	return Attr{Key: key, Value: Value{Kind: AttrKindBool, Bool: value}}
}

// AttrBytes returns an [Attr] carrying a byte slice. The slice
// header is copied; the underlying array is shared. Callers must
// not mutate value after constructing the [Attr].
//
// # Allocation contract
//
// Zero alloc.
func AttrBytes(key string, value []byte) Attr {
	return Attr{Key: key, Value: Value{Kind: AttrKindBytes, Bytes: value}}
}

// SlogAttr converts a [telemetry.Attr] to a [slog.Attr] without
// boxing primitive values. Used to bridge the unified attribute
// vocabulary to stdlib [log/slog]:
//
//	logger.LogAttrs(ctx, slog.LevelInfo, "request",
//	    a.SlogAttr(), b.SlogAttr())
//
// AttrKindUnspecified maps to a [slog.Attr] with the empty key
// (slog elides empty-key attrs from output by convention).
//
// # Allocation contract
//
// Zero alloc for every primitive kind — [AttrKindString],
// [AttrKindInt64], [AttrKindFloat64], [AttrKindBool], and
// [AttrKindUnspecified]. [AttrKindBytes] allocates one heap copy
// per call: stdlib [slog] has no zero-copy bytes value, so the
// bridge converts via `string(b)` which copies the byte content.
// Callers on a hot path should prefer [AttrKindString] when the
// value is already a string.
func (a Attr) SlogAttr() slog.Attr {
	switch a.Value.Kind {
	case AttrKindString:
		return slog.String(a.Key, a.Value.Str)
	case AttrKindInt64:
		return slog.Int64(a.Key, a.Value.Int)
	case AttrKindFloat64:
		return slog.Float64(a.Key, a.Value.Float)
	case AttrKindBool:
		return slog.Bool(a.Key, a.Value.Bool)
	case AttrKindBytes:
		return slog.String(a.Key, string(a.Value.Bytes))
	default:
		return slog.Attr{}
	}
}
