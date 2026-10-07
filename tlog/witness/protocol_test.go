// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package witness_test

import (
	"encoding/base64"
	"fmt"
	"math"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/tlog/witness"
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

func TestProtocol(t *testing.T) {
	t.Parallel()

	t.Run("SizeError", func(t *testing.T) {
		t.Parallel()

		t.Run("Error", func(t *testing.T) {
			t.Parallel()

			t.Run("returns the committed size after the prefix of the package", func(t *testing.T) {
				t.Parallel()
				err := &witness.SizeError{Size: math.MaxUint64}
				assert.Equal(t, err.Error(), "witness: the witness committed the size 18446744073709551615 last",
					"Error must contain the committed size")
				assert.HasPrefix(t, err.Error(), errorPrefix, "Error must start with the prefix")
			})
		})

		t.Run("Class", func(t *testing.T) {
			t.Parallel()

			t.Run("returns Conflict", func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, (&witness.SizeError{Size: 5}).Class(), errs.Conflict,
					"a SizeError must classify as Conflict")
			})

			t.Run("returns Conflict through a wrap", func(t *testing.T) {
				t.Parallel()
				err := fmt.Errorf("context: %w", &witness.SizeError{Size: 5})
				assert.Equal(t, errs.Classify(err), errs.Conflict, "a wrapped SizeError must classify as Conflict")
				got := assert.ErrorAs[*witness.SizeError](t, err, "errors.AsType must find the SizeError")
				assert.Equal(t, got.Size, uint64(5), "the SizeError must keep its size")
			})
		})
	})

	t.Run("ParseRequest", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the old size, the proof and the note of the example of tlog-witness", func(t *testing.T) {
			t.Parallel()
			oldSize, hashes, rest, err := witness.ParseRequest([]byte(exampleRequest), nil)
			assert.NoError(t, err, "ParseRequest must accept the example")
			expect.Equal(t, oldSize, uint64(20852014), "ParseRequest must return the old size")
			expect.Equal(t, hashes, []crypto.Digest{digest(t, exampleProof1), digest(t, exampleProof2)},
				"ParseRequest must return both proof lines")
			expect.Equal(t, string(rest), exampleNote, "ParseRequest must return the note after the blank line")
		})

		t.Run("returns an old size of 0 and no hashes for a body without proof lines", func(t *testing.T) {
			t.Parallel()
			oldSize, hashes, _, err := witness.ParseRequest([]byte("old 0\n\n"+exampleNote), nil)
			assert.NoError(t, err, "ParseRequest must accept the old size 0")
			expect.Equal(t, oldSize, uint64(0), "ParseRequest must return the old size 0")
			expect.Empty(t, hashes, "ParseRequest must return no hashes")
		})

		t.Run("returns the bytes after the blank line unparsed", func(t *testing.T) {
			t.Parallel()
			_, _, rest, err := witness.ParseRequest([]byte("old 5\n\nnot a note"), nil)
			assert.NoError(t, err, "ParseRequest must not parse the note")
			assert.Equal(t, string(rest), "not a note", "ParseRequest must return the bytes after the blank line")
		})

		t.Run("returns hashes of 48 and 64 bytes", func(t *testing.T) {
			t.Parallel()
			h48 := crypto.NewDigest384([48]byte{1})
			h64 := crypto.NewDigest512([64]byte{2})
			body := "old 1\n" + base64.StdEncoding.EncodeToString(h48.Bytes()) + "\n" +
				base64.StdEncoding.EncodeToString(h64.Bytes()) + "\n\n" + exampleNote
			_, hashes, _, err := witness.ParseRequest([]byte(body), nil)
			assert.NoError(t, err, "ParseRequest must accept hashes of 48 and 64 bytes")
			assert.Equal(t, hashes, []crypto.Digest{h48, h64}, "ParseRequest must return both hashes")
		})

		t.Run("appends the hashes to proof", func(t *testing.T) {
			t.Parallel()
			proof := []crypto.Digest{digest(t, exampleProof2)}
			_, hashes, _, err := witness.ParseRequest([]byte(exampleRequest), proof)
			assert.NoError(t, err, "ParseRequest must accept the example")
			assert.Equal(t, hashes,
				[]crypto.Digest{digest(t, exampleProof2), digest(t, exampleProof1), digest(t, exampleProof2)},
				"ParseRequest must append the hashes after those of proof")
		})

		t.Run("appends the hashes into the capacity of proof", func(t *testing.T) {
			t.Parallel()
			proof := make([]crypto.Digest, 0, 2)
			_, hashes, _, err := witness.ParseRequest([]byte(exampleRequest), proof)
			assert.NoError(t, err, "ParseRequest must accept the example")
			assert.Equal(t, &hashes[0], &proof[:1][0], "ParseRequest must append into the array of proof",
				assert.ByIdentity())
		})

		t.Run("returns 63 hashes after the hashes of proof", func(t *testing.T) {
			t.Parallel()
			proof := make([]crypto.Digest, 5)
			body := "old 1\n" + strings.Repeat(exampleProof1+"\n", 63) + "\n" + exampleNote
			_, hashes, _, err := witness.ParseRequest([]byte(body), proof)
			assert.NoError(t, err, "ParseRequest must accept 63 proof lines")
			assert.Length(t, hashes, 68, "ParseRequest must append the 63 hashes to the 5 of proof")
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
				give: "old 1\n" + strings.Repeat(exampleProof1+"\n", 64) + "\n" + exampleNote,
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
				proof := make([]crypto.Digest, 1, 64)
				oldSize, hashes, rest, err := witness.ParseRequest([]byte(tt.give), proof)
				assert.ErrorIs(t, err, witness.ErrRequest, "ParseRequest must refuse the body")
				expect.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				expect.Equal(t, oldSize, uint64(0), "ParseRequest must return no old size")
				expect.Equal(t, hashes, proof, "ParseRequest must return proof unchanged", assert.ByIdentity())
				expect.Nil(t, rest, "ParseRequest must return no rest")
			})
		}

		t.Run("returns an ErrRequest that names a body without a newline", func(t *testing.T) {
			t.Parallel()
			_, _, _, err := witness.ParseRequest([]byte("old 5"), nil)
			assert.ErrorIs(t, err, witness.ErrRequest, "ParseRequest must refuse the body")
			assert.Contains(t, err.Error(), "without an old size line", "the error must name the body")
		})
	})

	t.Run("AppendRequest", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the example of tlog-witness", func(t *testing.T) {
			t.Parallel()
			proof := []crypto.Digest{digest(t, exampleProof1), digest(t, exampleProof2)}
			got := witness.AppendRequest([]byte("prefix:"), 20852014, proof, []byte(exampleNote))
			assert.Equal(t, string(got), "prefix:"+exampleRequest, "AppendRequest must write the example")
		})

		t.Run("appends a body from which ParseRequest returns the old size, the proof and the message",
			func(t *testing.T) {
				t.Parallel()

				// request is the old size, the proof and the message of a body.
				type request struct {
					oldSize uint64
					proof   []crypto.Digest
					msg     []byte
				}

				requests := prop.Composite(func(c *prop.Case) request {
					r := request{
						oldSize: c.Draw(prop.Integer[uint64](0, math.MaxUint64), "old size"),
						proof:   make([]crypto.Digest, c.Draw(prop.Integer(0, 63), "proof length")),
					}

					for i := range r.proof {
						size := c.Draw(prop.SampledFrom(32, 48, 64), "hash size")
						raw := c.Draw(prop.Bytes(prop.MinSize(size), prop.MaxSize(size)), "hash")
						h, err := crypto.DigestFromBytes(raw)
						assert.NoError(c, err, "DigestFromBytes must accept the size")
						r.proof[i] = h
					}

					r.msg = c.Draw(prop.Bytes(), "message")

					return r
				})
				prop.RoundTrip(t, func(r request) ([]byte, error) {
					return witness.AppendRequest(nil, r.oldSize, r.proof, r.msg), nil
				}, func(body []byte) (request, error) {
					oldSize, proof, msg, err := witness.ParseRequest(body, nil)

					return request{oldSize: oldSize, proof: proof, msg: msg}, err
				}, "ParseRequest must return what AppendRequest wrote", prop.Using(requests), assert.EquateEmpty())
			})
	})
}

// TestProtocolAllocs checks the allocation contracts of the functions of
// the protocol and of the methods of SizeError that BenchmarkProtocol
// states. MaxAllocs counts the allocations of the whole process, so the
// test does not run in parallel.
//
//nolint:paralleltest // see above
func TestProtocolAllocs(t *testing.T) {
	t.Run("SizeError", func(t *testing.T) {
		err := &witness.SizeError{Size: 20852014}

		t.Run("Error", func(t *testing.T) {
			var got string
			expect.MaxAllocs(t, func() { got = err.Error() }, 1, "Error must allocate its text alone")
			assert.HasSuffix(t, got, " 20852014 last", "the test must measure the text")
		})

		t.Run("Class", func(t *testing.T) {
			var got errs.Class
			expect.MaxAllocs(t, func() { got = err.Class() }, 0, "Class must not allocate")
			assert.Equal(t, got, errs.Conflict, "the test must measure the class")
		})
	})

	t.Run("ParseRequest", func(t *testing.T) {
		body := []byte(exampleRequest)
		proof := make([]crypto.Digest, 0, 63)

		var (
			hashes []crypto.Digest
			err    error
		)

		expect.MaxAllocs(t, func() { _, hashes, _, err = witness.ParseRequest(body, proof) }, 0,
			"ParseRequest must not allocate into a proof with room")
		assert.NoError(t, err, "the test must measure a body that ParseRequest accepts")
		assert.Length(t, hashes, 2, "the test must measure both proof lines")
	})

	t.Run("AppendRequest", func(t *testing.T) {
		proof := []crypto.Digest{digest(t, exampleProof1), digest(t, exampleProof2)}
		note := []byte(exampleNote)
		buf := make([]byte, 0, len(exampleRequest))

		var got []byte
		expect.MaxAllocs(t, func() { got = witness.AppendRequest(buf[:0], 20852014, proof, note) }, 0,
			"AppendRequest must not allocate into a buffer with room")
		assert.Equal(t, string(got), exampleRequest, "the test must measure the example")
	})
}

func BenchmarkProtocol(b *testing.B) {
	b.Run("SizeError", func(b *testing.B) {
		err := &witness.SizeError{Size: 20852014}

		b.Run("Error", func(b *testing.B) {
			c := bench.Start(b).MaxAllocs(1)
			defer c.End()

			var got string
			for c.Loop() {
				got = err.Error()
			}

			assert.HasSuffix(b, got, " 20852014 last", "the benchmark must measure the text")
		})

		b.Run("Class", func(b *testing.B) {
			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			var got errs.Class
			for c.Loop() {
				got = err.Class()
			}

			assert.Equal(b, got, errs.Conflict, "the benchmark must measure the class")
		})
	})

	b.Run("ParseRequest", func(b *testing.B) {
		body := []byte(exampleRequest)
		proof := make([]crypto.Digest, 0, 63)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var (
			hashes []crypto.Digest
			err    error
		)

		for c.Loop() {
			_, hashes, _, err = witness.ParseRequest(body, proof)
		}

		assert.NoError(b, err, "the benchmark must measure a body that ParseRequest accepts")
		assert.Length(b, hashes, 2, "the benchmark must measure both proof lines")
	})

	b.Run("AppendRequest", func(b *testing.B) {
		proof := []crypto.Digest{digest(b, exampleProof1), digest(b, exampleProof2)}
		note := []byte(exampleNote)
		buf := make([]byte, 0, len(exampleRequest))

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		var got []byte
		for c.Loop() {
			got = witness.AppendRequest(buf[:0], 20852014, proof, note)
		}

		assert.Equal(b, string(got), exampleRequest, "the benchmark must measure the example")
	})
}

// digest returns the hash whose padded standard base64 is line, and fails
// the test for a line that is not the base64 of a hash.
func digest(tb testing.TB, line string) crypto.Digest {
	tb.Helper()

	raw, err := base64.StdEncoding.DecodeString(line)
	assert.NoError(tb, err, "the line must be base64")

	h, err := crypto.DigestFromBytes(raw)
	assert.NoError(tb, err, "the line must decode to a hash")

	return h
}
