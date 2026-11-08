// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package tlog_test

import (
	"cmp"
	"encoding/base64"
	"math"
	"strconv"
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
	// c2spHeader is the header line of C2SP tlog-proof v1, and proofHeader
	// that line with its newline.
	c2spHeader  = "c2sp.org/tlog-proof@v1"
	proofHeader = c2spHeader + "\n"

	// sha256Header, sha384Header and sha512Header are the header lines of
	// formats of SHA-256, SHA-384 and SHA-512 hashes that C2SP does not
	// define.
	sha256Header = "example.org/tlog-proof-sha256@v1"
	sha384Header = "example.org/tlog-proof-sha384@v1"
	sha512Header = "example.org/tlog-proof-sha512@v1"

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

// proofs generates the proofs of C2SP tlog-proof v1, whose hashes have 32
// bytes.
var proofs = proofsOf(crypto.DigestSize256)

// formats are the formats of the round trip of AppendFormat: C2SP
// tlog-proof v1 and a format of each size of a digest.
var formats = []tlog.ProofFormat{
	{},
	{Header: sha256Header},
	{Header: sha384Header, Size: crypto.DigestSize384},
	{Header: sha512Header, Size: crypto.DigestSize512},
}

// formatted is a proof and the format of its text.
type formatted struct {
	format tlog.ProofFormat
	proof  tlog.Proof
}

// formattedProofs generates a format of formats and a proof whose hashes have
// the size of the format.
var formattedProofs = prop.Composite(func(c *prop.Case) formatted {
	f := c.Draw(prop.SampledFrom(formats...), "format")

	return formatted{format: f, proof: c.Draw(proofsOf(cmp.Or(f.Size, crypto.DigestSize256)), "proof")}
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

	t.Run("AppendFormat", func(t *testing.T) {
		t.Parallel()

		sha256 := example(t).Hashes[0]
		sha384 := coresha512.New384().Hash([]byte("a SHA-384 digest"))
		sha512 := coresha512.New512().Hash([]byte("a SHA-512 digest"))
		tests := []struct {
			name       string
			giveFormat tlog.ProofFormat
			giveHash   crypto.Digest
			wantHeader string
		}{
			{name: "writes the header line of C2SP for the zero format", giveHash: sha256, wantHeader: c2spHeader},
			{
				name:       "writes the header line of C2SP for that header line with 32 bytes",
				giveFormat: tlog.ProofFormat{Header: c2spHeader, Size: crypto.DigestSize256},
				giveHash:   sha256,
				wantHeader: c2spHeader,
			},
			{
				name:       "writes hashes of 32 bytes for a header line without a size",
				giveFormat: tlog.ProofFormat{Header: sha256Header},
				giveHash:   sha256,
				wantHeader: sha256Header,
			},
			{
				name:       "writes the hashes of a format of SHA-384",
				giveFormat: tlog.ProofFormat{Header: sha384Header, Size: crypto.DigestSize384},
				giveHash:   sha384,
				wantHeader: sha384Header,
			},
			{
				name:       "writes the hashes of a format of SHA-512",
				giveFormat: tlog.ProofFormat{Header: sha512Header, Size: crypto.DigestSize512},
				giveHash:   sha512,
				wantHeader: sha512Header,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				p := tlog.Proof{Index: 7, Hashes: []crypto.Digest{tt.giveHash}, Checkpoint: []byte(shortCheckpoint)}
				got, err := p.AppendFormat([]byte("kept"), tt.giveFormat)
				assert.NoError(t, err, "AppendFormat must accept a proof of its format")
				line := base64.StdEncoding.EncodeToString(tt.giveHash.Bytes())
				want := "kept" + tt.wantHeader + "\nindex 7\n" + line + "\n\n" + shortCheckpoint
				assert.Equal(t, string(got), want,
					"AppendFormat must write the header line and the hashes of the format")
			})
		}

		refusals := []struct {
			name       string
			giveFormat tlog.ProofFormat
			giveHashes []crypto.Digest
		}{
			{
				name:       "returns ErrProofText for a hash of another size than the format",
				giveFormat: tlog.ProofFormat{Header: sha384Header, Size: crypto.DigestSize384},
				giveHashes: []crypto.Digest{sha256},
			},
			{
				name:       "returns ErrProofText for a size that is not the size of a digest",
				giveFormat: tlog.ProofFormat{Header: sha384Header, Size: 40},
			},
			{
				name:       "returns ErrProofText for a header line with a newline",
				giveFormat: tlog.ProofFormat{Header: "example.org/tlog\nproof"},
			},
			{
				name:       "returns ErrProofText for the header line of C2SP with 48 bytes",
				giveFormat: tlog.ProofFormat{Header: c2spHeader, Size: crypto.DigestSize384},
			},
			{
				name:       "returns ErrProofText for 48 bytes without a header line",
				giveFormat: tlog.ProofFormat{Size: crypto.DigestSize384},
			},
		}
		for _, tt := range refusals {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				p := tlog.Proof{Hashes: tt.giveHashes, Checkpoint: []byte(shortCheckpoint)}
				got, err := p.AppendFormat([]byte("kept"), tt.giveFormat)
				expect.ErrorIs(t, err, tlog.ErrProofText, "AppendFormat must refuse the proof")
				expect.Equal(t, string(got), "kept", "AppendFormat must return dst unchanged")
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

	t.Run("UnmarshalFormat", func(t *testing.T) {
		t.Parallel()

		f384 := tlog.ProofFormat{Header: sha384Header, Size: crypto.DigestSize384}
		f512 := tlog.ProofFormat{Header: sha512Header, Size: crypto.DigestSize512}
		sha384 := coresha512.New384().Hash([]byte("a SHA-384 digest"))
		line384 := base64.StdEncoding.EncodeToString(sha384.Bytes())

		t.Run("sets p to the proof of a format of SHA-384", func(t *testing.T) {
			t.Parallel()
			var p tlog.Proof
			text := sha384Header + "\nindex 7\n" + line384 + "\n\n" + shortCheckpoint
			assert.NoError(t, p.UnmarshalFormat([]byte(text), f384), "UnmarshalFormat must accept a text of its format")
			expect.Equal(t, p.Index, uint64(7), "UnmarshalFormat must set the index")
			expect.Equal(t, p.Hashes, []crypto.Digest{sha384}, "UnmarshalFormat must set the hashes")
			expect.Equal(t, string(p.Checkpoint), shortCheckpoint, "UnmarshalFormat must set the checkpoint")
		})

		t.Run("sets p to the proof of the example for the zero format", func(t *testing.T) {
			t.Parallel()
			var p tlog.Proof
			assert.NoError(t, p.UnmarshalFormat([]byte(exampleProof), tlog.ProofFormat{}),
				"UnmarshalFormat must accept the example")
			expect.Equal(t, p.Hashes, example(t).Hashes, "UnmarshalFormat must set the hashes of the example")
		})

		t.Run("empties p after an error of the format", func(t *testing.T) {
			t.Parallel()
			p := example(t)
			hashes := cap(p.Hashes)

			assert.ErrorIs(t, p.UnmarshalFormat([]byte(exampleProof), tlog.ProofFormat{Size: 40}), tlog.ErrProofText,
				"UnmarshalFormat must refuse a format of 40 bytes")
			expect.Empty(t, p.Extra, "p must have no extra data after an error")
			expect.Empty(t, p.Hashes, "p must have no hashes after an error")
			expect.Empty(t, p.Checkpoint, "p must have no checkpoint after an error")
			expect.Equal(t, p.Index, uint64(0), "p must have the index 0 after an error")
			expect.Equal(t, cap(p.Hashes), hashes, "p must keep the memory of its hashes after an error")
		})

		// A line of the length of the base64 of 48 bytes decodes to 46, 47 or
		// 48 bytes, and a line of the length of the base64 of 64 bytes to 64,
		// 65 or 66 bytes.
		tests := []struct {
			name       string
			giveFormat tlog.ProofFormat
			giveText   string
		}{
			{
				name:       "returns ErrProofText for the header line of another format",
				giveFormat: f384,
				giveText:   proofHeader + "index 7\n" + line384 + "\n\n" + shortCheckpoint,
			},
			{
				name:       "returns ErrProofText for a hash line of 32 bytes in a format of 48 bytes",
				giveFormat: f384,
				giveText:   sha384Header + "\nindex 7\n" + hashLine + "\n\n" + shortCheckpoint,
			},
			{
				name:       "returns ErrProofText for a hash line of 47 bytes in a format of 48 bytes",
				giveFormat: f384,
				giveText: sha384Header + "\nindex 7\n" + base64.StdEncoding.EncodeToString(make([]byte, 47)) + "\n\n" +
					shortCheckpoint,
			},
			{
				name:       "returns ErrProofText for a hash line of 65 bytes in a format of 64 bytes",
				giveFormat: f512,
				giveText: sha512Header + "\nindex 7\n" + base64.StdEncoding.EncodeToString(make([]byte, 65)) + "\n\n" +
					shortCheckpoint,
			},
			{
				name:       "returns ErrProofText for a hash line of 66 bytes in a format of 64 bytes",
				giveFormat: f512,
				giveText: sha512Header + "\nindex 7\n" + base64.StdEncoding.EncodeToString(make([]byte, 66)) + "\n\n" +
					shortCheckpoint,
			},
			{
				// Decode writes the 48 bytes of the first 16 groups of the line
				// before it refuses the 17th group.
				name:       "returns ErrProofText for a hash line whose 17th group is not base64 in a format of 64 bytes",
				giveFormat: f512,
				giveText: sha512Header + "\nindex 7\n" + base64.StdEncoding.EncodeToString(make([]byte, 48)) + "!!!!" +
					base64.StdEncoding.EncodeToString(make([]byte, 15)) + "\n\n" + shortCheckpoint,
			},
			{
				name:       "returns ErrProofText for a size that is not the size of a digest",
				giveFormat: tlog.ProofFormat{Header: sha384Header, Size: 40},
				giveText:   sha384Header + "\nindex 7\n\n" + shortCheckpoint,
			},
			{
				name:       "returns ErrProofText for a header line with a newline",
				giveFormat: tlog.ProofFormat{Header: sha384Header + "\nindex 7"},
				giveText:   sha384Header + "\nindex 7\n\n" + shortCheckpoint,
			},
			{
				name:       "returns ErrProofText for the header line of C2SP with 48 bytes",
				giveFormat: tlog.ProofFormat{Header: c2spHeader, Size: crypto.DigestSize384},
				giveText:   proofHeader + "index 7\n" + line384 + "\n\n" + shortCheckpoint,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var p tlog.Proof
				err := p.UnmarshalFormat([]byte(tt.giveText), tt.giveFormat)
				expect.ErrorIs(t, err, tlog.ErrProofText, "UnmarshalFormat must refuse the text")
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

	t.Run("round-trips every proof of every format through its text", func(t *testing.T) {
		t.Parallel()
		prop.RoundTrip(t,
			func(v formatted) ([]byte, error) { return v.proof.AppendFormat(nil, v.format) },
			func(text []byte) (formatted, error) {
				// A reader of more than one format takes the format of the first
				// line of the text.
				header, _, _ := strings.Cut(string(text), "\n")
				var v formatted
				for _, f := range formats {
					if cmp.Or(f.Header, c2spHeader) == header {
						v.format = f
					}
				}
				err := v.proof.UnmarshalFormat(text, v.format)

				return v, err
			},
			"the text of a proof must parse to the proof under its format", prop.Using(formattedProofs))
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

	t.Run("AppendFormat", func(t *testing.T) {
		t.Run("into a buffer with room", func(t *testing.T) {
			p, f, text := proof384(t)
			buf := make([]byte, 0, len(text))

			var (
				got []byte
				err error
			)
			expect.MaxAllocs(t, func() { got, err = p.AppendFormat(buf[:0], f) }, 0,
				"AppendFormat must not allocate into a buffer with room")
			assert.NoError(t, err, "the test must measure a proof that AppendFormat accepts")
			assert.Equal(t, string(got), string(text), "the test must measure the text of the proof")
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

	t.Run("UnmarshalFormat", func(t *testing.T) {
		t.Run("into a Proof with room", func(t *testing.T) {
			want, f, text := proof384(t)

			var p tlog.Proof
			assert.NoError(t, p.UnmarshalFormat(text, f), "UnmarshalFormat must accept the text")

			var err error
			expect.MaxAllocs(t, func() { err = p.UnmarshalFormat(text, f) }, 0,
				"UnmarshalFormat must not allocate into a Proof with room")
			assert.NoError(t, err, "the test must measure a text that UnmarshalFormat accepts")
			assert.Equal(t, p.Hashes, want.Hashes, "the test must measure the hashes of the proof")
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

	b.Run("AppendFormat", func(b *testing.B) {
		b.Run("into a buffer with room", func(b *testing.B) {
			p, f, text := proof384(b)
			buf := make([]byte, 0, len(text))

			var (
				got []byte
				err error
			)

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				got, err = p.AppendFormat(buf[:0], f)
			}

			assert.NoError(b, err, "the benchmark must measure a proof that AppendFormat accepts")
			assert.Equal(b, string(got), string(text), "the benchmark must measure the text of the proof")
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

	b.Run("UnmarshalFormat", func(b *testing.B) {
		b.Run("into a Proof with room", func(b *testing.B) {
			want, f, text := proof384(b)

			var p tlog.Proof
			assert.NoError(b, p.UnmarshalFormat(text, f), "UnmarshalFormat must accept the text")

			var err error

			c := bench.Start(b).MaxAllocs(0)
			defer c.End()

			for c.Loop() {
				err = p.UnmarshalFormat(text, f)
			}

			assert.NoError(b, err, "the benchmark must measure a text that UnmarshalFormat accepts")
			assert.Equal(b, p.Hashes, want.Hashes, "the benchmark must measure the hashes of the proof")
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

// proof384 returns the proof of the example with a SHA-384 hash of each of
// its hashes in their place, the format of SHA-384 hashes, and the text of
// that proof, which it builds with the base64 of the standard library.
func proof384(tb testing.TB) (tlog.Proof, tlog.ProofFormat, []byte) {
	tb.Helper()

	p := example(tb)
	text := []byte(sha384Header + "\nextra " + exampleExtra + "\nindex " + strconv.Itoa(exampleIndex) + "\n")
	h := coresha512.New384()
	for i := range p.Hashes {
		p.Hashes[i] = h.Hash(p.Hashes[i].Bytes())
		text = append(base64.StdEncoding.AppendEncode(text, p.Hashes[i].Bytes()), '\n')
	}

	f := tlog.ProofFormat{Header: sha384Header, Size: crypto.DigestSize384}

	return p, f, append(text, "\n"+exampleCheckpoint...)
}

// proofsOf generates proofs whose hashes have size bytes: any index, extra
// data of any length, up to 64 hashes, and a checkpoint of any bytes that ends
// with a newline. A proof without extra data has a nil Extra, as
// UnmarshalText returns it into a new Proof.
func proofsOf(size int) prop.Generator[tlog.Proof] {
	return prop.Composite(func(c *prop.Case) tlog.Proof {
		var p tlog.Proof
		p.Index = c.Draw(prop.Integer[uint64](0, math.MaxUint64), "index")

		if extra := c.Draw(prop.Bytes(), "extra"); len(extra) > 0 {
			p.Extra = extra
		}

		for range c.Draw(prop.Integer(0, 64), "hashes") {
			d, err := crypto.DigestFromBytes(c.Draw(prop.Bytes(prop.MinSize(size), prop.MaxSize(size)), "hash"))
			assert.NoError(c, err, "the bytes of a hash must be a digest")
			p.Hashes = append(p.Hashes, d)
		}

		p.Checkpoint = append(c.Draw(prop.Bytes(), "checkpoint"), '\n')

		return p
	})
}
