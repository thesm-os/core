// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package xxh3_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/hash/xxh3"
	"go.thesmos.sh/core/id"
)

// allocLengths contains one input length for each path of XXH3: up to 16
// bytes, 17 to 128 bytes, 129 to 240 bytes, and longer inputs.
var allocLengths = []struct {
	name  string
	giveN int
}{
	{name: "allocates nothing for 16 bytes", giveN: 16},
	{name: "allocates nothing for 128 bytes", giveN: 128},
	{name: "allocates nothing for 240 bytes", giveN: 240},
	{name: "allocates nothing for 4160 bytes", giveN: 4160},
}

func TestHash(t *testing.T) {
	t.Parallel()

	buf, digests := reference(t)

	t.Run("Hash", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			giveMin int
			giveMax int
		}{
			{name: "returns the digest of xxHash for each prefix of up to 16 bytes", giveMin: 0, giveMax: 16},
			{name: "returns the digest of xxHash for each prefix of 17 to 128 bytes", giveMin: 17, giveMax: 128},
			{name: "returns the digest of xxHash for each prefix of 129 to 240 bytes", giveMin: 129, giveMax: 240},
			{name: "returns the digest of xxHash for each prefix of 241 to 4160 bytes", giveMin: 241, giveMax: 4160},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				for n := tt.giveMin; n <= tt.giveMax; n++ {
					expect.Equal(t, xxh3.Hash(buf[:n]), digests[n],
						"Hash returns the digest of xxHash for the first "+strconv.Itoa(n)+" bytes")
				}
			})
		}
	})

	t.Run("HashString", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the digest that Hash returns for the bytes of s", func(t *testing.T) {
			t.Parallel()

			inputs := prop.Integer(0, len(buf)-1).Bind(func(n int) prop.Generator[[]byte] {
				return prop.Bytes(prop.MinSize(n), prop.MaxSize(n))
			})
			prop.Equal(t, func(b []byte) uint64 { return xxh3.HashString(string(b)) }, xxh3.Hash,
				"HashString returns the digest that Hash returns for the same bytes", prop.Using(inputs))
		})
	})
}

func TestHashAllocs(t *testing.T) {
	buf, digests := reference(t)

	t.Run("Hash", func(t *testing.T) {
		for _, tt := range allocLengths {
			t.Run(tt.name, func(t *testing.T) {
				b := buf[:tt.giveN]

				var got uint64
				expect.MaxAllocs(t, func() { got = xxh3.Hash(b) }, 0, "Hash does not allocate")
				assert.Equal(t, got, digests[tt.giveN], "the test measures the digest of xxHash")
			})
		}

		tests := []struct {
			name string
			give id.ID
		}{
			{name: "allocates nothing for the bytes of an id.ID of 128 bits", give: id.New128([id.Size128]byte(buf))},
			{name: "allocates nothing for the bytes of an id.ID of 160 bits", give: id.New160([id.Size160]byte(buf))},
			{name: "allocates nothing for the bytes of an id.ID of 256 bits", give: id.New256([id.Size256]byte(buf))},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				x := tt.give

				var got uint64
				expect.MaxAllocs(t, func() { got = xxh3.Hash(x.Bytes()) }, 0,
					"Hash does not allocate for the bytes that ID.Bytes returns")
				assert.Equal(t, got, digests[x.Size()], "the test measures the digest of xxHash")
			})
		}
	})

	t.Run("HashString", func(t *testing.T) {
		for _, tt := range allocLengths {
			t.Run(tt.name, func(t *testing.T) {
				s := string(buf[:tt.giveN])

				var got uint64
				expect.MaxAllocs(t, func() { got = xxh3.HashString(s) }, 0, "HashString does not allocate")
				assert.Equal(t, got, digests[tt.giveN], "the test measures the digest of xxHash")
			})
		}
	})
}

func BenchmarkHash(b *testing.B) {
	buf, digests := reference(b)
	lengths := []int{16, 20, 32, 128, 240, 4096}

	b.Run("Hash", func(b *testing.B) {
		for _, n := range lengths {
			b.Run(strconv.Itoa(n), func(b *testing.B) {
				in := buf[:n]
				b.SetBytes(int64(n))

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				var got uint64
				for c.Loop() {
					got = xxh3.Hash(in)
				}

				assert.Equal(b, got, digests[n], "the benchmark measures the digest of xxHash")
			})
		}
	})

	b.Run("HashString", func(b *testing.B) {
		for _, n := range lengths {
			b.Run(strconv.Itoa(n), func(b *testing.B) {
				in := string(buf[:n])
				b.SetBytes(int64(n))

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				var got uint64
				for c.Loop() {
					got = xxh3.HashString(in)
				}

				assert.Equal(b, got, digests[n], "the benchmark measures the digest of xxHash")
			})
		}
	})
}

// reference returns the sanity buffer of xxHash's tests and the digests of its
// prefixes that testdata/vectors.txt lists. digests[n] is the digest of the
// first n bytes of the buffer.
//
// The sanity test of xxHash fills a buffer of 4,161 bytes. It starts a 64-bit
// value at 2654435761 and multiplies it by 11400714785074694797 after each
// byte, and each byte is the high byte of the value.
func reference(tb testing.TB) (buf []byte, digests []uint64) {
	tb.Helper()

	buf = make([]byte, 4096+64+1)
	gen := uint64(2654435761)
	for i := range buf {
		buf[i] = byte(gen >> 56)
		gen *= 11400714785074694797
	}

	f, err := os.Open(filepath.Join("testdata", "vectors.txt"))
	assert.NoError(tb, err, "the vectors open")
	defer f.Close()

	lines := bufio.NewScanner(f)
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		if len(fields) == 0 || fields[0] == "#" {
			continue
		}

		n, err := strconv.Atoi(fields[0])
		assert.NoError(tb, err, "a vector starts with the length of its prefix")
		assert.Equal(tb, n, len(digests), "the vectors are in the order of their lengths")

		digest, err := strconv.ParseUint(fields[1], 16, 64)
		assert.NoError(tb, err, "a vector ends with its digest in hexadecimal")
		digests = append(digests, digest)
	}
	assert.NoError(tb, lines.Err(), "the vectors read")
	assert.Length(tb, digests, len(buf), "the vectors list a digest for each prefix of the buffer")

	return buf, digests
}
