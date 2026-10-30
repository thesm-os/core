// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"encoding/base64"
	"math"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
	coresha512 "go.thesmos.sh/core/crypto/sha512"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog"
)

// exampleIndex, exampleExtra, exampleCheckpoint and exampleProof are the
// example of C2SP tlog-proof at 0fb2b55. It proves the record at index 73894
// with extra data, five hashes and a checkpoint with a signature of the log
// and three cosignatures. The specification shortens the signatures, and the
// codec of a proof does not read them.
const (
	exampleIndex = 73894
	exampleExtra = "YWdlLXYxLjIuMS1kYXJ3aW4tYXJtNjQudGFyLmd6kYXJ3aW4tYXJ"

	exampleCheckpoint = "example.com/fancylog\n" +
		"109482\n" +
		"sFodV/vSp5O8n9a8QpW6PRY97tfOSW5bsc2Xl/EQi08=\n" +
		"\n" +
		"— example.com/fancylog hI2DJw[...]1roloI=\n" +
		"— witness1.example mJirIklj[...]qY9v2B/5bg==\n" +
		"— witness2.example TnKKVHLX[...]xwYwrSjgow==\n" +
		"— witness3.example S4X82uH5[...]3oEcROGLFQ==\n"

	exampleProof = "c2sp.org/tlog-proof@v1\n" +
		"extra " + exampleExtra + "\n" +
		"index 73894\n" +
		"gSKyXoYZUgZ6jduWYrkDOARinOMGJveXjgMkBTcdPlQ=\n" +
		"B95lDa8R83lS8n0eG+o0buTxRKQTYFi//1U8anccXmA=\n" +
		"EKNzoDWG8LGC0Yp9o+sv3qllpMP9uHQ9B20KNL+Q1zs=\n" +
		"RoopEkOdqkYqMB4MJXrbt/hMjOxsVn0IrWjpz1ZMMes=\n" +
		"AHCioX9nLjsrse6YhjRRmk1WUEirVOLLRoOQ6vfO5vk=\n" +
		"\n" +
		exampleCheckpoint
)

// The parts of the texts of the cases of a proof.
const (
	// proofHeader is the first line of every proof.
	proofHeader = "c2sp.org/tlog-proof@v1\n"

	// shortCheckpoint is a checkpoint of the cases that a proof copies
	// verbatim.
	shortCheckpoint = "example.com/log\n1\nAAAA\n\n— example.com/log AAAA\n"

	// hashLine is the first hash line of the example, and badBits the same
	// line with a spare bit set, which canonical base64 refuses.
	hashLine = "gSKyXoYZUgZ6jduWYrkDOARinOMGJveXjgMkBTcdPlQ="
	badBits  = "gSKyXoYZUgZ6jduWYrkDOARinOMGJveXjgMkBTcdPlR="
)

// exampleHashes are the hash lines of the example, from the sibling of the
// leaf up.
var exampleHashes = []string{
	hashLine,
	"B95lDa8R83lS8n0eG+o0buTxRKQTYFi//1U8anccXmA=",
	"EKNzoDWG8LGC0Yp9o+sv3qllpMP9uHQ9B20KNL+Q1zs=",
	"RoopEkOdqkYqMB4MJXrbt/hMjOxsVn0IrWjpz1ZMMes=",
	"AHCioX9nLjsrse6YhjRRmk1WUEirVOLLRoOQ6vfO5vk=",
}

// proofs generates proofs: any index, extra data of any length, up to 64
// hashes of 32 bytes, and a checkpoint of any bytes that ends with a
// newline. A proof without extra data has a nil Extra, as UnmarshalText
// returns it into a new Proof.
var proofs = prop.Composite(func(c *prop.Case) tlog.Proof {
	var p tlog.Proof
	p.Index = c.Draw(prop.Integer[uint64](0, math.MaxUint64), "index")

	if extra := c.Draw(prop.Bytes(), "extra"); len(extra) > 0 {
		p.Extra = extra
	}

	for range c.Draw(prop.Integer(0, 64), "hashes") {
		d, err := crypto.DigestFromBytes(c.Draw(prop.Bytes(prop.MinSize(32), prop.MaxSize(32)), "hash"))
		assert.NoError(c, err, "32 bytes must be a digest")
		p.Hashes = append(p.Hashes, d)
	}

	p.Checkpoint = append(c.Draw(prop.Bytes(), "checkpoint"), '\n')

	return p
})

func TestProof(t *testing.T) {
	t.Parallel()

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the text of the example of the specification", func(t *testing.T) {
			t.Parallel()
			p := example(t)
			got, err := p.AppendText(nil)
			assert.NoError(t, err, "AppendText must accept the example")
			assert.Equal(t, string(got), exampleProof, "AppendText must write the text of the specification")
		})

		t.Run("appends the text to the bytes of dst", func(t *testing.T) {
			t.Parallel()
			p := example(t)
			got, err := p.AppendText([]byte("kept"))
			assert.NoError(t, err, "AppendText must accept the example")
			assert.Equal(t, string(got), "kept"+exampleProof, "AppendText must keep the bytes of dst")
		})

		t.Run("writes no extra line for a nil Extra", func(t *testing.T) {
			t.Parallel()
			p := tlog.Proof{Index: 7, Checkpoint: []byte(shortCheckpoint)}
			got, err := p.AppendText(nil)
			assert.NoError(t, err, "AppendText must accept the proof")
			assert.Equal(t, string(got), proofHeader+"index 7\n\n"+shortCheckpoint,
				"AppendText must write no extra line")
		})

		t.Run("writes no extra line for an empty Extra", func(t *testing.T) {
			t.Parallel()
			p := tlog.Proof{Extra: []byte{}, Checkpoint: []byte(shortCheckpoint)}
			got, err := p.AppendText(nil)
			assert.NoError(t, err, "AppendText must accept the proof")
			assert.Equal(t, string(got), proofHeader+"index 0\n\n"+shortCheckpoint,
				"AppendText must write no extra line")
		})

		sha384 := coresha512.New384().Hash([]byte("a SHA-384 digest"))
		tests := []struct {
			name string
			give tlog.Proof
		}{
			{
				name: "returns ErrProofText for a hash that is not 32 bytes",
				give: tlog.Proof{Hashes: []crypto.Digest{sha384}, Checkpoint: []byte(shortCheckpoint)},
			},
			{
				name: "returns ErrProofText for a checkpoint that does not end with a newline",
				give: tlog.Proof{Checkpoint: []byte(strings.TrimSuffix(shortCheckpoint, "\n"))},
			},
			{name: "returns ErrProofText for an empty checkpoint"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := tt.give.AppendText([]byte("kept"))
				expect.ErrorIs(t, err, tlog.ErrProofText, "AppendText must refuse the proof")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrProofText must classify as Invalid")
				expect.Equal(t, string(got), "kept", "AppendText must return dst unchanged")
			})
		}
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("sets p to the proof of the example of the specification", func(t *testing.T) {
			t.Parallel()
			var p tlog.Proof
			assert.NoError(t, p.UnmarshalText([]byte(exampleProof)), "UnmarshalText must accept the example")

			want := example(t)
			expect.Equal(t, p.Index, want.Index, "UnmarshalText must set the index")
			expect.Equal(t, p.Extra, want.Extra, "UnmarshalText must set the extra data")
			expect.Equal(t, p.Hashes, want.Hashes, "UnmarshalText must set the hashes")
			expect.Equal(t, string(p.Checkpoint), exampleCheckpoint, "UnmarshalText must set the checkpoint")
		})

		t.Run("sets no extra data for an extra line without data", func(t *testing.T) {
			t.Parallel()
			p := example(t)
			assert.NoError(t, p.UnmarshalText([]byte(proofHeader+"extra \nindex 0\n\n"+shortCheckpoint)),
				"UnmarshalText must accept an extra line without data")
			assert.Empty(t, p.Extra, "an extra line without data must leave Extra empty")
		})

		t.Run("sets no extra data for a proof without an extra line", func(t *testing.T) {
			t.Parallel()
			p := example(t)
			assert.NoError(t, p.UnmarshalText([]byte(proofHeader+"index 0\n\n"+shortCheckpoint)),
				"UnmarshalText must accept a proof without an extra line")
			assert.Empty(t, p.Extra, "a proof without an extra line must leave Extra empty")
		})

		t.Run("copies the checkpoint out of text", func(t *testing.T) {
			t.Parallel()
			text := []byte(exampleProof)
			var p tlog.Proof
			assert.NoError(t, p.UnmarshalText(text), "UnmarshalText must accept the example")

			clear(text)
			assert.Equal(t, string(p.Checkpoint), exampleCheckpoint, "the checkpoint must not alias text")
		})

		t.Run("decodes the proof into the memory of p", func(t *testing.T) {
			t.Parallel()
			p := tlog.Proof{
				Extra:      make([]byte, 0, 64),
				Hashes:     make([]crypto.Digest, 0, 8),
				Checkpoint: make([]byte, 0, 512),
			}
			extra, hashes, checkpoint := &p.Extra[:1][0], &p.Hashes[:1][0], &p.Checkpoint[:1][0]

			assert.NoError(t, p.UnmarshalText([]byte(exampleProof)), "UnmarshalText must accept the example")
			expect.Equal(t, &p.Extra[0], extra, "the extra data must be in the memory of Extra", assert.ByIdentity())
			expect.Equal(t, &p.Hashes[0], hashes, "the hashes must be in the memory of Hashes", assert.ByIdentity())
			expect.Equal(t, &p.Checkpoint[0], checkpoint, "the checkpoint must be in the memory of Checkpoint",
				assert.ByIdentity())
		})

		t.Run("empties p after an error", func(t *testing.T) {
			t.Parallel()
			p := example(t)
			hashes := cap(p.Hashes)

			assert.ErrorIs(t, p.UnmarshalText([]byte("index 0\n")), tlog.ErrProofText,
				"UnmarshalText must refuse a text without the header")
			expect.Empty(t, p.Extra, "p must have no extra data after an error")
			expect.Empty(t, p.Hashes, "p must have no hashes after an error")
			expect.Empty(t, p.Checkpoint, "p must have no checkpoint after an error")
			expect.Equal(t, p.Index, uint64(0), "p must have the index 0 after an error")
			expect.Equal(t, cap(p.Hashes), hashes, "p must keep the memory of its hashes after an error")
		})

		sha384 := base64.StdEncoding.EncodeToString(coresha512.New384().Hash([]byte("a SHA-384 digest")).Bytes())
		short := base64.StdEncoding.EncodeToString(make([]byte, 31))
		long := base64.StdEncoding.EncodeToString(make([]byte, 33))
		tests := []struct {
			name string
			give string
		}{
			{name: "returns ErrProofText for an empty text", give: ""},
			{name: "returns ErrProofText for a text without the header", give: "index 0\n\n" + shortCheckpoint},
			{
				name: "returns ErrProofText for the header of another version",
				give: "c2sp.org/tlog-proof@v2\nindex 0\n\n" + shortCheckpoint,
			},
			{
				name: "returns ErrProofText for an extra line that is not canonical base64",
				give: proofHeader + "extra YR==\nindex 0\n\n" + shortCheckpoint,
			},
			{name: "returns ErrProofText for a text without an index line", give: proofHeader + "\n" + shortCheckpoint},
			{
				name: "returns ErrProofText for an index line without its prefix",
				give: proofHeader + "73894\n\n" + shortCheckpoint,
			},
			{
				name: "returns ErrProofText for an index with a leading zero",
				give: proofHeader + "index 01\n\n" + shortCheckpoint,
			},
			{
				name: "returns ErrProofText for an index that is not a decimal",
				give: proofHeader + "index x\n\n" + shortCheckpoint,
			},
			{
				name: "returns ErrProofText for an index above 2^64 - 1",
				give: proofHeader + "index 18446744073709551616\n\n" + shortCheckpoint,
			},
			{
				name: "returns ErrProofText for a hash line that is not canonical base64",
				give: proofHeader + "index 0\n" + badBits + "\n\n" + shortCheckpoint,
			},
			{
				name: "returns ErrProofText for a hash line of 48 bytes",
				give: proofHeader + "index 0\n" + sha384 + "\n\n" + shortCheckpoint,
			},
			{
				name: "returns ErrProofText for a hash line of 31 bytes",
				give: proofHeader + "index 0\n" + short + "\n\n" + shortCheckpoint,
			},
			{
				name: "returns ErrProofText for a hash line of 33 bytes",
				give: proofHeader + "index 0\n" + long + "\n\n" + shortCheckpoint,
			},
			{
				name: "returns ErrProofText for a text without the empty line before the checkpoint",
				give: proofHeader + "index 0\n" + hashLine + "\n",
			},
			{name: "returns ErrProofText for an empty checkpoint", give: proofHeader + "index 0\n\n"},
			{
				name: "returns ErrProofText for a checkpoint that does not end with a newline",
				give: proofHeader + "index 0\n\n" + strings.TrimSuffix(shortCheckpoint, "\n"),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var p tlog.Proof
				err := p.UnmarshalText([]byte(tt.give))
				expect.ErrorIs(t, err, tlog.ErrProofText, "UnmarshalText must refuse the text")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "ErrProofText must classify as Invalid")
			})
		}
	})

	t.Run("round-trips every proof through its text", func(t *testing.T) {
		t.Parallel()
		prop.RoundTrip(t,
			func(p tlog.Proof) ([]byte, error) { return p.AppendText(nil) },
			func(text []byte) (tlog.Proof, error) {
				var p tlog.Proof
				err := p.UnmarshalText(text)

				return p, err
			},
			"the text of a proof must parse to the proof", prop.Using(proofs))
	})
}

// TestProofAllocs checks the allocation contract of AppendText and of
// UnmarshalText. MaxAllocs counts the allocations of the whole process, so
// the test does not run in parallel.
func TestProofAllocs(t *testing.T) {
	t.Run("AppendText", func(t *testing.T) {
		t.Run("into a buffer with room", func(t *testing.T) {
			p := example(t)
			buf := make([]byte, 0, len(exampleProof))

			var (
				got []byte
				err error
			)
			expect.MaxAllocs(t, func() { got, err = p.AppendText(buf[:0]) }, 0,
				"AppendText must not allocate into a buffer with room")
			assert.NoError(t, err, "the test must measure a proof that AppendText accepts")
			assert.Equal(t, string(got), exampleProof, "the test must measure the text of the example")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Run("into a Proof with room", func(t *testing.T) {
			text := []byte(exampleProof)

			var p tlog.Proof
			assert.NoError(t, p.UnmarshalText(text), "UnmarshalText must accept the example")

			var err error
			expect.MaxAllocs(t, func() { err = p.UnmarshalText(text) }, 0,
				"UnmarshalText must not allocate into a Proof with room")
			assert.NoError(t, err, "the test must measure a text that UnmarshalText accepts")
			assert.Equal(t, p.Index, uint64(exampleIndex), "the test must measure the example")
		})
	})
}

// BenchmarkProof reports the cost of AppendText into a buffer with room and
// of UnmarshalText into a Proof with room, and fails when either allocates.
func BenchmarkProof(b *testing.B) {
	b.Run("AppendText", func(b *testing.B) {
		b.Run("into a buffer with room", func(b *testing.B) {
			p := example(b)
			buf := make([]byte, 0, len(exampleProof))

			var (
				got []byte
				err error
			)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got, err = p.AppendText(buf[:0])
			}

			assert.NoError(b, err, "the benchmark must measure a proof that AppendText accepts")
			assert.Equal(b, string(got), exampleProof, "the benchmark must measure the text of the example")
		})
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		b.Run("into a Proof with room", func(b *testing.B) {
			text := []byte(exampleProof)

			var p tlog.Proof
			assert.NoError(b, p.UnmarshalText(text), "UnmarshalText must accept the example")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = p.UnmarshalText(text)
			}

			assert.NoError(b, err, "the benchmark must measure a text that UnmarshalText accepts")
			assert.Equal(b, p.Index, uint64(exampleIndex), "the benchmark must measure the example")
		})
	})
}

// example returns the proof of the example of the specification, decoded
// with the base64 of the standard library, and fails tb when a line of the
// example does not decode.
func example(tb testing.TB) tlog.Proof {
	tb.Helper()

	extra, err := base64.StdEncoding.DecodeString(exampleExtra)
	assert.NoError(tb, err, "the extra data of the example must decode")

	p := tlog.Proof{Index: exampleIndex, Extra: extra, Checkpoint: []byte(exampleCheckpoint)}
	for _, line := range exampleHashes {
		raw, err := base64.StdEncoding.DecodeString(line)
		assert.NoError(tb, err, "a hash of the example must decode")

		d, err := crypto.DigestFromBytes(raw)
		assert.NoError(tb, err, "a hash of the example must be a digest")
		p.Hashes = append(p.Hashes, d)
	}

	return p
}
