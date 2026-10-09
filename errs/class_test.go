// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package errs_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"log/slog"
	"math"
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/errs"
)

// The encoding interfaces Class satisfies. A missing method is a build
// failure.
var (
	_ encoding.TextAppender    = errs.Class(0)
	_ encoding.TextMarshaler   = errs.Class(0)
	_ encoding.TextUnmarshaler = (*errs.Class)(nil)
)

// refuseContract is the contract of refusesText, which TestClass and
// FuzzClass check.
const refuseContract = "UnmarshalText must refuse every text that names no class"

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

// The generators of the properties over classes: defined generates the
// eight classes, and undefined the values of Class past Integrity.
var (
	defined   = prop.Integer[errs.Class](errs.Unspecified, errs.Integrity)
	undefined = prop.Integer[errs.Class](errs.Integrity+1, math.MaxUint8)
)

func TestClass(t *testing.T) {
	t.Parallel()

	// Unspecified must stay the zero value: an error nobody has
	// reasoned about has to be non-retryable by default, and that
	// property comes from its position, not from its name.
	t.Run("is Unspecified at the zero value", func(t *testing.T) {
		t.Parallel()
		var zero errs.Class
		assert.Equal(t, zero, errs.Unspecified, "the zero Class must be Unspecified")
	})

	// A kanon record encodes a class as its number, so the numbers are
	// a persisted encoding, as the names are. names lists the classes
	// in the order of their constants.
	t.Run("numbers the classes from 0 to 7 in the order of the constants", func(t *testing.T) {
		t.Parallel()
		got := make([]int, 0, len(names))
		for _, tc := range names {
			got = append(got, int(tc.class))
		}
		assert.Equal(t, got, []int{0, 1, 2, 3, 4, 5, 6, 7}, "every class must keep its number")
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		for _, tc := range names {
			t.Run("returns "+tc.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, tc.class.String(), tc.name, "String must name the class")
			})
		}

		t.Run("returns Class(N) for a value outside the eight", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, errs.Class(99).String(), "Class(99)",
				"an unrecognised class must stay distinguishable in a log line")
			prop.Matches(t, errs.Class.String, `^Class\([0-9]+\)$`,
				"String must render every value past Integrity as Class(N)", prop.Using(undefined))
		})

		t.Run("returns a distinct name for each class", func(t *testing.T) {
			t.Parallel()
			assert.NoDuplicates(t, func() ([]string, error) {
				got := make([]string, 0, len(names))
				for c := range errs.Integrity + 1 {
					got = append(got, c.String())
				}

				return got, nil
			}, "no two classes may share a name")
		})
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the name of each class to dst", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendText must extend dst with the name of the class", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				class := c.Draw(defined, "class")

				got, err := class.AppendText(dst)
				assert.NoError(c, err, "AppendText must accept "+class.String())
				assert.Equal(c, got, append(slices.Clip(dst), class.String()...),
					"AppendText must keep dst and append the name")
			})
		})

		t.Run("returns ErrUnknownClass for a value outside the eight", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "AppendText must refuse every value past Integrity", func(c *prop.Case) {
				dst := c.Draw(prop.Bytes(prop.MaxSize(8)), "dst")
				class := c.Draw(undefined, "class")

				got, err := class.AppendText(dst)
				assert.ErrorIs(c, err, errs.ErrUnknownClass, "AppendText must refuse "+class.String())
				assert.Equal(c, got, dst, "a refused append must return dst unchanged")
			})
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the name of each class", func(t *testing.T) {
			t.Parallel()
			for _, tc := range names {
				got, err := tc.class.MarshalText()
				assert.NoError(t, err, "MarshalText must accept "+tc.name)
				assert.Equal(t, string(got), tc.name, "MarshalText must return the name")
			}
		})

		t.Run("returns ErrUnknownClass for a value outside the eight", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(c errs.Class) error {
				_, err := c.MarshalText()

				return err
			}, errs.ErrUnknownClass, "MarshalText must refuse every value past Integrity", prop.Using(undefined))
			assert.Equal(t, errs.Classify(errs.ErrUnknownClass), errs.Invalid,
				"ErrUnknownClass must classify as Invalid")
		})

		t.Run("encodes a class in JSON as its name", func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(struct {
				Class errs.Class `json:"class"`
			}{errs.Transient})
			assert.NoError(t, err, "json.Marshal must succeed")
			assert.Equal(t, string(got), `{"class":"Transient"}`, "JSON must contain the name, not the number")
		})

		t.Run("writes a class as its name in the JSON handler of log/slog", func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			slog.New(slog.NewJSONHandler(&buf, nil)).Info("classified", "class", errs.Transient)
			assert.Contains(t, buf.String(), `"class":"Transient"`,
				"a JSON log line must contain the name, not the number")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the class that MarshalText encodes", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, errs.Class.MarshalText, func(text []byte) (errs.Class, error) {
				var c errs.Class
				err := c.UnmarshalText(text)

				return c, err
			}, "UnmarshalText must undo MarshalText for each of the eight classes", prop.Using(defined))
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

				var err error
				assert.Pure(t, func() errs.Class { return got }, func() { err = got.UnmarshalText([]byte(tt.give)) },
					"a refused decode must leave the class unchanged")
				assert.ErrorIs(t, err, errs.ErrUnknownClass, "UnmarshalText must refuse "+tt.give)
			})
		}

		t.Run("returns ErrUnknownClass for any text but a name", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, refuseContract, refusesText)
		})

		t.Run("decodes a class from its JSON name", func(t *testing.T) {
			t.Parallel()
			var got struct {
				Class errs.Class `json:"class"`
			}
			assert.NoError(t, json.Unmarshal([]byte(`{"class":"Integrity"}`), &got), "json.Unmarshal must succeed")
			assert.Equal(t, got.Class, errs.Integrity, "JSON must decode the name")
		})
	})
}

// FuzzClass checks the contract of refusesText on the texts that a fuzzer
// finds.
func FuzzClass(f *testing.F) {
	prop.Fuzz(f, refuseContract, refusesText)
}

// TestClassAllocs checks the allocation contract of each method of a
// Class. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
func TestClassAllocs(t *testing.T) {
	t.Run("String", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = errs.Transient.String() }, 0, "String must not allocate for a class")
		assert.Equal(t, got, "Transient", "the test must measure a defined class")
	})

	t.Run("AppendText", func(t *testing.T) {
		buf := make([]byte, 0, len("Unsupported"))

		var (
			got []byte
			err error
		)
		expect.MaxAllocs(t, func() { got, err = errs.Unsupported.AppendText(buf[:0]) }, 0,
			"AppendText must not allocate into a buffer with room for the name")
		assert.NoError(t, err, "AppendText must accept a defined class")
		assert.Equal(t, string(got), "Unsupported", "the test must measure the name")
	})

	t.Run("MarshalText", func(t *testing.T) {
		var (
			got []byte
			err error
		)
		expect.MaxAllocs(t, func() { got, err = errs.Denied.MarshalText() }, 1,
			"MarshalText must allocate only the returned slice")
		assert.NoError(t, err, "MarshalText must accept a defined class")
		assert.Equal(t, string(got), "Denied", "the test must measure the name")
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		text := []byte("Integrity")

		var (
			got errs.Class
			err error
		)
		expect.MaxAllocs(t, func() { err = got.UnmarshalText(text) }, 0, "UnmarshalText must not allocate")
		assert.NoError(t, err, "UnmarshalText must accept a name")
		assert.Equal(t, got, errs.Integrity, "the test must measure a decode")
	})
}

func BenchmarkClass(b *testing.B) {
	b.Run("String", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = errs.Transient.String()
		}

		assert.Equal(b, got, "Transient", "the benchmark must measure a defined class")
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len("Unsupported"))

		var (
			got []byte
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got, err = errs.Unsupported.AppendText(buf[:0])
		}

		assert.NoError(b, err, "AppendText must accept a defined class")
		assert.Equal(b, string(got), "Unsupported", "the benchmark must measure the name")
	})

	b.Run("MarshalText", func(b *testing.B) {
		var (
			got []byte
			err error
		)

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, err = errs.Denied.MarshalText()
		}

		assert.NoError(b, err, "MarshalText must accept a defined class")
		assert.Equal(b, string(got), "Denied", "the benchmark must measure the name")
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		text := []byte("Integrity")

		var (
			got errs.Class
			err error
		)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			err = got.UnmarshalText(text)
		}

		assert.NoError(b, err, "UnmarshalText must accept a name")
		assert.Equal(b, got, errs.Integrity, "the benchmark must measure a decode")
	})
}

// refusesText decodes a text that names no class, invalid UTF-8 included,
// into a Class of Denied. The decode must return ErrUnknownClass and leave
// the class unchanged.
func refusesText(c *prop.Case) {
	text := c.Draw(prop.Bytes(prop.MaxSize(16)).Filter(func(b []byte) bool {
		for _, n := range names {
			if n.name == string(b) {
				return false
			}
		}

		return true
	}), "text")
	got := errs.Denied

	var err error
	assert.Pure(c, func() errs.Class { return got }, func() { err = got.UnmarshalText(text) },
		"a refused decode must leave the class unchanged")
	assert.ErrorIs(c, err, errs.ErrUnknownClass, "UnmarshalText must refuse "+string(text))
}
