// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"crypto/cipher"
	"encoding/hex"
	"io"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/aesgcm"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/constant"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// aes256GCMOverhead pins the bytes an AES-256-GCM envelope adds to its
// plaintext: 1 of version, 1 of name length, 11 of name, 12 of nonce and
// 16 of tag.
const aes256GCMOverhead = 41

// aeadKey is the key of every AEAD of the tests: the bytes 0 to 31. An
// AES-128 key is its first 16 bytes.
var aeadKey = []byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
}

// relabelled wraps an AEAD and reports a different Algorithm, leaving
// the key and the underlying cipher untouched. Two relabelled AEADs over
// one key differ in nothing but the name, so they isolate the binding of
// the algorithm into the tag.
type relabelled struct {
	crypto.AEAD
	name crypto.Algorithm
}

// Algorithm returns the name that r reports in place of the name of the
// wrapped AEAD.
func (r relabelled) Algorithm() crypto.Algorithm { return r.name }

func TestAEAD(t *testing.T) {
	t.Parallel()

	a := newAEAD(t, aesgcm.KeySize256)
	modes := []struct {
		name string
		aead crypto.AEAD
	}{
		{name: "with a caller nonce", aead: a},
		{name: "with a module nonce", aead: newModuleNonceAEAD(t)},
	}

	t.Run("SealedSize", func(t *testing.T) {
		t.Parallel()

		for _, mode := range modes {
			t.Run("returns the length of every envelope that AppendSeal writes "+mode.name, func(t *testing.T) {
				t.Parallel()
				prop.Equal(t, func(n int) int { return crypto.SealedSize(mode.aead, n) }, func(n int) int {
					sealed, _ := crypto.AppendSeal(nil, mode.aead, randcrypto.New(), make([]byte, n), nil)

					return len(sealed)
				}, "SealedSize must be the length of the envelope",
					prop.Using(prop.Integer(0, 4096)), prop.Example(0), prop.Example(1), prop.Example(1024))
			})

			t.Run("adds 41 bytes to a plaintext under AES-256-GCM "+mode.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, crypto.SealedSize(mode.aead, 100), 100+aes256GCMOverhead,
					"an AES-256-GCM envelope must add its header, nonce and tag")
			})
		}
	})

	t.Run("Seal", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an envelope that Open opens", func(t *testing.T) {
			t.Parallel()
			prop.RoundTrip(t, func(p []byte) ([]byte, error) {
				return crypto.Seal(a, randcrypto.New(), p, []byte("aad"))
			}, func(sealed []byte) ([]byte, error) {
				return crypto.Open(a, sealed, []byte("aad"))
			}, "Open must recover the plaintext that Seal sealed",
				prop.Using(prop.Bytes(prop.MaxSize(256))), assert.EquateEmpty())
		})

		t.Run("writes the version then the length of the name then the name", func(t *testing.T) {
			t.Parallel()
			sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), nil)
			assert.NoError(t, err, "Seal must succeed")
			alg := string(a.Algorithm())
			expect.Equal(t, sealed[0], byte(crypto.EnvelopeVersion), "the version must lead the envelope")
			expect.Equal(t, int(sealed[1]), len(alg), "the length of the name must follow the version")
			expect.Equal(t, string(sealed[2:2+len(alg)]), alg, "the name must follow its length")
		})

		// The layout of an envelope is frozen, so a change to any byte
		// here changes every envelope ever sealed.
		t.Run("returns the recorded envelope for fixed inputs", func(t *testing.T) {
			t.Parallel()
			want, err := hex.DecodeString("01" + // envelope version
				"0b" + // algorithm length
				"6165732d3235362d67636d" + // "aes-256-gcm"
				"000102030405060700010203" + // nonce
				"f76564626377d7" + // ciphertext of "payload"
				"9bd3c07b7edbf8db8bd2d5f62ae3280d") // tag
			assert.NoError(t, err, "the recorded envelope must be hexadecimal")
			sealed, err := crypto.Seal(a, constant.New(0x0706050403020100), []byte("payload"), []byte("aad"))
			assert.NoError(t, err, "Seal must succeed")
			assert.Equal(t, sealed, want, "the sealed envelope must match its recorded bytes")
		})

		t.Run("returns a buffer of SealedSize bytes of capacity", func(t *testing.T) {
			t.Parallel()
			prop.Equal(t, func(n int) int {
				sealed, _ := crypto.Seal(a, randcrypto.New(), make([]byte, n), nil)

				return cap(sealed)
			}, func(n int) int { return crypto.SealedSize(a, n) }, "Seal must size its buffer once and exactly",
				prop.Using(prop.Integer(0, 4096)), prop.Example(0), prop.Example(1), prop.Example(1024))
		})

		t.Run("writes an algorithm name of 255 bytes", func(t *testing.T) {
			t.Parallel()
			name := crypto.Algorithm(strings.Repeat("x", 255))
			sealed, err := crypto.Seal(relabelled{AEAD: a, name: name}, randcrypto.New(), []byte("payload"), nil)
			assert.NoError(t, err, "a name of 255 bytes must fit the header")
			got, err := crypto.PeekAlgorithm(sealed)
			assert.NoError(t, err, "the header must parse")
			assert.Equal(t, got, name, "the whole name must be in the header")
		})

		tests := []struct {
			name string
			give crypto.Algorithm
		}{
			{name: "returns ErrAlgorithmSize for an empty algorithm name", give: ""},
			{
				name: "returns ErrAlgorithmSize for an algorithm name of 256 bytes",
				give: crypto.Algorithm(strings.Repeat("x", 256)),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := crypto.Seal(relabelled{AEAD: a, name: tt.give}, randcrypto.New(), nil, nil)
				assert.ErrorIs(t, err, crypto.ErrAlgorithmSize, "a name the header cannot express must be refused")
			})
		}

		// Both AEADs share one key and one cipher and differ only in the
		// name that they report, so only the tag can tell them apart.
		t.Run("binds the algorithm into the tag", func(t *testing.T) {
			t.Parallel()
			const (
				nameA = "test-aead-aaa"
				nameB = "test-aead-bbb"
			)
			sealed, err := crypto.Seal(relabelled{AEAD: a, name: nameA}, randcrypto.New(), []byte("payload"), nil)
			assert.NoError(t, err, "Seal must succeed")

			// The rewritten envelope parses and names the algorithm of the
			// opening AEAD.
			forged := bytes.Clone(sealed)
			copy(forged[2:2+len(nameA)], nameB)

			_, err = crypto.Open(relabelled{AEAD: a, name: nameB}, forged, nil)
			assert.HasError(t, err, "a rewritten algorithm must not authenticate")
			assert.ErrorIsNot(t, err, crypto.ErrAlgorithmMismatch, "only the tag must refuse the rewrite")
		})

		// An independently rebuilt frame opens what Seal produced, so a
		// change of the framing fails here.
		t.Run("authenticates the domain then the name then the associated data", func(t *testing.T) {
			t.Parallel()
			sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), []byte("caller"))
			assert.NoError(t, err, "Seal must succeed")

			f := crypto.NewFramer(nil, crypto.Domain{Name: crypto.EnvelopeDomainName, Version: crypto.EnvelopeVersion})
			f.String(string(a.Algorithm()))
			f.Bytes([]byte("caller"))

			rest := sealed[2+int(sealed[1]):]
			opened, err := cipher.AEAD.Open(a, nil, rest[:a.NonceSize()], rest[a.NonceSize():], f.Frame())
			assert.NoError(t, err, "a rebuilt frame must open the envelope")
			assert.Equal(t, opened, []byte("payload"), "the rebuilt frame must open to the plaintext")
		})

		t.Run("returns the error of the source", func(t *testing.T) {
			t.Parallel()
			failing := randcrypto.NewWithReader(iotest.ErrReader(io.ErrUnexpectedEOF))
			_, err := crypto.Seal(a, failing, []byte("payload"), nil)
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "Seal must return the failure of the source")
		})

		t.Run("returns nil with the error of the source", func(t *testing.T) {
			t.Parallel()
			failing := randcrypto.NewWithReader(iotest.ErrReader(io.ErrUnexpectedEOF))
			sealed, err := crypto.Seal(a, failing, []byte("payload"), nil)
			assert.HasError(t, err, "the test must seal with a source that fails")
			assert.Nil(t, sealed, "Seal must return no envelope with an error")
		})

		// A deterministic source repeats the nonce, the hazard that the
		// docblock of Seal names.
		t.Run("returns the same envelope twice for a constant source", func(t *testing.T) {
			t.Parallel()
			first, err := crypto.Seal(a, constant.New(1), []byte("payload"), nil)
			assert.NoError(t, err, "Seal must succeed")
			second, err := crypto.Seal(a, constant.New(1), []byte("payload"), nil)
			assert.NoError(t, err, "Seal must succeed")
			assert.Equal(t, first, second, "a constant source must repeat the nonce")
		})

		// A nil source panics on its first read, so a call that returns
		// reads no source.
		t.Run("reads no source for an AEAD with a module nonce", func(t *testing.T) {
			t.Parallel()
			m := newModuleNonceAEAD(t)
			sealed, err := crypto.Seal(m, nil, []byte("payload"), []byte("aad"))
			assert.NoError(t, err, "Seal must not read a source")
			opened, err := crypto.Open(m, sealed, []byte("aad"))
			assert.NoError(t, err, "Open must open the envelope")
			assert.Equal(t, opened, []byte("payload"), "Open must recover the plaintext")
		})
	})

	t.Run("AppendSeal", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the envelope after the bytes of dst", func(t *testing.T) {
			t.Parallel()
			dst := append(make([]byte, 0, 512), "keep me"...)
			dst, err := crypto.AppendSeal(dst, a, randcrypto.New(), []byte("payload"), nil)
			assert.NoError(t, err, "AppendSeal must succeed")
			assert.HasPrefix(t, string(dst), "keep me", "AppendSeal must keep the bytes of dst")
			opened, err := crypto.Open(a, dst[len("keep me"):], nil)
			assert.NoError(t, err, "the appended envelope must open")
			assert.Equal(t, opened, []byte("payload"), "the appended envelope must open to the plaintext")
		})
	})

	sealed, err := crypto.Seal(a, randcrypto.New(), []byte("payload"), []byte("aad"))
	assert.NoError(t, err, "Seal must succeed")
	header := 2 + int(sealed[1])

	t.Run("Open", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrEnvelopeVersion for an unknown version", func(t *testing.T) {
			t.Parallel()
			forged := bytes.Clone(sealed)
			forged[0]++
			_, err := crypto.Open(a, forged, []byte("aad"))
			assert.ErrorIs(t, err, crypto.ErrEnvelopeVersion,
				"an unknown layout must be refused before any key is used")
		})

		t.Run("returns ErrCiphertextShort for an envelope shorter than its header and nonce", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := crypto.Open(a, sealed[:n], []byte("aad"))

				return err
			}, crypto.ErrCiphertextShort, "a prefix of the header and the nonce must be a size error",
				prop.Using(prop.Integer(0, header+a.NonceSize()-1)),
				prop.Example(0), prop.Example(header-1), prop.Example(header+a.NonceSize()-1))
		})

		t.Run("returns ErrAlgorithmMismatch for an envelope of another algorithm", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.Open(newAEAD(t, aesgcm.KeySize128), sealed, []byte("aad"))
			assert.ErrorIs(t, err, crypto.ErrAlgorithmMismatch, "the envelope must name the algorithm of the AEAD")
		})

		t.Run("returns an error of class Invalid for an envelope of another algorithm", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.Open(newAEAD(t, aesgcm.KeySize128), sealed, []byte("aad"))
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrAlgorithmMismatch must classify as Invalid")
		})

		t.Run("returns ErrAlgorithmSize for an empty algorithm name", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.Open(a, []byte{crypto.EnvelopeVersion, 0}, nil)
			assert.ErrorIs(t, err, crypto.ErrAlgorithmSize, "an envelope must name an algorithm")
		})

		// A nonce of full length is long enough to attempt, so it fails
		// authentication and not the size check.
		t.Run("returns an error of the AEAD for a nonce without a body", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.Open(a, sealed[:header+a.NonceSize()], []byte("aad"))
			assert.HasError(t, err, "a nonce without a tag must fail")
			assert.ErrorIsNot(t, err, crypto.ErrCiphertextShort, "a nonce of full length must not be a size error")
		})

		t.Run("returns an error of the AEAD for other associated data", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.Open(a, sealed, []byte("other"))
			assert.HasError(t, err, "associated data must be authenticated")
			expect.ErrorIsNot(t, err, crypto.ErrCiphertextShort, "the failure must not be a size error")
			expect.ErrorIsNot(t, err, crypto.ErrAlgorithmMismatch, "the failure must not be a name error")
		})

		// The pre-envelope framing is a nonce and a ciphertext without a
		// header. A reader that falls back to it lets an attacker force
		// the weaker mode by stripping bytes.
		forgeries := []struct {
			name  string
			forge func([]byte) []byte
		}{
			{name: "returns an error for a changed tag", forge: func(b []byte) []byte {
				b[len(b)-1] ^= 0xFF

				return b
			}},
			{name: "returns an error for a changed nonce", forge: func(b []byte) []byte {
				b[header] ^= 0xFF

				return b
			}},
			{name: "returns an error for a ciphertext without a header", forge: func(b []byte) []byte {
				return b[header:]
			}},
		}
		for _, tt := range forgeries {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := crypto.Open(a, tt.forge(bytes.Clone(sealed)), []byte("aad"))
				assert.HasError(t, err, "a changed envelope must not open")
			})
		}

		t.Run("returns nil for an empty plaintext", func(t *testing.T) {
			t.Parallel()
			empty, err := crypto.Seal(a, randcrypto.New(), nil, nil)
			assert.NoError(t, err, "Seal must succeed")
			opened, err := crypto.Open(a, empty, nil)
			assert.NoError(t, err, "Open must open the envelope")
			assert.Nil(t, opened, "an empty plaintext must open to nil")
		})
	})

	t.Run("AppendOpen", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the plaintext after the bytes of dst", func(t *testing.T) {
			t.Parallel()
			got, err := crypto.AppendOpen(append(make([]byte, 0, 64), "keep me"...), a, sealed, []byte("aad"))
			assert.NoError(t, err, "AppendOpen must succeed")
			assert.Equal(t, string(got), "keep mepayload", "AppendOpen must append the plaintext to dst")
		})

		t.Run("returns nil for an envelope that it refuses", func(t *testing.T) {
			t.Parallel()
			got, err := crypto.AppendOpen([]byte("keep me"), a, []byte{crypto.EnvelopeVersion + 1, 1, 'x'}, nil)
			assert.HasError(t, err, "the test must open an envelope that is refused")
			assert.Nil(t, got, "a refused envelope must return no partial output")
		})

		t.Run("leaves dst unchanged for an envelope that it refuses", func(t *testing.T) {
			t.Parallel()
			dst := append(make([]byte, 0, 512), "keep me"...)
			_, err := crypto.AppendOpen(dst, a, []byte{crypto.EnvelopeVersion + 1, 1, 'x'}, nil)
			assert.HasError(t, err, "the test must open an envelope that is refused")
			assert.Equal(t, string(dst), "keep me", "a refused envelope must leave the bytes of dst unchanged")
		})
	})

	t.Run("PeekAlgorithm", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the algorithm of the AEAD that sealed the envelope", func(t *testing.T) {
			t.Parallel()
			got, err := crypto.PeekAlgorithm(sealed)
			assert.NoError(t, err, "the header must be readable without a key")
			assert.Equal(t, got, a.Algorithm(), "the name must be the name of the AEAD that sealed it")
		})

		// Peeking reads only the header, so the name may end at the end
		// of the input.
		t.Run("returns the name of a header without a body", func(t *testing.T) {
			t.Parallel()
			got, err := crypto.PeekAlgorithm([]byte{crypto.EnvelopeVersion, 2, 'h', 'i'})
			assert.NoError(t, err, "a complete header must parse without a body")
			assert.Equal(t, got, crypto.Algorithm("hi"), "the name must be read whole")
		})

		short := []struct {
			name string
			give []byte
		}{
			{name: "returns ErrCiphertextShort for an empty envelope", give: nil},
			{name: "returns ErrCiphertextShort for an envelope of one byte", give: []byte{crypto.EnvelopeVersion}},
			{
				name: "returns ErrCiphertextShort for a name longer than the envelope",
				give: []byte{crypto.EnvelopeVersion, 8, 'a', 'b'},
			},
		}
		for _, tt := range short {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := crypto.PeekAlgorithm(tt.give)
				assert.ErrorIs(t, err, crypto.ErrCiphertextShort, "the header must fit the envelope")
			})
		}

		t.Run("returns an error of class Integrity for a name longer than the envelope", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.PeekAlgorithm([]byte{crypto.EnvelopeVersion, 8, 'a', 'b'})
			assert.Equal(t, errs.Classify(err), errs.Integrity, "ErrCiphertextShort must classify as Integrity")
		})

		t.Run("returns ErrEnvelopeVersion for an unknown version", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.PeekAlgorithm([]byte{crypto.EnvelopeVersion + 1, 1, 'x'})
			assert.ErrorIs(t, err, crypto.ErrEnvelopeVersion, "an unknown layout must be refused")
		})

		t.Run("returns an error of class Unsupported for an unknown version", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.PeekAlgorithm([]byte{crypto.EnvelopeVersion + 1, 1, 'x'})
			assert.Equal(t, errs.Classify(err), errs.Unsupported, "ErrEnvelopeVersion must classify as Unsupported")
		})

		t.Run("returns ErrAlgorithmSize for an empty algorithm name", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.PeekAlgorithm([]byte{crypto.EnvelopeVersion, 0})
			assert.ErrorIs(t, err, crypto.ErrAlgorithmSize, "an envelope must name an algorithm")
		})

		t.Run("returns an error of class Integrity for an empty algorithm name", func(t *testing.T) {
			t.Parallel()
			_, err := crypto.PeekAlgorithm([]byte{crypto.EnvelopeVersion, 0})
			assert.Equal(t, errs.Classify(err), errs.Integrity, "ErrAlgorithmSize must classify as Integrity")
		})
	})
}

// TestAEADAllocs checks the allocation contract of each function of the
// envelope, in both nonce modes where the mode matters. MaxAllocs counts
// the allocations of the whole process, so the test does not run in
// parallel.
//
//nolint:paralleltest // see above
func TestAEADAllocs(t *testing.T) {
	plaintext := make([]byte, 64)
	dst := make([]byte, 0, 256)
	// Converted once, so no measured call boxes the source.
	var r rand.Rand = randcrypto.New()
	modes := []struct {
		name string
		aead crypto.AEAD
	}{
		{name: "with a caller nonce", aead: newAEAD(t, aesgcm.KeySize256)},
		{name: "with a module nonce", aead: newModuleNonceAEAD(t)},
	}

	t.Run("SealedSize", func(t *testing.T) {
		var n int
		expect.MaxAllocs(t, func() { n = crypto.SealedSize(modes[0].aead, len(plaintext)) }, 0,
			"SealedSize must not allocate")
		assert.Equal(t, n, len(plaintext)+aes256GCMOverhead, "the test must measure the size of an envelope")
	})

	t.Run("Seal", func(t *testing.T) {
		var sealed []byte
		expect.MaxAllocs(t, func() { sealed, _ = crypto.Seal(modes[0].aead, r, plaintext, nil) }, 1,
			"Seal must allocate only the envelope")
		assert.Length(t, sealed, len(plaintext)+aes256GCMOverhead, "the test must measure an envelope")
	})

	t.Run("AppendSeal", func(t *testing.T) {
		for _, mode := range modes {
			t.Run(mode.name, func(t *testing.T) {
				var err error
				expect.MaxAllocs(t, func() { dst, err = crypto.AppendSeal(dst[:0], mode.aead, r, plaintext, nil) }, 0,
					"AppendSeal must not allocate when dst has capacity")
				assert.NoError(t, err, "the test must measure a seal that succeeds")
			})
		}

		// Two collections empty the pool of frame buffers, so the warm-up
		// call of MaxAllocs grows a buffer for the frame of aad. The
		// measured calls reuse it only when AppendSeal returns the grown
		// buffer to the pool.
		t.Run("with associated data longer than a pooled buffer", func(t *testing.T) {
			aad := make([]byte, 4<<10)
			runtime.GC()
			runtime.GC()
			var err error
			expect.MaxAllocs(t, func() { dst, err = crypto.AppendSeal(dst[:0], modes[0].aead, r, plaintext, aad) }, 0,
				"AppendSeal must keep the buffer that it grew for the frame")
			assert.NoError(t, err, "the test must measure a seal that succeeds")
		})
	})

	t.Run("Open", func(t *testing.T) {
		sealed, err := crypto.Seal(modes[0].aead, r, plaintext, nil)
		assert.NoError(t, err, "Seal must succeed")
		expect.MaxAllocs(t, func() { _, err = crypto.Open(modes[0].aead, sealed, nil) }, 1,
			"Open must allocate only the plaintext")
		assert.NoError(t, err, "the test must measure an open that succeeds")
	})

	t.Run("AppendOpen", func(t *testing.T) {
		for _, mode := range modes {
			t.Run(mode.name, func(t *testing.T) {
				sealed, err := crypto.Seal(mode.aead, r, plaintext, nil)
				assert.NoError(t, err, "Seal must succeed")
				expect.MaxAllocs(t, func() { dst, err = crypto.AppendOpen(dst[:0], mode.aead, sealed, nil) }, 0,
					"AppendOpen must not allocate when dst has capacity")
				assert.NoError(t, err, "the test must measure an open that succeeds")
			})
		}

		// Two collections empty the pool of frame buffers, so the warm-up
		// call of MaxAllocs grows a buffer for the frame of aad. The
		// measured calls reuse it only when AppendOpen returns the grown
		// buffer to the pool.
		t.Run("with associated data longer than a pooled buffer", func(t *testing.T) {
			aad := make([]byte, 4<<10)
			sealed, err := crypto.Seal(modes[0].aead, r, plaintext, aad)
			assert.NoError(t, err, "Seal must succeed")
			runtime.GC()
			runtime.GC()
			expect.MaxAllocs(t, func() { dst, err = crypto.AppendOpen(dst[:0], modes[0].aead, sealed, aad) }, 0,
				"AppendOpen must keep the buffer that it grew for the frame")
			assert.NoError(t, err, "the test must measure an open that succeeds")
		})
	})

	t.Run("PeekAlgorithm", func(t *testing.T) {
		sealed, err := crypto.Seal(modes[0].aead, r, plaintext, nil)
		assert.NoError(t, err, "Seal must succeed")
		var alg crypto.Algorithm
		expect.MaxAllocs(t, func() { alg, _ = crypto.PeekAlgorithm(sealed) }, 1,
			"PeekAlgorithm must allocate only the name")
		assert.Equal(t, alg, modes[0].aead.Algorithm(), "the test must measure a header that parses")
	})
}

// BenchmarkAEAD reports the cost of each function of the envelope over a
// plaintext of 1 KiB, and of the embedded primitive alone as the
// baseline of the envelope's cost, and fails when one of them allocates
// more than TestAEADAllocs allows.
func BenchmarkAEAD(b *testing.B) {
	a := newAEAD(b, aesgcm.KeySize256)
	var r rand.Rand = randcrypto.New()
	plaintext := bytes.Repeat([]byte{0x5A}, 1024)
	sealed, err := crypto.Seal(a, r, plaintext, nil)
	assert.NoError(b, err, "Seal must succeed")

	b.Run("SealedSize", func(b *testing.B) {
		var n int

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			n = crypto.SealedSize(a, len(plaintext))
		}

		assert.Equal(b, n, len(plaintext)+aes256GCMOverhead, "the benchmark must measure the size of an envelope")
	})

	b.Run("Seal", func(b *testing.B) {
		var out []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			out, _ = crypto.Seal(a, r, plaintext, nil)
		}

		assert.Length(b, out, len(plaintext)+aes256GCMOverhead, "the benchmark must measure an envelope")
	})

	b.Run("AppendSeal", func(b *testing.B) {
		dst := make([]byte, 0, 2048)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			dst, _ = crypto.AppendSeal(dst[:0], a, r, plaintext, nil)
		}

		assert.Length(b, dst, len(plaintext)+aes256GCMOverhead, "the benchmark must measure an envelope")
	})

	// The embedded primitive with a sized destination and a nonce that
	// the caller manages. The cost of the envelope is the difference
	// between this and AppendSeal.
	b.Run("BaselineSeal", func(b *testing.B) {
		nonce := make([]byte, a.NonceSize())
		dst := make([]byte, 0, 2048)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			dst = cipher.AEAD.Seal(a, dst[:0], nonce, plaintext, nil)
		}

		assert.Length(b, dst, len(plaintext)+a.Overhead(), "the benchmark must measure a ciphertext")
	})

	b.Run("Open", func(b *testing.B) {
		var out []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			out, _ = crypto.Open(a, sealed, nil)
		}

		assert.Equal(b, out, plaintext, "the benchmark must measure an open that succeeds")
	})

	b.Run("AppendOpen", func(b *testing.B) {
		dst := make([]byte, 0, 2048)

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			dst, _ = crypto.AppendOpen(dst[:0], a, sealed, nil)
		}

		assert.Equal(b, dst, plaintext, "the benchmark must measure an open that succeeds")
	})

	b.Run("PeekAlgorithm", func(b *testing.B) {
		var alg crypto.Algorithm

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			alg, _ = crypto.PeekAlgorithm(sealed)
		}

		assert.Equal(b, alg, a.Algorithm(), "the benchmark must measure a header that parses")
	})
}

// newAEAD returns an AES-GCM AEAD over the first size bytes of aeadKey,
// with nonces that Seal draws. It fails tb when aesgcm refuses the key.
func newAEAD(tb testing.TB, size int) crypto.AEAD {
	tb.Helper()

	a, err := aesgcm.New(aeadKey[:size])
	assert.NoError(tb, err, "aesgcm.New must accept a key of a valid size")

	return a
}

// newModuleNonceAEAD returns an AES-256-GCM AEAD over aeadKey that draws
// its own nonces. It fails tb when aesgcm refuses the key.
func newModuleNonceAEAD(tb testing.TB) crypto.AEAD {
	tb.Helper()

	a, err := aesgcm.NewRandomNonce(aeadKey)
	assert.NoError(tb, err, "aesgcm.NewRandomNonce must accept a key of 32 bytes")

	return a
}
