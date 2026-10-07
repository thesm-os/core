// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package tag_test

import (
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/tag"
)

// The fixture values of the cases of Tags.
const (
	// absent is a key that no generated Tags contains.
	absent = "zone"

	// duplicate labels a case whose Tags contain the key of the case more
	// than once.
	duplicate = "a key that the Tags contain twice"
)

// keys are the keys of the generated Tags. Three keys make a key that the
// Tags contain twice common.
var keys = []string{"region", "service", "tier"}

// measured is the Tags of the allocation tests and the benchmarks, with one
// Tag of each key.
var measured = tag.Tags{
	{Key: "region", Value: "eu-west-1"},
	{Key: "service", Value: "ledger"},
	{Key: "tier", Value: "prod"},
}

// The generators of the properties of Tags.
var (
	// tagOf generates a Tag of a key of keys and of any value.
	tagOf = prop.Composite(func(c *prop.Case) tag.Tag {
		return tag.Tag{Key: c.Draw(prop.SampledFrom(keys...), "key"), Value: c.Draw(prop.String(), "value")}
	})

	// tags generates Tags of tagOf, the empty Tags included.
	tags = prop.List(tagOf).Map(func(ts []tag.Tag) tag.Tags { return ts })

	// filled generates Tags of tagOf with at least one Tag.
	filled = prop.List(tagOf, prop.MinSize(1)).Map(func(ts []tag.Tag) tag.Tags { return ts })

	// lookups generates Tags and a key that they contain, contain twice or
	// lack.
	lookups = prop.Composite(func(c *prop.Case) lookup {
		return lookup{
			tags: c.Draw(tags, "tags"),
			key:  c.Draw(prop.OneOf(prop.SampledFrom(keys...), prop.Just(absent)), "key"),
		}
	})
)

// lookup is a key and the Tags in which a case looks it up.
type lookup struct {
	key  string
	tags tag.Tags
}

func TestTags(t *testing.T) {
	t.Parallel()

	t.Run("Find", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the first Tag of a key that the Tags contain", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Find must return the first Tag of a key that the Tags contain", func(c *prop.Case) {
				ts := c.Draw(filled, "tags")
				key := ts[c.Draw(prop.Integer(0, len(ts)-1), "index")].Key
				first := slices.IndexFunc(ts, func(tg tag.Tag) bool { return tg.Key == key })
				if slices.ContainsFunc(ts[first+1:], func(tg tag.Tag) bool { return tg.Key == key }) {
					c.Classify(duplicate)
				}

				got, ok := ts.Find(key)
				assert.True(c, ok, "Find must report a key that the Tags contain")
				assert.Equal(c, got, ts[first], "Find must return the first Tag of the key")
			}, prop.Require(duplicate, 0.1))
		})

		t.Run("reports false for a key that the Tags lack", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Find must report false for a key that the Tags lack", func(c *prop.Case) {
				got, ok := c.Draw(tags, "tags").Find(absent)
				assert.False(c, ok, "Find must report false for a key that the Tags lack")
				assert.Equal(c, got, tag.Tag{}, "Find must return the zero Tag for a key that the Tags lack")
			})
		})

		t.Run("reports false for nil Tags", func(t *testing.T) {
			t.Parallel()
			_, ok := tag.Tags(nil).Find(keys[0])
			assert.False(t, ok, "Find must report false for nil Tags")
		})
	})

	t.Run("Has", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether a Tag of the key is present", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(in lookup) bool { return in.tags.Has(in.key) }, func(in lookup) bool {
				return slices.ContainsFunc(in.tags, func(tg tag.Tag) bool { return tg.Key == in.key })
			}, "Has must report whether a Tag of the key is present", prop.Using(lookups))
		})
	})

	t.Run("Get", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of the Tag that Find returns", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(in lookup) string { return in.tags.Get(in.key) }, func(in lookup) string {
				found, _ := in.tags.Find(in.key)

				return found.Value
			}, "Get must return the value of the Tag that Find returns", prop.Using(lookups))
		})
	})

	t.Run("With", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the first Tag of a key that the Tags contain", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "With must replace the first Tag of a key that the Tags contain", func(c *prop.Case) {
				ts := c.Draw(filled, "tags")
				key := ts[c.Draw(prop.Integer(0, len(ts)-1), "index")].Key
				tg := tag.Tag{Key: key, Value: c.Draw(prop.String(), "value")}

				want := slices.Clone(ts)
				want[slices.IndexFunc(ts, func(old tag.Tag) bool { return old.Key == key })] = tg
				assert.Equal(c, ts.With(tg), want, "With must replace the first Tag of the key")
			})
		})

		t.Run("appends a Tag of a key that the Tags lack", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "With must append a Tag of a key that the Tags lack", func(c *prop.Case) {
				ts := c.Draw(tags, "tags")
				tg := tag.Tag{Key: absent, Value: c.Draw(prop.String(), "value")}
				assert.Equal(c, ts.With(tg), append(slices.Clone(ts), tg), "With must append the Tag")
			})
		})

		t.Run("leaves the receiver unchanged", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "With must not change the receiver", func(c *prop.Case) {
				in := c.Draw(lookups, "lookup")
				tg := tag.Tag{Key: in.key, Value: c.Draw(prop.String(), "value")}
				assert.Pure(c, func() tag.Tags { return slices.Clone(in.tags) }, func() { _ = in.tags.With(tg) },
					"With must not change the receiver")
			})
		})
	})

	t.Run("Without", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every Tag of the key", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(in lookup) tag.Tags { return in.tags.Without(in.key) }, func(in lookup) tag.Tags {
				return slices.DeleteFunc(slices.Clone(in.tags), func(tg tag.Tag) bool { return tg.Key == in.key })
			}, "Without must remove the Tags of the key alone", prop.Using(lookups))
		})

		t.Run("leaves the receiver unchanged", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, "Without must not change the receiver", func(c *prop.Case) {
				in := c.Draw(lookups, "lookup")
				assert.Pure(c, func() tag.Tags { return slices.Clone(in.tags) }, func() { _ = in.tags.Without(in.key) },
					"Without must not change the receiver")
			})
		})
	})
}

// TestTagsAllocs checks the allocation contract of each method. MaxAllocs
// counts the allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestTagsAllocs(t *testing.T) {
	t.Run("Find", func(t *testing.T) {
		t.Run("of a key that the Tags contain", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { _, got = measured.Find(measured[2].Key) }, 0, "Find must not allocate")
			assert.True(t, got, "the test must measure a key that the Tags contain")
		})

		t.Run("of a key that the Tags lack", func(t *testing.T) {
			var got bool
			expect.MaxAllocs(t, func() { _, got = measured.Find(absent) }, 0, "Find must not allocate")
			assert.False(t, got, "the test must measure a key that the Tags lack")
		})
	})

	t.Run("Has", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = measured.Has(measured[2].Key) }, 0, "Has must not allocate")
		assert.True(t, got, "the test must measure a key that the Tags contain")
	})

	t.Run("Get", func(t *testing.T) {
		var got string
		expect.MaxAllocs(t, func() { got = measured.Get(measured[2].Key) }, 0, "Get must not allocate")
		assert.Equal(t, got, measured[2].Value, "the test must measure a key that the Tags contain")
	})

	t.Run("With", func(t *testing.T) {
		t.Run("of a key that the Tags contain", func(t *testing.T) {
			var got tag.Tags
			expect.MaxAllocs(t, func() { got = measured.With(tag.Tag{Key: measured[1].Key}) }, 1,
				"With must allocate the new Tags alone")
			assert.Length(t, got, len(measured), "the test must measure a replacement")
		})

		t.Run("of a key that the Tags lack", func(t *testing.T) {
			var got tag.Tags
			expect.MaxAllocs(t, func() { got = measured.With(tag.Tag{Key: absent}) }, 1,
				"With must allocate the new Tags alone")
			assert.Length(t, got, len(measured)+1, "the test must measure an append")
		})
	})

	t.Run("Without", func(t *testing.T) {
		var got tag.Tags
		expect.MaxAllocs(t, func() { got = measured.Without(measured[1].Key) }, 1,
			"Without must allocate the new Tags alone")
		assert.Length(t, got, len(measured)-1, "the test must measure a removal")
	})
}

// BenchmarkTags reports the cost of each method, and fails above the
// allocations that their contracts state.
func BenchmarkTags(b *testing.B) {
	b.Run("Find", func(b *testing.B) {
		b.Run("of a key that the Tags contain", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, got = measured.Find(measured[2].Key)
			}

			assert.True(b, got, "the benchmark must measure a key that the Tags contain")
		})

		b.Run("of a key that the Tags lack", func(b *testing.B) {
			var got bool

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				_, got = measured.Find(absent)
			}

			assert.False(b, got, "the benchmark must measure a key that the Tags lack")
		})
	})

	b.Run("Has", func(b *testing.B) {
		var got bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = measured.Has(measured[2].Key)
		}

		assert.True(b, got, "the benchmark must measure a key that the Tags contain")
	})

	b.Run("Get", func(b *testing.B) {
		var got string

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			got = measured.Get(measured[2].Key)
		}

		assert.Equal(b, got, measured[2].Value, "the benchmark must measure a key that the Tags contain")
	})

	b.Run("With", func(b *testing.B) {
		b.Run("of a key that the Tags contain", func(b *testing.B) {
			var got tag.Tags

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				got = measured.With(tag.Tag{Key: measured[1].Key})
			}

			assert.Length(b, got, len(measured), "the benchmark must measure a replacement")
		})

		b.Run("of a key that the Tags lack", func(b *testing.B) {
			var got tag.Tags

			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			for c.Loop() {
				got = measured.With(tag.Tag{Key: absent})
			}

			assert.Length(b, got, len(measured)+1, "the benchmark must measure an append")
		})
	})

	b.Run("Without", func(b *testing.B) {
		var got tag.Tags

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got = measured.Without(measured[1].Key)
		}

		assert.Length(b, got, len(measured)-1, "the benchmark must measure a removal")
	})
}
