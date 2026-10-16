// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package filter_test

import (
	"math"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/filter"
)

func TestBloom(t *testing.T) {
	t.Parallel()

	t.Run("NewBloom", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name           string
			giveKeys       int
			giveBitsPerKey float64
			wantSize       int
			wantErr        error
		}{
			{name: "returns one block for 256 bits", giveKeys: 256, giveBitsPerKey: 1, wantSize: 32},
			{name: "rounds 257 bits up to two blocks", giveKeys: 257, giveBitsPerKey: 1, wantSize: 64},
			{name: "rounds 1050 bits up to five blocks", giveKeys: 100, giveBitsPerKey: 10.5, wantSize: 160},
			{name: "returns 40 blocks for 1000 keys of 10 bits", giveKeys: 1000, giveBitsPerKey: 10, wantSize: 1280},
			{name: "accepts 256 bits per key", giveKeys: 3, giveBitsPerKey: 256, wantSize: 96},
			{name: "counts no keys as one key", giveKeys: 0, giveBitsPerKey: 10, wantSize: 32},
			{name: "counts a negative number of keys as one key", giveKeys: -5, giveBitsPerKey: 10, wantSize: 32},
			{
				name: "returns ErrBitsPerKey for NaN bits per key", giveKeys: 1000, giveBitsPerKey: math.NaN(),
				wantErr: filter.ErrBitsPerKey,
			},
			{
				name: "returns ErrBitsPerKey for 0 bits per key", giveKeys: 1000, giveBitsPerKey: 0,
				wantErr: filter.ErrBitsPerKey,
			},
			{
				name: "returns ErrBitsPerKey for negative bits per key", giveKeys: 1000, giveBitsPerKey: -1,
				wantErr: filter.ErrBitsPerKey,
			},
			{
				name: "returns ErrBitsPerKey for more than 256 bits per key", giveKeys: 1000,
				giveBitsPerKey: math.Nextafter(256, math.Inf(1)), wantErr: filter.ErrBitsPerKey,
			},
			{
				name: "returns ErrBitsPerKey for infinite bits per key", giveKeys: 1000, giveBitsPerKey: math.Inf(1),
				wantErr: filter.ErrBitsPerKey,
			},
			{
				name: "returns ErrTooLarge for more than 2^32 blocks", giveKeys: 1<<32 + 1, giveBitsPerKey: 256,
				wantErr: filter.ErrTooLarge,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				f, err := filter.NewBloom(tt.giveKeys, tt.giveBitsPerKey)
				if tt.wantErr != nil {
					assert.ErrorIs(t, err, tt.wantErr, "NewBloom refuses the arguments")
					assert.Nil(t, f, "NewBloom returns no filter with an error")

					return
				}
				assert.NoError(t, err, "NewBloom accepts the arguments")
				assert.Equal(t, f.Size(), tt.wantSize, "the filter has 32 bytes for each block")
			})
		}
	})

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("makes MayContain report true for the hash", func(t *testing.T) {
			t.Parallel()

			prop.ForAll(t, "MayContain reports true for every hash that Add added", func(c *prop.Case) {
				hs := c.Draw(sets, "hashes")
				f, err := filter.NewBloom(len(hs), 10)
				assert.NoError(c, err, "NewBloom accepts the number of keys")
				for _, h := range hs {
					f.Add(h)
				}
				for _, h := range hs {
					assert.True(c, f.MayContain(h), "MayContain reports true for a hash that Add added")
				}
			})
		})

		t.Run("leaves the zero Bloom without blocks", func(t *testing.T) {
			t.Parallel()

			var f filter.Bloom
			assert.Pure(t, f.Size, func() { f.Add(42) }, "Add does not give the zero Bloom a block")
		})

		t.Run("is linearizable with MayContain on other goroutines", func(t *testing.T) {
			t.Parallel()

			// added is the set of the indexes of the hashes of hs that Add
			// added. MayContain may report true for any hash, and reports
			// false only for a hash that no Add added.
			spec := history.Spec[uint8]{
				Initial: func() uint8 { return 0 },
				Next: func(added uint8, op history.Operation) []uint8 {
					bit := uint8(1) << op.Args[0].(int)
					if op.Name == "Add" {
						return []uint8{added | bit}
					}
					if added&bit != 0 && !op.Returned(true) {
						return nil
					}

					return []uint8{added}
				},
			}
			hs := hashes(8, 17)
			f, err := filter.NewBloom(1000, 10)
			assert.NoError(t, err, "NewBloom accepts the size")
			h := history.New()
			outcomes := history.Concurrently(4, 10*time.Second, func(client int) (any, error) {
				for i := range 64 {
					k := (client + i) % len(hs)
					if i%4 == client {
						call := h.Invoke(client, "Add", []any{k})
						f.Add(hs[k])
						call.OK(nil)

						continue
					}
					call := h.Invoke(client, "MayContain", []any{k})
					call.OK(f.MayContain(hs[k]))
				}

				return nil, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every client finishes within the time of Concurrently")
			}
			history.Linearizable(t, h, spec, "MayContain reports true for every hash that an Add before it added")
		})
	})

	t.Run("MayContain", func(t *testing.T) {
		t.Parallel()

		t.Run("reports false for every hash of an empty filter", func(t *testing.T) {
			t.Parallel()

			f, err := filter.NewBloom(1000, 10)
			assert.NoError(t, err, "NewBloom accepts the size")
			prop.ForAll(t, "an empty filter reports false for every hash", func(c *prop.Case) {
				assert.False(c, f.MayContain(c.Draw(anyHash, "hash")), "MayContain reports false for an empty filter")
			})
		})

		t.Run("reports true for every hash of the zero Bloom", func(t *testing.T) {
			t.Parallel()

			var f filter.Bloom
			prop.ForAll(t, "the zero Bloom reports true for every hash", func(c *prop.Case) {
				assert.True(c, f.MayContain(c.Draw(anyHash, "hash")), "MayContain reports true for the zero Bloom")
			})
		})

		t.Run("reports true for absent hashes at the rate of the split block model", func(t *testing.T) {
			t.Parallel()

			const keys, queries, bitsPerKey = 1 << 16, 1 << 20, 10
			f, err := filter.NewBloom(keys, bitsPerKey)
			assert.NoError(t, err, "NewBloom accepts the size")
			for _, h := range hashes(keys, 23) {
				f.Add(h)
			}
			positives := 0
			for _, h := range hashes(queries, 29) {
				if f.MayContain(h) {
					positives++
				}
			}

			// A block of i keys sets the bit of a query in each of its eight
			// 32-bit words with probability 1 - (31/32)^i, and the number of
			// keys of a block is binomial with keys trials of 1/z.
			z := float64(f.Size() / 32)
			want := 0.0
			for i := range keys + 1 {
				n, k := float64(keys), float64(i)
				ln, _ := math.Lgamma(n + 1)
				lk, _ := math.Lgamma(k + 1)
				lnk, _ := math.Lgamma(n - k + 1)
				pmf := math.Exp(ln - lk - lnk + k*math.Log(1/z) + (n-k)*math.Log1p(-1/z))
				want += pmf * math.Pow(1-math.Pow(31.0/32, k), 8)
			}
			got := float64(positives) / queries
			assert.InRange(t, got, 0.9*want, 1.1*want, "the false positives are within a tenth of the model")
		})
	})

	t.Run("MayContainBatch", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the shorter length of hs and out", func(t *testing.T) {
			t.Parallel()

			f, err := filter.NewBloom(1000, 10)
			assert.NoError(t, err, "NewBloom accepts the size")
			prop.ForAll(t, "MayContainBatch returns the shorter length", func(c *prop.Case) {
				hs := make([]uint64, c.Draw(prop.Integer(0, 40), "len(hs)"))
				out := make([]bool, c.Draw(prop.Integer(0, 40), "len(out)"))
				assert.Equal(c, f.MayContainBatch(hs, out), min(len(hs), len(out)),
					"MayContainBatch returns the shorter length")
			})
		})

		t.Run("sets each element of out to the result of MayContain", func(t *testing.T) {
			t.Parallel()

			pool := hashes(64, 31)
			f, err := filter.NewBloom(len(pool), 10)
			assert.NoError(t, err, "NewBloom accepts the size")
			for _, h := range pool[:32] {
				f.Add(h)
			}
			queries := prop.List(prop.OneOf(prop.SampledFrom(pool...), anyHash), prop.MaxSize(70))
			prop.ForAll(t, "MayContainBatch reports what MayContain reports", func(c *prop.Case) {
				hs := c.Draw(queries, "hs")
				out := make([]bool, len(hs))
				n := f.MayContainBatch(hs, out)
				for i, h := range hs[:n] {
					assert.Equal(c, out[i], f.MayContain(h), "MayContainBatch reports what MayContain reports")
				}
			})
		})

		t.Run("leaves the elements of out past the length of hs unchanged", func(t *testing.T) {
			t.Parallel()

			f, err := filter.NewBloom(1000, 10)
			assert.NoError(t, err, "NewBloom accepts the size")
			hs := hashes(20, 37)
			out := make([]bool, 40)
			for i := range out {
				out[i] = true
			}
			f.MayContainBatch(hs, out)
			for _, v := range out[len(hs):] {
				expect.True(t, v, "MayContainBatch writes no element of out past the length of hs")
			}
		})

		t.Run("reports true for every hash of the zero Bloom", func(t *testing.T) {
			t.Parallel()

			var f filter.Bloom
			hs := hashes(40, 41)
			out := make([]bool, len(hs))
			f.MayContainBatch(hs, out)
			for _, v := range out {
				expect.True(t, v, "MayContainBatch reports true for the zero Bloom")
			}
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("removes every hash", func(t *testing.T) {
			t.Parallel()

			hs := hashes(1000, 43)
			f, err := filter.NewBloom(len(hs), 10)
			assert.NoError(t, err, "NewBloom accepts the size")
			for _, h := range hs {
				f.Add(h)
			}
			f.Reset()
			for _, h := range hs {
				expect.False(t, f.MayContain(h), "MayContain reports false after Reset")
			}
		})
	})

	t.Run("Size", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 0 for the zero Bloom", func(t *testing.T) {
			t.Parallel()

			var f filter.Bloom
			assert.Equal(t, f.Size(), 0, "the zero Bloom has no blocks")
		})
	})
}

func TestBloomAllocs(t *testing.T) {
	hs := hashes(1<<12, 47)
	f, err := filter.NewBloom(len(hs), 10)
	assert.NoError(t, err, "NewBloom accepts the size")
	for _, h := range hs {
		f.Add(h)
	}

	t.Run("Add", func(t *testing.T) {
		i := 0
		expect.MaxAllocs(t, func() {
			f.Add(hs[i%len(hs)])
			i++
		}, 0, "Add does not allocate")
	})

	t.Run("MayContain", func(t *testing.T) {
		var got bool
		expect.MaxAllocs(t, func() { got = f.MayContain(hs[0]) }, 0, "MayContain does not allocate")
		assert.True(t, got, "the test measures a hash that the filter contains")
	})

	t.Run("MayContainBatch", func(t *testing.T) {
		out := make([]bool, 64)
		var got int
		expect.MaxAllocs(t, func() { got = f.MayContainBatch(hs[:64], out) }, 0, "MayContainBatch does not allocate")
		assert.Equal(t, got, 64, "the test measures a batch of 64 hashes")
	})

	t.Run("Reset", func(t *testing.T) {
		g, err := filter.NewBloom(1000, 10)
		assert.NoError(t, err, "NewBloom accepts the size")
		expect.MaxAllocs(t, g.Reset, 0, "Reset does not allocate")
	})

	t.Run("Size", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got = f.Size() }, 0, "Size does not allocate")
		assert.Equal(t, got, 160*32, "the test measures a filter of 160 blocks")
	})
}

func BenchmarkBloom(b *testing.B) {
	hs := hashes(1<<20, 53)
	f, err := filter.NewBloom(len(hs), 10)
	assert.NoError(b, err, "NewBloom accepts the size")
	for _, h := range hs {
		f.Add(h)
	}

	b.Run("Add", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		i := 0
		for c.Loop() {
			f.Add(hs[i&(len(hs)-1)])
			i++
		}

		assert.True(b, f.MayContain(hs[0]), "the benchmark adds hashes that the filter keeps")
	})

	b.Run("MayContain", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got bool
		i := 0
		for c.Loop() {
			got = f.MayContain(hs[i&(len(hs)-1)])
			i++
		}

		assert.True(b, got, "the benchmark measures hashes that the filter contains")
	})

	b.Run("MayContainBatch", func(b *testing.B) {
		out := make([]bool, 64)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got int
		i := 0
		for c.Loop() {
			off := (i * 64) & (len(hs) - 1)
			got = f.MayContainBatch(hs[off:off+64], out)
			i++
		}

		assert.Equal(b, got, 64, "the benchmark measures batches of 64 hashes")
	})

	b.Run("Reset", func(b *testing.B) {
		g, err := filter.NewBloom(1000, 10)
		assert.NoError(b, err, "NewBloom accepts the size")

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			g.Reset()
		}

		assert.False(b, g.MayContain(hs[0]), "the benchmark resets the filter")
	})

	b.Run("Size", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got int
		for c.Loop() {
			got = f.Size()
		}

		assert.Equal(b, got, 1<<20*10/256*32, "the benchmark measures a filter of 10 bits per key")
	})
}
