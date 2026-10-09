// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"testing/iotest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sha256"
)

// The contracts of the properties that the tests and the fuzz targets of
// HashDomain and HashReader share.
const (
	// hashDomainContract is the contract of hashDomainFrames.
	hashDomainContract = "HashDomain must hash the domain and each part after its length"

	// hashDomainBoundaryContract is the contract of hashDomainBoundaries.
	hashDomainBoundaryContract = "HashDomain must change the digest when the boundary between two parts moves"

	// hashReaderContract is the contract of hashReaderReads.
	hashReaderContract = "HashReader must return the digest of every byte that it reads"
)

// hasher is the Hasher of the tests of the crypto package. SHA-256 is
// the implementation that every build of the module contains.
var hasher = sha256.New()

// closeCounter wraps a Hasher and counts the Close calls of the streams
// that its NewStream returns. It is not safe for concurrent use.
type closeCounter struct {
	crypto.Hasher
	closes int
}

// NewStream returns a stream of the wrapped Hasher that counts its Close
// calls in h.
func (h *closeCounter) NewStream() crypto.Stream {
	return countedStream{Stream: h.Hasher.NewStream(), closes: &h.closes}
}

// countedStream adds one to *closes on each Close, then closes the
// wrapped Stream.
type countedStream struct {
	crypto.Stream
	closes *int
}

// Close counts the call and closes the wrapped Stream.
func (s countedStream) Close() {
	*s.closes++
	s.Stream.Close()
}

func TestHelpers(t *testing.T) {
	t.Parallel()

	t.Run("HashDomain", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the digest of the domain followed by each part after its length", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, hashDomainContract, hashDomainFrames)
		})

		t.Run("returns another digest when the boundary between two parts moves", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, hashDomainBoundaryContract, hashDomainBoundaries)
		})

		t.Run("returns another digest for another domain", func(t *testing.T) {
			t.Parallel()
			data := []byte("identical-payload")
			assert.NotEqual(t, crypto.HashDomain(hasher, []byte("domain-a"), data),
				crypto.HashDomain(hasher, []byte("domain-b"), data), "two domains must give two digests")
		})

		t.Run("returns the digest of the domain alone for no parts", func(t *testing.T) {
			t.Parallel()
			domain := []byte("just:the:domain")
			assert.Equal(t, crypto.HashDomain(hasher, domain), hasher.Hash(domain),
				"HashDomain with no parts must equal Hash of the domain")
		})
	})

	t.Run("HashReader", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the digest of every byte that it reads", func(t *testing.T) {
			t.Parallel()
			prop.ForAll(t, hashReaderContract, hashReaderReads)
		})

		t.Run("returns the error of the reader behind the prefix of the package", func(t *testing.T) {
			t.Parallel()
			errRead := errors.New("network read failed")
			_, err := crypto.HashReader(hasher, iotest.ErrReader(errRead))
			assert.ErrorIs(t, err, errRead, "HashReader must wrap the error of the reader")
			assert.HasPrefix(t, err.Error(), "crypto: hash reader: ", "the error must name the package")
		})

		t.Run("returns the zero Digest for a reader that fails", func(t *testing.T) {
			t.Parallel()
			d, err := crypto.HashReader(hasher, iotest.ErrReader(errors.New("network read failed")))
			assert.HasError(t, err, "the test must read from a reader that fails")
			assert.True(t, d.IsZero(), "HashReader must discard the partial digest")
		})

		readers := []struct {
			name string
			give io.Reader
		}{
			{name: "closes its stream after a read that succeeds", give: bytes.NewReader([]byte("payload"))},
			{
				name: "closes its stream after a read that fails",
				give: iotest.ErrReader(errors.New("network read failed")),
			},
		}
		for _, tt := range readers {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				h := &closeCounter{Hasher: hasher}
				_, _ = crypto.HashReader(h, tt.give)
				assert.Equal(t, h.closes, 1, "HashReader must return its stream to the Hasher once")
			})
		}
	})
}

// TestHelpersAllocs checks that HashDomain and HashReader allocate nothing
// once the pools of the Hasher and of the length prefix are warm.
// MaxAllocs counts the allocations of the whole process, so the test does
// not run in parallel.
func TestHelpersAllocs(t *testing.T) {
	domain := []byte("thesmos.audit.v1")
	parts := [][]byte{[]byte("entry"), []byte("payload")}
	data := make([]byte, 4<<10)
	r := bytes.NewReader(nil)

	t.Run("HashDomain", func(t *testing.T) {
		var d crypto.Digest
		expect.MaxAllocs(t, func() { d = crypto.HashDomain(hasher, domain, parts...) }, 0,
			"HashDomain must not allocate on the warm path")
		assert.False(t, d.IsZero(), "the test must measure a digest")
	})

	t.Run("HashReader", func(t *testing.T) {
		var err error
		expect.MaxAllocs(t, func() {
			r.Reset(data)
			_, err = crypto.HashReader(hasher, r)
		}, 0, "HashReader of a bytes.Reader must not allocate on the warm path")
		assert.NoError(t, err, "the test must measure a read that succeeds")
	})
}

// BenchmarkHelpers reports the cost of HashDomain and of HashReader over
// readers of 4 KiB, 64 KiB and 1 MiB, and fails when either allocates.
func BenchmarkHelpers(b *testing.B) {
	b.Run("HashDomain", func(b *testing.B) {
		domain := []byte("thesmos.audit.v1")
		parts := [][]byte{[]byte("entry"), []byte("payload")}
		var d crypto.Digest

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			d = crypto.HashDomain(hasher, domain, parts...)
		}

		assert.False(b, d.IsZero(), "the benchmark must measure a digest")
	})

	b.Run("HashReader", func(b *testing.B) {
		for _, size := range []struct {
			name string
			n    int
		}{
			{"of 4 KiB", 4 << 10},
			{"of 64 KiB", 64 << 10},
			{"of 1 MiB", 1 << 20},
		} {
			b.Run(size.name, func(b *testing.B) {
				data := make([]byte, size.n)
				r := bytes.NewReader(nil)
				var err error

				b.SetBytes(int64(size.n))
				c := bench.Start(b).MaxAllocs(0)
				defer c.End()

				for c.Loop() {
					r.Reset(data)
					_, err = crypto.HashReader(hasher, r)
				}

				assert.NoError(b, err, "the benchmark must measure a read that succeeds")
			})
		}
	})
}

// FuzzHashDomain checks the framing of HashDomain on the inputs that a
// fuzzer finds.
func FuzzHashDomain(f *testing.F) {
	prop.Fuzz(f, hashDomainContract, hashDomainFrames)
}

// FuzzHashDomainBoundary checks that a moved boundary changes the digest
// of HashDomain on the inputs that a fuzzer finds.
func FuzzHashDomainBoundary(f *testing.F) {
	prop.Fuzz(f, hashDomainBoundaryContract, hashDomainBoundaries)
}

// FuzzHashReader checks HashReader on the inputs that a fuzzer finds.
func FuzzHashReader(f *testing.F) {
	prop.Fuzz(f, hashReaderContract, hashReaderReads)
}

// hashDomainFrames checks that HashDomain returns the digest of a stream
// over a drawn domain and each drawn part after its big-endian length.
func hashDomainFrames(c *prop.Case) {
	domain := c.Draw(prop.Bytes(prop.MaxSize(64)), "domain")
	parts := c.Draw(prop.List(prop.Bytes(prop.MaxSize(64)), prop.MaxSize(4)), "parts")
	s := hasher.NewStream()
	_, _ = s.Write(domain)
	for _, p := range parts {
		_, _ = s.Write(binary.BigEndian.AppendUint64(nil, uint64(len(p))))
		_, _ = s.Write(p)
	}
	assert.Equal(c, crypto.HashDomain(hasher, domain, parts...), s.Sum(),
		"HashDomain must equal a stream over the domain and the framed parts")
}

// hashDomainBoundaries checks that two splits of a drawn part into two
// adjacent parts, at two drawn offsets, give two digests.
func hashDomainBoundaries(c *prop.Case) {
	domain := c.Draw(prop.Bytes(prop.MaxSize(16)), "domain")
	part := c.Draw(prop.Bytes(prop.MinSize(1), prop.MaxSize(32)), "part")
	i := c.Draw(prop.Integer(0, len(part)-1), "first boundary")
	j := c.Draw(prop.Integer(i+1, len(part)), "second boundary")
	assert.NotEqual(c, crypto.HashDomain(hasher, domain, part[:i], part[i:]),
		crypto.HashDomain(hasher, domain, part[:j], part[j:]), "a moved boundary must change the digest")
}

// hashReaderReads checks that HashReader of a reader over drawn bytes
// returns the digest of the bytes.
func hashReaderReads(c *prop.Case) {
	data := c.Draw(prop.Bytes(prop.MaxSize(4096)), "data")
	got, err := crypto.HashReader(hasher, bytes.NewReader(data))
	assert.NoError(c, err, "HashReader must read a bytes.Reader to its end")
	assert.Equal(c, got, hasher.Hash(data), "HashReader must equal Hash over the same bytes")
}
