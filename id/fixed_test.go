// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package id_test

import (
	"bytes"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/id"
)

// fixedType describes the fixed-size identifier type K to the cases that
// every fixed-size type shares. asOf is the method of ID that converts an ID
// to K, and as is its name. toID, compare and str are the ID, Compare and
// String methods of K. of converts bytes of the size of K to a K, and bytesOf
// returns the bytes of a K.
type fixedType[K comparable] struct {
	asOf    func(id.ID) (K, bool)
	toID    func(K) id.ID
	compare func(K, K) int
	str     func(K) string
	of      func([]byte) K
	bytesOf func(K) []byte
	name    string
	as      string
	size    int
}

// fixed128, fixed160 and fixed256 describe ID128, ID160 and ID256.
var (
	fixed128 = fixedType[id.ID128]{
		name: "ID128", as: "As128", size: id.Size128,
		asOf: id.ID.As128, toID: id.ID128.ID, compare: id.ID128.Compare, str: id.ID128.String,
		of:      func(b []byte) id.ID128 { return id.ID128(b) },
		bytesOf: func(k id.ID128) []byte { return k[:] },
	}
	fixed160 = fixedType[id.ID160]{
		name: "ID160", as: "As160", size: id.Size160,
		asOf: id.ID.As160, toID: id.ID160.ID, compare: id.ID160.Compare, str: id.ID160.String,
		of:      func(b []byte) id.ID160 { return id.ID160(b) },
		bytesOf: func(k id.ID160) []byte { return k[:] },
	}
	fixed256 = fixedType[id.ID256]{
		name: "ID256", as: "As256", size: id.Size256,
		asOf: id.ID.As256, toID: id.ID256.ID, compare: id.ID256.Compare, str: id.ID256.String,
		of:      func(b []byte) id.ID256 { return id.ID256(b) },
		bytesOf: func(k id.ID256) []byte { return k[:] },
	}
)

func TestFixed(t *testing.T) {
	t.Parallel()

	testFixedType(t, fixed128)
	testFixedType(t, fixed160)
	testFixedType(t, fixed256)

	t.Run("ID", func(t *testing.T) {
		t.Parallel()

		testAs(t, fixed128)
		testAs(t, fixed160)
		testAs(t, fixed256)
	})
}

// TestFixedAllocs checks the allocation contracts of the fixed-size types
// and of the conversions of an ID. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
func TestFixedAllocs(t *testing.T) {
	a128, b128 := id.ID128(fill128(0x42)), id.ID128(fill128(0x43))
	a160, b160 := id.ID160(fill160(0x42)), id.ID160(fill160(0x43))
	a256, b256 := id.ID256(fill256(0x42)), id.ID256(fill256(0x43))
	i128, i160, i256 := id.New128(fill128(0x42)), id.New160(fill160(0x42)), id.New256(fill256(0x42))

	t.Run("ID128", func(t *testing.T) {
		t.Run("ID", func(t *testing.T) {
			var got id.ID
			expect.MaxAllocs(t, func() { got = a128.ID() }, 0, "ID must not allocate")
			assert.Equal(t, got.Size(), id.Size128, "the test must measure an ID of Size128")
		})

		t.Run("Compare", func(t *testing.T) {
			var order int
			expect.MaxAllocs(t, func() { order = a128.Compare(b128) }, 0, "Compare must not allocate")
			assert.Equal(t, order, -1, "the test must measure two identifiers that differ")
		})

		t.Run("String", func(t *testing.T) {
			var s string
			expect.MaxAllocs(t, func() { s = a128.String() }, 1, "String must allocate only the result")
			assert.Length(t, s, len("id:")+2*id.Size128, "the test must measure the string of the identifier")
		})
	})

	t.Run("ID160", func(t *testing.T) {
		t.Run("ID", func(t *testing.T) {
			var got id.ID
			expect.MaxAllocs(t, func() { got = a160.ID() }, 0, "ID must not allocate")
			assert.Equal(t, got.Size(), id.Size160, "the test must measure an ID of Size160")
		})

		t.Run("Compare", func(t *testing.T) {
			var order int
			expect.MaxAllocs(t, func() { order = a160.Compare(b160) }, 0, "Compare must not allocate")
			assert.Equal(t, order, -1, "the test must measure two identifiers that differ")
		})

		t.Run("String", func(t *testing.T) {
			var s string
			expect.MaxAllocs(t, func() { s = a160.String() }, 1, "String must allocate only the result")
			assert.Length(t, s, len("id:")+2*id.Size160, "the test must measure the string of the identifier")
		})
	})

	t.Run("ID256", func(t *testing.T) {
		t.Run("ID", func(t *testing.T) {
			var got id.ID
			expect.MaxAllocs(t, func() { got = a256.ID() }, 0, "ID must not allocate")
			assert.Equal(t, got.Size(), id.Size256, "the test must measure an ID of Size256")
		})

		t.Run("Compare", func(t *testing.T) {
			var order int
			expect.MaxAllocs(t, func() { order = a256.Compare(b256) }, 0, "Compare must not allocate")
			assert.Equal(t, order, -1, "the test must measure two identifiers that differ")
		})

		t.Run("String", func(t *testing.T) {
			var s string
			expect.MaxAllocs(t, func() { s = a256.String() }, 1, "String must allocate only the result")
			assert.Length(t, s, len("id:")+2*id.Size256, "the test must measure the string of the identifier")
		})
	})

	t.Run("ID", func(t *testing.T) {
		t.Run("As128", func(t *testing.T) {
			var ok bool
			expect.MaxAllocs(t, func() { _, ok = i128.As128() }, 0, "As128 must not allocate")
			assert.True(t, ok, "the test must measure an ID of Size128")
		})

		t.Run("As160", func(t *testing.T) {
			var ok bool
			expect.MaxAllocs(t, func() { _, ok = i160.As160() }, 0, "As160 must not allocate")
			assert.True(t, ok, "the test must measure an ID of Size160")
		})

		t.Run("As256", func(t *testing.T) {
			var ok bool
			expect.MaxAllocs(t, func() { _, ok = i256.As256() }, 0, "As256 must not allocate")
			assert.True(t, ok, "the test must measure an ID of Size256")
		})
	})
}

// BenchmarkFixed reports the cost of the methods of the fixed-size types and
// of the conversions of an ID, and fails when one allocates more than
// TestFixedAllocs allows.
func BenchmarkFixed(b *testing.B) {
	a128, b128 := id.ID128(fill128(0x42)), id.ID128(fill128(0x43))
	a160, b160 := id.ID160(fill160(0x42)), id.ID160(fill160(0x43))
	a256, b256 := id.ID256(fill256(0x42)), id.ID256(fill256(0x43))
	i128, i160, i256 := id.New128(fill128(0x42)), id.New160(fill160(0x42)), id.New256(fill256(0x42))

	b.Run("ID128", func(b *testing.B) {
		b.Run("ID", func(b *testing.B) {
			var got id.ID

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = a128.ID()
			}

			assert.Equal(b, got.Size(), id.Size128, "the benchmark must measure an ID of Size128")
		})

		b.Run("Compare", func(b *testing.B) {
			var order int

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				order = a128.Compare(b128)
			}

			assert.Equal(b, order, -1, "the benchmark must measure two identifiers that differ")
		})

		b.Run("String", func(b *testing.B) {
			var s string

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				s = a128.String()
			}

			assert.Length(b, s, len("id:")+2*id.Size128, "the benchmark must measure the string of the identifier")
		})
	})

	b.Run("ID160", func(b *testing.B) {
		b.Run("ID", func(b *testing.B) {
			var got id.ID

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = a160.ID()
			}

			assert.Equal(b, got.Size(), id.Size160, "the benchmark must measure an ID of Size160")
		})

		b.Run("Compare", func(b *testing.B) {
			var order int

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				order = a160.Compare(b160)
			}

			assert.Equal(b, order, -1, "the benchmark must measure two identifiers that differ")
		})

		b.Run("String", func(b *testing.B) {
			var s string

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				s = a160.String()
			}

			assert.Length(b, s, len("id:")+2*id.Size160, "the benchmark must measure the string of the identifier")
		})
	})

	b.Run("ID256", func(b *testing.B) {
		b.Run("ID", func(b *testing.B) {
			var got id.ID

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got = a256.ID()
			}

			assert.Equal(b, got.Size(), id.Size256, "the benchmark must measure an ID of Size256")
		})

		b.Run("Compare", func(b *testing.B) {
			var order int

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				order = a256.Compare(b256)
			}

			assert.Equal(b, order, -1, "the benchmark must measure two identifiers that differ")
		})

		b.Run("String", func(b *testing.B) {
			var s string

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				s = a256.String()
			}

			assert.Length(b, s, len("id:")+2*id.Size256, "the benchmark must measure the string of the identifier")
		})
	})

	b.Run("ID", func(b *testing.B) {
		b.Run("As128", func(b *testing.B) {
			var ok bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, ok = i128.As128()
			}

			assert.True(b, ok, "the benchmark must measure an ID of Size128")
		})

		b.Run("As160", func(b *testing.B) {
			var ok bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, ok = i160.As160()
			}

			assert.True(b, ok, "the benchmark must measure an ID of Size160")
		})

		b.Run("As256", func(b *testing.B) {
			var ok bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, ok = i256.As256()
			}

			assert.True(b, ok, "the benchmark must measure an ID of Size256")
		})
	})
}

// testFixedType runs the cases of the methods of the fixed-size type of f.
func testFixedType[K comparable](t *testing.T, f fixedType[K]) {
	t.Helper()

	keys := prop.Bytes(prop.MinSize(f.size), prop.MaxSize(f.size)).Map(f.of)

	t.Run(f.name, func(t *testing.T) {
		t.Parallel()

		t.Run("ID", func(t *testing.T) {
			t.Parallel()

			t.Run("returns an ID of the size of the type", func(t *testing.T) {
				t.Parallel()
				prop.Equal(t, func(k K) int { return f.toID(k).Size() }, func(K) int { return f.size },
					"the ID must have the size of the type", prop.Using(keys))
			})

			t.Run("returns an ID of the bytes of k", func(t *testing.T) {
				t.Parallel()
				prop.Equal(t, func(k K) []byte { return f.toID(k).Bytes() }, f.bytesOf,
					"the ID must have the bytes of the identifier", prop.Using(keys))
			})

			t.Run("returns an ID whose binary form is the bytes of k", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "the binary form of the ID must be the bytes of the identifier", func(c *prop.Case) {
					k := c.Draw(keys, "k")
					b, err := f.toID(k).AppendBinary(nil)
					assert.NoError(c, err, "AppendBinary must encode every ID")
					assert.Equal(c, b, f.bytesOf(k), "the binary form must be the bytes of the identifier")
				})
			})

			t.Run("returns an ID that is not Zero for the zero value", func(t *testing.T) {
				t.Parallel()
				var zero K
				assert.False(t, f.toID(zero).IsZero(), "the ID of the zero value must not be Zero")
			})
		})

		t.Run("Compare", func(t *testing.T) {
			t.Parallel()

			low := f.of(bytes.Repeat([]byte{0x01}, f.size))
			high := f.of(bytes.Repeat([]byte{0x02}, f.size))
			tests := []struct {
				a, b K
				name string
				want int
			}{
				{name: "returns 0 for identical identifiers", a: low, b: low, want: 0},
				{name: "returns -1 for smaller bytes", a: low, b: high, want: -1},
				{name: "returns 1 for larger bytes", a: high, b: low, want: 1},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, f.compare(tt.a, tt.b), tt.want, "Compare must order the identifiers by their bytes")
				})
			}

			t.Run("returns what ID.Compare returns for the IDs", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Compare must order as ID.Compare orders the IDs", func(c *prop.Case) {
					a := c.Draw(keys, "a")
					b := c.Draw(keys, "b")
					assert.Equal(c, f.compare(a, b), f.toID(a).Compare(f.toID(b)),
						"Compare must return what ID.Compare returns")
				})
			})

			t.Run("returns what bytes.Compare returns for identifiers that share a prefix", func(t *testing.T) {
				t.Parallel()
				prop.ForAll(t, "Compare must agree with bytes.Compare on a common prefix", func(c *prop.Case) {
					a := c.Draw(keys, "a")
					prefix := c.Draw(prop.Integer(0, f.size), "prefix")
					rest := c.Draw(prop.Bytes(prop.MinSize(f.size-prefix), prop.MaxSize(f.size-prefix)), "rest")
					b := f.of(append(bytes.Clone(f.bytesOf(a)[:prefix]), rest...))
					assert.Equal(c, f.compare(a, b), bytes.Compare(f.bytesOf(a), f.bytesOf(b)),
						"Compare must return what bytes.Compare returns for the bytes")
				})
			})
		})

		t.Run("String", func(t *testing.T) {
			t.Parallel()

			t.Run("returns what ID.String returns for the ID of k", func(t *testing.T) {
				t.Parallel()
				prop.Equal(t, f.str, func(k K) string { return f.toID(k).String() },
					"String must return the text of the ID", prop.Using(keys))
			})
		})
	})
}

// testAs runs the cases of the method of ID that converts to the fixed-size
// type of f.
func testAs[K comparable](t *testing.T, f fixedType[K]) {
	t.Helper()

	keys := prop.Bytes(prop.MinSize(f.size), prop.MaxSize(f.size)).Map(f.of)
	others := ids.Filter(func(i id.ID) bool { return i.Size() != f.size })

	t.Run(f.as, func(t *testing.T) {
		t.Parallel()

		t.Run("returns the fixed-size form of an ID of its size", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(k K) (id.ID, error) { return f.toID(k), nil }, func(i id.ID) (K, error) {
				k, ok := f.asOf(i)
				if !ok {
					return k, id.ErrSize
				}

				return k, nil
			}, "the conversion must return the identifier that the ID was built from", prop.Using(keys))
		})

		t.Run("reports false for an ID of another size", func(t *testing.T) {
			t.Parallel()
			prop.False(t, func(i id.ID) bool {
				_, ok := f.asOf(i)

				return ok
			}, "an ID of another size must not convert", prop.Using(others))
		})

		t.Run("returns the zero value for an ID of another size", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(i id.ID) K {
				got, _ := f.asOf(i)

				return got
			}, func(id.ID) K { return *new(K) }, "a refused conversion must return the zero value", prop.Using(others))
		})

		t.Run("reports false for Zero", func(t *testing.T) {
			t.Parallel()
			_, ok := f.asOf(id.Zero)
			assert.False(t, ok, "Zero must not convert")
		})

		t.Run("returns the zero value for Zero", func(t *testing.T) {
			t.Parallel()
			got, _ := f.asOf(id.Zero)
			assert.Equal(t, got, *new(K), "a refused conversion must return the zero value")
		})
	})
}
