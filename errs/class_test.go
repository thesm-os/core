// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package errs_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"log/slog"
	"runtime"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
)

// The encoding interfaces Class satisfies. A missing method is a build
// failure.
var (
	_ encoding.TextAppender    = errs.Class(0)
	_ encoding.TextMarshaler   = errs.Class(0)
	_ encoding.TextUnmarshaler = (*errs.Class)(nil)
)

// names pins the name of every class. The names are a persisted
// encoding, so this table is the recorded form a change must match.
var names = []struct {
	class errs.Class
	name  string
}{
	{errs.Unspecified, "Unspecified"},
	{errs.Transient, "Transient"},
	{errs.Conflict, "Conflict"},
	{errs.NotFound, "NotFound"},
	{errs.Invalid, "Invalid"},
	{errs.Unsupported, "Unsupported"},
	{errs.Denied, "Denied"},
	{errs.Integrity, "Integrity"},
}

// outOfRange are values of Class outside the eight classes: the first
// one past Integrity, and one far past it.
var outOfRange = []errs.Class{errs.Integrity + 1, 99}

func TestClassString(t *testing.T) {
	t.Parallel()

	for _, tc := range names {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, tc.class.String(), tc.name, "String must name the class")
		})
	}

	t.Run("an out-of-range value renders numerically", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, errs.Class(99).String(), "Class(99)",
			"an unrecognised class must stay distinguishable in a log line")
	})

	t.Run("every defined class has a distinct name", func(t *testing.T) {
		t.Parallel()
		seen := make(map[string]errs.Class, len(names))
		for _, tc := range names {
			prior, dup := seen[tc.class.String()]
			testkit.False(t, dup,
				"two classes share the name "+tc.class.String()+
					" (also "+prior.String()+")")
			seen[tc.class.String()] = tc.class
		}
	})
}

func TestClassOrdering(t *testing.T) {
	t.Parallel()

	// Unspecified must stay the zero value: an error nobody has
	// reasoned about has to be non-retryable by default, and that
	// property comes from its position, not from its name.
	t.Run("Unspecified is the zero value", func(t *testing.T) {
		t.Parallel()
		var zero errs.Class
		testkit.Equal(t, zero, errs.Unspecified, "the zero Class must be Unspecified")
	})
}

func TestClassAppendText(t *testing.T) {
	t.Parallel()

	t.Run("appends the name of each class to dst", func(t *testing.T) {
		t.Parallel()
		for _, tc := range names {
			got, err := tc.class.AppendText([]byte("class="))
			testkit.NoError(t, err, "AppendText must accept "+tc.name)
			testkit.Equal(t, string(got), "class="+tc.name, "AppendText must append the name")
		}
	})

	t.Run("returns ErrUnknownClass for a value outside the eight", func(t *testing.T) {
		t.Parallel()
		for _, c := range outOfRange {
			got, err := c.AppendText([]byte("class="))
			testkit.ErrorIs(t, err, errs.ErrUnknownClass, "AppendText must refuse "+c.String())
			testkit.Equal(t, string(got), "class=", "a refused append must leave dst unchanged")
		}
	})
}

func TestClassMarshalText(t *testing.T) {
	t.Parallel()

	t.Run("returns the name of each class", func(t *testing.T) {
		t.Parallel()
		for _, tc := range names {
			got, err := tc.class.MarshalText()
			testkit.NoError(t, err, "MarshalText must accept "+tc.name)
			testkit.Equal(t, string(got), tc.name, "MarshalText must return the name")
		}
	})

	t.Run("returns ErrUnknownClass for a value outside the eight", func(t *testing.T) {
		t.Parallel()
		for _, c := range outOfRange {
			_, err := c.MarshalText()
			testkit.ErrorIs(t, err, errs.ErrUnknownClass, "MarshalText must refuse "+c.String())
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrUnknownClass must classify as Invalid")
		}
	})

	t.Run("encodes a class in JSON as its name", func(t *testing.T) {
		t.Parallel()
		got, err := json.Marshal(struct {
			Class errs.Class `json:"class"`
		}{errs.Transient})
		testkit.NoError(t, err, "json.Marshal must succeed")
		testkit.Equal(t, string(got), `{"class":"Transient"}`, "JSON must contain the name, not the number")
	})

	t.Run("writes a class as its name in the JSON handler of log/slog", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("classified", "class", errs.Transient)
		testkit.True(t, bytes.Contains(buf.Bytes(), []byte(`"class":"Transient"`)),
			"a JSON log line must contain the name, not the number: "+buf.String())
	})
}

func TestClassUnmarshalText(t *testing.T) {
	t.Parallel()

	t.Run("decodes the name of each class", func(t *testing.T) {
		t.Parallel()
		for _, tc := range names {
			got := errs.Class(99)
			testkit.NoError(t, got.UnmarshalText([]byte(tc.name)), "UnmarshalText must accept "+tc.name)
			testkit.Equal(t, got, tc.class, "UnmarshalText must decode "+tc.name)
		}
	})

	tests := []struct {
		name string
		give string
	}{
		{name: "returns ErrUnknownClass for empty text", give: ""},
		{name: "returns ErrUnknownClass for a name in lower case", give: "transient"},
		{name: "returns ErrUnknownClass for the rendering of an out-of-range value", give: "Class(8)"},
		{name: "returns ErrUnknownClass for a name no class has", give: "Throttled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := errs.Denied
			err := got.UnmarshalText([]byte(tt.give))
			testkit.ErrorIs(t, err, errs.ErrUnknownClass, "UnmarshalText must refuse "+tt.give)
			testkit.Equal(t, got, errs.Denied, "a refused decode must leave the class unchanged")
		})
	}

	t.Run("decodes a class from its JSON name", func(t *testing.T) {
		t.Parallel()
		var got struct {
			Class errs.Class `json:"class"`
		}
		testkit.NoError(t, json.Unmarshal([]byte(`{"class":"Integrity"}`), &got), "json.Unmarshal must succeed")
		testkit.Equal(t, got.Class, errs.Integrity, "JSON must decode the name")
	})
}

func BenchmarkClassString(b *testing.B) {
	b.ReportAllocs()
	var sink string
	for b.Loop() {
		sink = errs.Transient.String()
	}
	runtime.KeepAlive(sink)
}

func BenchmarkClassAppendText(b *testing.B) {
	buf := make([]byte, 0, len("Unsupported"))
	b.ReportAllocs()
	for b.Loop() {
		buf, _ = errs.Unsupported.AppendText(buf[:0])
	}
	runtime.KeepAlive(buf)
}

func BenchmarkClassUnmarshalText(b *testing.B) {
	text := []byte("Integrity")
	var c errs.Class
	b.ReportAllocs()
	for b.Loop() {
		_ = c.UnmarshalText(text)
	}
	runtime.KeepAlive(c)
}
