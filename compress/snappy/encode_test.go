// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package snappy_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/golden"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/compress/snappy"
)

// childEnv is set in the environment of the child process in which a case
// runs alone.
const childEnv = "CORE_TEST_CHILD"

// corpusDigest is the SHA-256 of the encodings of the corpus by
// golang/snappy v1.0.0, each after its length as 8 bytes big-endian.
const corpusDigest = "328a0c6da2791a0c2e58965321c58fcdbf7fb86f5b441fd333ab2c12dc4dba52"

// sources generates the inputs of the encoder, of up to 140,000 bytes, so a
// source can span three blocks of the encoder. A period of 0 gives random
// bytes, a period of 1 a run of one byte, and a longer period the repetition
// of that many random bytes.
var sources = prop.Composite(func(c *prop.Case) []byte {
	n := c.Draw(prop.Integer(0, 140_000), "length")
	period := c.Draw(prop.Integer(0, 64), "period")
	r := rand.New(rand.NewPCG(c.Draw(prop.Integer[uint64](0, math.MaxUint64), "seed"), 0))
	b := make([]byte, n)
	for i := range b {
		if period == 0 || i < period {
			b[i] = byte(r.Uint32())
		} else {
			b[i] = b[i-period]
		}
	}

	return b
})

// boundaries are sources whose lengths lie on and around the boundaries of
// the blocks of the encoder.
var boundaries = [][]byte{
	make([]byte, 65535), make([]byte, 65536), make([]byte, 65537), make([]byte, 131072), make([]byte, 131073),
}

func TestEncode(t *testing.T) {
	t.Parallel()

	t.Run("MaxEncodedLen", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			giveN   int
			wantLen int
			wantErr error
		}{
			{name: "returns 32 for no bytes", giveN: 0, wantLen: 32},
			{name: "returns 39 for 6 bytes", giveN: 6, wantLen: 39},
			{name: "returns 76490 for 65536 bytes", giveN: 65536, wantLen: 76490},
			{name: "returns 5010795209 for MaxBlockLen bytes", giveN: snappy.MaxBlockLen, wantLen: 5010795209},
			{name: "returns ErrTooLarge for a negative length", giveN: -1, wantErr: snappy.ErrTooLarge},
			{
				name: "returns ErrTooLarge for more than MaxBlockLen bytes", giveN: snappy.MaxBlockLen + 1,
				wantErr: snappy.ErrTooLarge,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := snappy.MaxEncodedLen(tt.giveN)
				if tt.wantErr != nil {
					assert.ErrorIs(t, err, tt.wantErr, "MaxEncodedLen refuses the length")

					return
				}
				assert.NoError(t, err, "MaxEncodedLen accepts the length")
				assert.Equal(t, got, tt.wantLen, "MaxEncodedLen returns 32 + n + n/6")
			})
		}
	})

	t.Run("AppendEncode", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the bytes of golang/snappy for the Opticks", func(t *testing.T) {
			t.Parallel()

			text, want := opticks(t)
			got, err := snappy.AppendEncode(nil, text)
			assert.NoError(t, err, "AppendEncode encodes the text")
			assert.Equal(t, got, want, "AppendEncode writes the bytes of golang/snappy")
		})

		tests := []struct {
			name     string
			giveLen  int
			wantFile string
		}{
			{
				name:    "writes the bytes of golang/snappy for 17 bytes of the Opticks",
				giveLen: 17, wantFile: "opticks-17.rawsnappy",
			},
			{
				name:    "writes the bytes of golang/snappy for 100 bytes of the Opticks",
				giveLen: 100, wantFile: "opticks-100.rawsnappy",
			},
			{
				name:    "writes the bytes of golang/snappy for 1000 bytes of the Opticks",
				giveLen: 1000, wantFile: "opticks-1000.rawsnappy",
			},
			{
				name:    "writes the bytes of golang/snappy for 4096 bytes of the Opticks",
				giveLen: 4096, wantFile: "opticks-4096.rawsnappy",
			},
			{
				name:    "writes the bytes of golang/snappy for 65537 bytes of the repeated Opticks",
				giveLen: 65537, wantFile: "opticks-65537.rawsnappy",
			},
			{
				name:    "writes the bytes of golang/snappy for 70000 bytes of the repeated Opticks",
				giveLen: 70000, wantFile: "opticks-70000.rawsnappy",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				text, _ := opticks(t)
				got, err := snappy.AppendEncode(nil, bytes.Repeat(text, tt.giveLen/len(text)+1)[:tt.giveLen])
				assert.NoError(t, err, "AppendEncode encodes the text")
				// The golden files are the encodings of golang/snappy, so -update
				// does not rewrite them.
				golden.Match(t, tt.wantFile, got, false)
			})
		}

		// The digest is the SHA-256 of the encodings of the corpus by
		// golang/snappy v1.0.0, each after its length as 8 bytes big-endian.
		t.Run("writes the bytes of golang/snappy for the corpus", func(t *testing.T) {
			t.Parallel()

			var all []byte
			for _, src := range corpus() {
				enc, err := snappy.AppendEncode(nil, src)
				assert.NoError(t, err, "AppendEncode encodes the source")
				all = binary.BigEndian.AppendUint64(all, uint64(len(enc)))
				all = append(all, enc...)
			}
			sum := sha256.Sum256(all)
			assert.Equal(t, hex.EncodeToString(sum[:]), corpusDigest, "AppendEncode writes the bytes of golang/snappy")
		})

		// The source repeats its first 32 random bytes up to position 85, the
		// search limit of 100 bytes. Its next 4 bytes repeat the bytes at
		// position 5. golang/snappy v1.0.0 emits a literal of 32 bytes, a copy
		// of 53 bytes that ends at the limit, and a literal of the last 15
		// bytes.
		t.Run("writes the bytes of golang/snappy for a match that ends at the search limit", func(t *testing.T) {
			t.Parallel()

			r := rand.New(rand.NewPCG(85, 0))
			src := make([]byte, 100)
			for i := range src {
				if i < 32 || i >= 89 {
					src[i] = byte(r.Uint32())
				} else if i < 85 {
					src[i] = src[i-32]
				} else {
					src[i] = src[i-80]
				}
			}
			want, err := hex.DecodeString("647ca505282939145a1614b4e1bd39b355be9860e5a64ca19e867243ee892e7dc5b6" +
				"d2200038145a1614a00189b5417fd0974d11f6")
			assert.NoError(t, err, "the encoding of golang/snappy decodes from hex")
			got, err := snappy.AppendEncode(nil, src)
			assert.NoError(t, err, "AppendEncode encodes the source")
			assert.Equal(t, got, want, "AppendEncode writes the bytes of golang/snappy")
		})

		t.Run("appends the encoding to dst", func(t *testing.T) {
			t.Parallel()

			text, want := opticks(t)
			got, err := snappy.AppendEncode([]byte("prefix"), text)
			assert.NoError(t, err, "AppendEncode encodes the text")
			assert.Equal(t, got, append([]byte("prefix"), want...), "AppendEncode keeps the bytes of dst")
		})

		t.Run("encodes no bytes as a block of length 0", func(t *testing.T) {
			t.Parallel()

			got, err := snappy.AppendEncode(nil, nil)
			assert.NoError(t, err, "AppendEncode encodes no bytes")
			assert.Equal(t, got, []byte{0}, "the block of no bytes is its preamble of 0")
		})

		t.Run("returns ErrTooLarge for more than MaxBlockLen bytes", func(t *testing.T) {
			t.Parallel()

			// The source takes 4 GiB of address space. The runtime zeroes a large
			// allocation that reuses pages that a collection freed, and so
			// touches every page. The case therefore runs alone in a child
			// process of the test binary, whose heap has freed no pages yet.
			if os.Getenv(childEnv) == "" {
				exe, err := os.Executable()
				assert.NoError(t, err, "the test binary has a path")
				levels := strings.Split(t.Name(), "/")
				for i, level := range levels {
					levels[i] = "^" + regexp.QuoteMeta(level) + "$"
				}
				args := []string{"-test.run=" + strings.Join(levels, "/"), "-test.count=1"}
				if dir := flag.Lookup("test.gocoverdir"); dir != nil && dir.Value.String() != "" {
					args = append(args, "-test.gocoverdir="+dir.Value.String())
				}
				if deadline, ok := t.Deadline(); ok {
					args = append(args, "-test.timeout="+time.Until(deadline).String())
				}
				cmd := exec.CommandContext(t.Context(), exe, args...)
				cmd.Env = append(os.Environ(), childEnv+"=1")
				out, err := cmd.CombinedOutput()
				assert.NoError(t, err, "the case passes in a child process, whose output is:\n"+string(out))

				return
			}
			src := make([]byte, snappy.MaxBlockLen+1)
			dst := []byte("prefix")
			got, err := snappy.AppendEncode(dst, src)
			assert.ErrorIs(t, err, snappy.ErrTooLarge, "AppendEncode refuses a source beyond the format")
			assert.Equal(t, got, dst, "AppendEncode returns dst unchanged", assert.ByIdentity())
		})

		t.Run("writes at most MaxEncodedLen bytes", func(t *testing.T) {
			t.Parallel()

			prop.ForAll(t, "an encoding has at most MaxEncodedLen bytes", func(c *prop.Case) {
				src := c.Draw(sources, "source")
				got, err := snappy.AppendEncode(nil, src)
				assert.NoError(c, err, "AppendEncode encodes the source")
				bound, err := snappy.MaxEncodedLen(len(src))
				assert.NoError(c, err, "MaxEncodedLen accepts the length")
				assert.InRange(c, len(got), 0, float64(bound), "the encoding has at most MaxEncodedLen bytes")
			})
		})

		t.Run("writes a block that AppendDecode decodes to the source", func(t *testing.T) {
			t.Parallel()

			prop.RoundTrip(t,
				func(src []byte) ([]byte, error) { return snappy.AppendEncode(nil, src) },
				func(enc []byte) ([]byte, error) { return snappy.AppendDecode(nil, enc) },
				"AppendDecode undoes AppendEncode", prop.Using(sources), prop.Examples(boundaries...),
				assert.EquateEmpty())
		})
	})
}

func TestEncodeAllocs(t *testing.T) {
	src := leveldbBlock(32<<10, 1)

	t.Run("AppendEncode", func(t *testing.T) {
		bound, err := snappy.MaxEncodedLen(len(src))
		assert.NoError(t, err, "MaxEncodedLen accepts the length")
		dst := make([]byte, 0, bound)

		var got []byte
		expect.MaxAllocs(t, func() { got, err = snappy.AppendEncode(dst, src) }, 0,
			"AppendEncode does not allocate into a dst with room for MaxEncodedLen bytes")
		assert.NoError(t, err, "the test measures an encoding that succeeds")
		back, err := snappy.AppendDecode(nil, got)
		assert.NoError(t, err, "the encoding decodes")
		assert.Equal(t, back, src, "the test measures an encoding of the block")
	})

	t.Run("MaxEncodedLen", func(t *testing.T) {
		var got int
		expect.MaxAllocs(t, func() { got, _ = snappy.MaxEncodedLen(len(src)) }, 0, "MaxEncodedLen does not allocate")
		assert.Equal(t, got, 32+len(src)+len(src)/6, "the test measures the bound of the block")
	})
}

func BenchmarkEncode(b *testing.B) {
	b.Run("AppendEncode", func(b *testing.B) {
		for _, size := range []int{4 << 10, 32 << 10} {
			b.Run(strconv.Itoa(size), func(b *testing.B) {
				src := leveldbBlock(size, 2)
				bound, err := snappy.MaxEncodedLen(len(src))
				assert.NoError(b, err, "MaxEncodedLen accepts the length")
				dst := make([]byte, 0, bound)
				b.SetBytes(int64(len(src)))

				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				var got []byte
				for c.Loop() {
					got, err = snappy.AppendEncode(dst, src)
				}

				assert.NoError(b, err, "the benchmark measures encodings that succeed")
				assert.NotEmpty(b, got, "the benchmark measures encodings of the block")
			})
		}
	})

	b.Run("MaxEncodedLen", func(b *testing.B) {
		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got int
		for c.Loop() {
			got, _ = snappy.MaxEncodedLen(32 << 10)
		}

		assert.Equal(b, got, 32+32<<10+32<<10/6, "the benchmark measures the bound of 32 KiB")
	})
}
