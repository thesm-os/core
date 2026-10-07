// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness

import (
	"crypto/rand"
	"encoding/base64"
	"math"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
)

// The proof lines and the note of the example request of tlog-witness,
// which the tests parse and write.
const (
	exampleProof1 = "PlRNCrwHpqhGrupue0L7gxbjbMiKA9temvuZZDDpkaw="
	exampleProof2 = "jrJZDmY8Y7SyJE0MWLpLozkIVMSMZcD5kvuKxPC3swk="
	exampleNote   = "example.com/behind-the-sofa\n20852163\nCsUYapGGPo4dkMgIAUqom/Xajj7h2fB2MPA3j2jxq2I=\n\n" +
		"— example.com/behind-the-sofa Az3grlgtzPICa5OS8npVmf1Myq/5IZniMp+ZJurmRDeOoRDe4URYN7u5/Zhcyv2q1gGzGku9nTo+" +
		"zyWE+xeMcTOAYQ8=\n"
	exampleRequest = "old 20852014\n" + exampleProof1 + "\n" + exampleProof2 + "\n\n" + exampleNote
)

func TestProtocolInternal(t *testing.T) {
	t.Parallel()

	t.Run("request", func(t *testing.T) {
		t.Parallel()

		t.Run("parse", func(t *testing.T) {
			t.Parallel()

			t.Run("sets the old size, the proof and the note of the example of tlog-witness", func(t *testing.T) {
				t.Parallel()
				var r request
				assert.NoError(t, r.parse([]byte(exampleRequest)), "parse must accept the example")
				assert.Equal(t, r.oldSize, uint64(20852014), "parse must read the old size")
				assert.Equal(t, r.proof, []crypto.Digest{digest(t, exampleProof1), digest(t, exampleProof2)},
					"parse must read both proof lines")
				assert.Equal(t, string(r.note), exampleNote, "parse must leave the note after the blank line")
			})

			t.Run("sets an old size of 0 and no proof", func(t *testing.T) {
				t.Parallel()
				var r request
				assert.NoError(t, r.parse([]byte("old 0\n\n"+exampleNote)), "parse must accept the old size 0")
				assert.Equal(t, r.oldSize, uint64(0), "parse must read the old size 0")
				assert.Empty(t, r.proof, "parse must read no proof")
			})

			t.Run("sets 63 proof lines", func(t *testing.T) {
				t.Parallel()
				var r request
				body := "old 1\n" + strings.Repeat(exampleProof1+"\n", maxProof) + "\n" + exampleNote
				assert.NoError(t, r.parse([]byte(body)), "parse must accept 63 proof lines")
				assert.Length(t, r.proof, maxProof, "parse must read 63 proof lines")
			})

			t.Run("sets proof hashes of 48 and 64 bytes", func(t *testing.T) {
				t.Parallel()
				var r request
				h48, h64 := randomHash(t, 48), randomHash(t, 64)
				body := "old 1\n" + base64.StdEncoding.EncodeToString(h48.Bytes()) + "\n" +
					base64.StdEncoding.EncodeToString(h64.Bytes()) + "\n\n" + exampleNote
				assert.NoError(t, r.parse([]byte(body)), "parse must accept hashes of 48 and 64 bytes")
				assert.Equal(t, r.proof, []crypto.Digest{h48, h64}, "parse must read both hashes")
			})

			t.Run("reuses the capacity of the proof", func(t *testing.T) {
				t.Parallel()
				r := request{proof: make([]crypto.Digest, 0, 2)}
				first := &r.proof[:1][0]
				assert.NoError(t, r.parse([]byte(exampleRequest)), "parse must accept the example")
				assert.Equal(t, &r.proof[0], first, "parse must append into the proof of r", assert.ByIdentity())
			})

			t.Run("leaves the note unparsed", func(t *testing.T) {
				t.Parallel()
				var r request
				assert.NoError(t, r.parse([]byte("old 5\n\nnot a note")), "parse must not parse the note")
				assert.Equal(t, string(r.note), "not a note", "parse must return the bytes after the blank line")
			})

			tests := []struct {
				name string
				give string
			}{
				{name: "returns ErrRequest for a body without a newline", give: "old 5"},
				{name: "returns ErrRequest for a first line without the old prefix", give: "size 5\n\n" + exampleNote},
				{
					name: "returns ErrRequest for a first line of a size without the old prefix",
					give: "5\n\n" + exampleNote,
				},
				{name: "returns ErrRequest for an old size with a leading zero", give: "old 05\n\n" + exampleNote},
				{name: "returns ErrRequest for an empty old size", give: "old \n\n" + exampleNote},
				{name: "returns ErrRequest for an old size that is not a decimal", give: "old 5a\n\n" + exampleNote},
				{name: "returns ErrRequest for a negative old size", give: "old -1\n\n" + exampleNote},
				{
					name: "returns ErrRequest for an old size above the largest uint64",
					give: "old 18446744073709551616\n\n" + exampleNote,
				},
				{name: "returns ErrRequest for a body without a blank line", give: "old 5\n" + exampleProof1 + "\n"},
				{
					name: "returns ErrRequest for 64 proof lines",
					give: "old 1\n" + strings.Repeat(exampleProof1+"\n", maxProof+1) + "\n" + exampleNote,
				},
				{name: "returns ErrRequest for a proof line that is not base64", give: "old 1\n!!!!\n\n" + exampleNote},
				{
					name: "returns ErrRequest for a proof line whose spare bits are not zero",
					give: "old 1\n" + exampleProof1[:42] + "x=\n\n" + exampleNote,
				},
				{
					name: "returns ErrRequest for a proof line that starts with a carriage return",
					give: "old 1\n\r" + exampleProof1 + "\n\n" + exampleNote,
				},
				{
					name: "returns ErrRequest for a proof line with a character after its padding",
					give: "old 1\n" + exampleProof1 + "A\n\n" + exampleNote,
				},
				{
					name: "returns ErrRequest for a proof line of 20 bytes",
					give: "old 1\n" + base64.StdEncoding.EncodeToString(make([]byte, 20)) + "\n\n" + exampleNote,
				},
				{
					name: "returns ErrRequest for a proof line of 66 bytes",
					give: "old 1\n" + base64.StdEncoding.EncodeToString(make([]byte, 66)) + "\n\n" + exampleNote,
				},
				{
					name: "returns ErrRequest for a proof line longer than the base64 of 66 bytes",
					give: "old 1\n" + base64.StdEncoding.EncodeToString(make([]byte, 69)) + "\n\n" + exampleNote,
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					r := request{oldSize: 7}
					err := r.parse([]byte(tt.give))
					assert.ErrorIs(t, err, ErrRequest, "parse must refuse the body")
					assert.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
					assert.Equal(t, r.oldSize, uint64(7), "parse must leave r unchanged")
				})
			}

			t.Run("returns an ErrRequest that names a body without a newline", func(t *testing.T) {
				t.Parallel()
				var r request
				err := r.parse([]byte("old 5"))
				assert.ErrorIs(t, err, ErrRequest, "parse must refuse the body")
				assert.Contains(t, err.Error(), "without an old size line", "the error must name the body")
			})
		})
	})

	t.Run("appendRequest", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the example of tlog-witness", func(t *testing.T) {
			t.Parallel()
			proof := []crypto.Digest{digest(t, exampleProof1), digest(t, exampleProof2)}
			got := appendRequest([]byte("prefix:"), 20852014, proof, []byte(exampleNote))
			assert.Equal(t, string(got), "prefix:"+exampleRequest, "appendRequest must write the example")
		})

		t.Run("appends a body that parse reads back", func(t *testing.T) {
			t.Parallel()
			proof := []crypto.Digest{randomHash(t, 32), randomHash(t, 64)}
			var r request
			assert.NoError(t, r.parse(appendRequest(nil, 0, proof, []byte(exampleNote))), "parse must read the body")
			assert.Equal(t, r.proof, proof, "the proof must round-trip")
			assert.Equal(t, string(r.note), exampleNote, "the note must round-trip")
		})
	})

	t.Run("appendSize", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the decimal and a newline", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, string(appendSize([]byte("a:"), 20852163)), "a:20852163\n",
				"appendSize must write the size and a newline")
		})

		t.Run("appends maxSizeBody bytes for the largest size", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, len(appendSize(nil, math.MaxUint64)), maxSizeBody,
				"the body of a 409 must fit an array of maxSizeBody bytes")
		})
	})

	t.Run("SizeError", func(t *testing.T) {
		t.Parallel()

		t.Run("Error", func(t *testing.T) {
			t.Parallel()

			t.Run("returns a text of maxSizeError bytes for the largest size", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, len((&SizeError{Size: math.MaxUint64}).Error()), maxSizeError,
					"the text of a SizeError must fit an array of maxSizeError bytes")
			})
		})
	})

	t.Run("parseSizeBody", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the size of a body that appendSize writes", func(t *testing.T) {
			t.Parallel()
			size, ok := parseSizeBody(appendSize(nil, 18446744073709551615))
			assert.True(t, ok, "parseSizeBody must accept the body")
			assert.Equal(t, size, uint64(18446744073709551615), "parseSizeBody must read the size")
		})

		tests := []struct {
			name string
			give string
		}{
			{name: "reports false for a body without a newline", give: "5"},
			{name: "reports false for a size with a leading zero", give: "05\n"},
			{name: "reports false for an empty size", give: "\n"},
			{name: "reports false for two newlines", give: "5\n\n"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, ok := parseSizeBody([]byte(tt.give))
				assert.False(t, ok, "parseSizeBody must refuse the body")
			})
		}
	})
}

// TestProtocolInternalAllocs checks the allocation contracts of the parse
// and the write of a request that BenchmarkProtocolInternal states.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
//
//nolint:paralleltest // see above
func TestProtocolInternalAllocs(t *testing.T) {
	t.Run("request", func(t *testing.T) {
		t.Run("parse", func(t *testing.T) {
			body := []byte(exampleRequest)
			r := request{proof: make([]crypto.Digest, 0, maxProof)}

			var err error
			expect.MaxAllocs(t, func() { err = r.parse(body) }, 0, "parse must not allocate into a request with room")
			assert.NoError(t, err, "the test must measure a body that parse accepts")
		})
	})

	t.Run("appendRequest", func(t *testing.T) {
		proof := []crypto.Digest{digest(t, exampleProof1), digest(t, exampleProof2)}
		note := []byte(exampleNote)
		buf := make([]byte, 0, len(exampleRequest))

		var got []byte
		expect.MaxAllocs(t, func() { got = appendRequest(buf[:0], 20852014, proof, note) }, 0,
			"appendRequest must not allocate into a buffer with room")
		assert.Equal(t, string(got), exampleRequest, "the test must measure the example")
	})
}

func BenchmarkProtocolInternal(b *testing.B) {
	b.Run("request", func(b *testing.B) {
		b.Run("parse", func(b *testing.B) {
			body := []byte(exampleRequest)
			r := request{proof: make([]crypto.Digest, 0, maxProof)}

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			var err error
			for c.Loop() {
				err = r.parse(body)
			}

			assert.NoError(b, err, "the benchmark must measure a body that parse accepts")
		})
	})

	b.Run("appendRequest", func(b *testing.B) {
		proof := []crypto.Digest{digest(b, exampleProof1), digest(b, exampleProof2)}
		note := []byte(exampleNote)
		buf := make([]byte, 0, len(exampleRequest))

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got []byte
		for c.Loop() {
			got = appendRequest(buf[:0], 20852014, proof, note)
		}

		assert.Equal(b, string(got), exampleRequest, "the benchmark must measure the example")
	})
}

// digest returns the hash whose base64 is line, and fails the test when
// parseHash refuses it.
func digest(tb testing.TB, line string) crypto.Digest {
	tb.Helper()

	h, ok := parseHash([]byte(line))
	assert.True(tb, ok, "parseHash must accept "+line)

	return h
}

// randomHash returns a random hash of size bytes.
func randomHash(tb testing.TB, size int) crypto.Digest {
	tb.Helper()

	raw := make([]byte, size)
	_, _ = rand.Read(raw)

	h, err := crypto.DigestFromBytes(raw)
	assert.NoError(tb, err, "DigestFromBytes must accept the size")

	return h
}
