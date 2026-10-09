// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"testing"
	"testing/iotest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/localkey"
	"go.thesmos.sh/core/rand"
	"go.thesmos.sh/core/rand/constant"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// wrappedKey is the wrapped key that the Keepers of the tests return.
var wrappedKey = []byte("wrapped")

// decorated wraps a Keeper and returns it from UnwrapKeeper, as a
// tracing decorator does.
type decorated struct{ crypto.Keeper }

// UnwrapKeeper returns the Keeper that d wraps.
func (d decorated) UnwrapKeeper() crypto.Keeper { return d.Keeper }

// opaque wraps a Keeper and has no UnwrapKeeper, so the As functions
// cannot see past it.
type opaque struct{ crypto.Keeper }

// creator is a decorator that implements KeyCreator through the
// KeyCreator that it wraps, and returns that KeyCreator from
// UnwrapKeeper.
type creator struct{ crypto.KeyCreator }

// UnwrapKeeper returns the KeyCreator that c wraps.
func (c creator) UnwrapKeeper() crypto.Keeper { return c.KeyCreator }

// staticKeeper wraps every data key into wrappedKey and allocates
// nothing, so a measurement of GenerateKey counts GenerateKey alone.
type staticKeeper struct{}

// KeyID returns the name of the static key.
func (staticKeeper) KeyID() string { return "static" }

// Wrap returns wrappedKey.
func (staticKeeper) Wrap(context.Context, []byte) ([]byte, error) { return wrappedKey, nil }

// Unwrap returns wrapped unchanged.
func (staticKeeper) Unwrap(_ context.Context, wrapped []byte) ([]byte, error) { return wrapped, nil }

// leakySource writes 0x01 into every byte of p, keeps p, and returns one
// byte less than len(p) with an error, as a source that fails in the
// middle of a read does. It is not safe for concurrent use.
type leakySource struct{ seen []byte }

// Read fills p, keeps it in r.seen, and fails one byte short of len(p).
func (r *leakySource) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0x01
	}
	r.seen = p

	return len(p) - 1, errors.New("source failed")
}

func TestKeeper(t *testing.T) {
	t.Parallel()

	t.Run("AsDestroyer", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the Destroyer behind two decorators", func(t *testing.T) {
			t.Parallel()
			d := cryptotest.NewDestroyerStub(t)
			got, ok := crypto.AsDestroyer(decorated{decorated{d}})
			assert.True(t, ok, "a Destroyer behind decorators must be found")
			assert.Equal(t, got, crypto.Destroyer(d), "the wrapped Destroyer must be returned", assert.ByIdentity())
		})

		t.Run("reports false for a decorator without UnwrapKeeper", func(t *testing.T) {
			t.Parallel()
			_, ok := crypto.AsDestroyer(opaque{cryptotest.NewDestroyerStub(t)})
			assert.False(t, ok, "a decorator without UnwrapKeeper must end the chain")
		})

		t.Run("reports false for a nil Keeper", func(t *testing.T) {
			t.Parallel()
			_, ok := crypto.AsDestroyer(nil)
			assert.False(t, ok, "a nil Keeper must not report a capability")
		})

		t.Run("reports false for a decorator of nil", func(t *testing.T) {
			t.Parallel()
			_, ok := crypto.AsDestroyer(decorated{})
			assert.False(t, ok, "a decorator of nil must not report a capability")
		})
	})

	t.Run("AsKeyGenerator", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the KeyGenerator behind a decorator", func(t *testing.T) {
			t.Parallel()
			g := cryptotest.NewKeyGeneratorStub(t)
			got, ok := crypto.AsKeyGenerator(decorated{g})
			assert.True(t, ok, "a KeyGenerator behind a decorator must be found")
			assert.Equal(t, got, crypto.KeyGenerator(g), "the wrapped KeyGenerator must be returned",
				assert.ByIdentity())
		})

		t.Run("reports false for a Keeper that cannot generate", func(t *testing.T) {
			t.Parallel()
			_, ok := crypto.AsKeyGenerator(decorated{cryptotest.NewKeeperStub(t)})
			assert.False(t, ok, "a Keeper without the capability must not report it")
		})
	})

	t.Run("AsAADKeeper", func(t *testing.T) {
		t.Parallel()

		k := newLocalKeeper(t)
		tests := []struct {
			name string
			give crypto.Keeper
		}{
			{name: "returns the AADKeeper itself", give: k},
			{name: "returns the AADKeeper behind one decorator", give: decorated{k}},
			{name: "returns the AADKeeper behind two decorators", give: decorated{decorated{k}}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, ok := crypto.AsAADKeeper(tt.give)
				assert.True(t, ok, "the AADKeeper must be found")
				assert.Equal(t, got, crypto.AADKeeper(k), "the AADKeeper of the chain must be returned",
					assert.ByIdentity())
			})
		}

		t.Run("reports false for a Keeper without the capability", func(t *testing.T) {
			t.Parallel()
			_, ok := crypto.AsAADKeeper(decorated{cryptotest.NewKeeperStub(t)})
			assert.False(t, ok, "a Keeper without the capability must not report it")
		})

		t.Run("reports false for a decorator without UnwrapKeeper", func(t *testing.T) {
			t.Parallel()
			_, ok := crypto.AsAADKeeper(opaque{newLocalKeeper(t)})
			assert.False(t, ok, "a decorator without UnwrapKeeper must end the chain")
		})
	})

	t.Run("AsKeyCreator", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the KeyCreator behind two decorators", func(t *testing.T) {
			t.Parallel()
			k := newLocalKeeper(t)
			got, ok := crypto.AsKeyCreator(decorated{decorated{k}})
			assert.True(t, ok, "a KeyCreator behind decorators must be found")
			assert.Equal(t, got, crypto.KeyCreator(k), "the wrapped KeyCreator must be returned", assert.ByIdentity())
		})

		t.Run("returns a decorator that implements KeyCreator before the KeyCreator it wraps", func(t *testing.T) {
			t.Parallel()
			d := creator{newLocalKeeper(t)}
			got, ok := crypto.AsKeyCreator(decorated{d})
			assert.True(t, ok, "the decorating KeyCreator must be found")
			assert.Equal(t, got, crypto.KeyCreator(d), "the decorating KeyCreator must be returned")
		})

		t.Run("reports false for a Keeper without the capability", func(t *testing.T) {
			t.Parallel()
			_, ok := crypto.AsKeyCreator(decorated{cryptotest.NewKeeperStub(t)})
			assert.False(t, ok, "a Keeper without the capability must not report it")
		})

		t.Run("reports false for a decorator without UnwrapKeeper", func(t *testing.T) {
			t.Parallel()
			_, ok := crypto.AsKeyCreator(opaque{newLocalKeeper(t)})
			assert.False(t, ok, "a decorator without UnwrapKeeper must end the chain")
		})
	})

	t.Run("GenerateKey", func(t *testing.T) {
		t.Parallel()

		// The cases of the generator path pass a nil source, which panics
		// on the first read, so a call that returns proves that the path
		// reads no source.
		t.Run("returns the key of the generator of the custodian", func(t *testing.T) {
			t.Parallel()
			g := cryptotest.NewKeyGeneratorStub(t)
			g.OnGenerateKey.Returns([]byte("plain"), wrappedKey, nil)

			plain, wrapped, err := crypto.GenerateKey(t.Context(), g, nil, 5)
			assert.NoError(t, err, "GenerateKey must succeed")
			expect.Equal(t, plain, []byte("plain"), "the plaintext of the custodian must be returned")
			expect.Equal(t, wrapped, wrappedKey, "the wrapped key of the custodian must be returned")
		})

		t.Run("returns the key of a generator behind a decorator", func(t *testing.T) {
			t.Parallel()
			g := cryptotest.NewKeyGeneratorStub(t)
			g.OnGenerateKey.Returns([]byte("plain"), wrappedKey, nil)

			plain, _, err := crypto.GenerateKey(t.Context(), decorated{g}, nil, 5)
			assert.NoError(t, err, "GenerateKey must succeed")
			assert.Equal(t, plain, []byte("plain"), "the plaintext of the decorated custodian must be returned")
		})

		t.Run("wraps a key read from the source for a custodian that cannot generate", func(t *testing.T) {
			t.Parallel()
			k := cryptotest.NewKeeperStub(t)
			var seen []byte
			k.OnWrap.Func(func(_ context.Context, dek []byte) ([]byte, error) {
				seen = bytes.Clone(dek)

				return wrappedKey, nil
			})

			plain, wrapped, err := crypto.GenerateKey(t.Context(), k, constant.New(0x0101010101010101), 8)
			assert.NoError(t, err, "GenerateKey must succeed")
			expect.Equal(t, plain, bytes.Repeat([]byte{0x01}, 8), "the plaintext must be the bytes of the source")
			expect.Equal(t, seen, plain, "Wrap must receive the plaintext")
			expect.Equal(t, wrapped, wrappedKey, "the output of Wrap must be returned")
		})

		t.Run("returns ErrKeySize for a size that is not positive", func(t *testing.T) {
			t.Parallel()
			k := cryptotest.NewKeeperStub(t)
			prop.ErrorIs(t, func(size int) error {
				_, _, err := crypto.GenerateKey(t.Context(), k, randcrypto.New(), size)

				return err
			}, crypto.ErrKeySize, "GenerateKey must refuse a size that is not positive",
				prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0), prop.Example(-1))
		})

		t.Run("returns the error of the source", func(t *testing.T) {
			t.Parallel()
			failing := randcrypto.NewWithReader(iotest.ErrReader(io.ErrUnexpectedEOF))
			_, _, err := crypto.GenerateKey(t.Context(), cryptotest.NewKeeperStub(t), failing, 8)
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "the failure of the source must be returned")
		})

		t.Run("returns no key with the error of the source", func(t *testing.T) {
			t.Parallel()
			failing := randcrypto.NewWithReader(iotest.ErrReader(io.ErrUnexpectedEOF))
			plain, wrapped, err := crypto.GenerateKey(t.Context(), cryptotest.NewKeeperStub(t), failing, 8)
			assert.HasError(t, err, "the test must read from a source that fails")
			expect.Nil(t, plain, "no plaintext may accompany an error")
			expect.Nil(t, wrapped, "no wrapped key may accompany an error")
		})

		t.Run("zeroes the bytes that the source wrote when the source fails", func(t *testing.T) {
			t.Parallel()
			source := &leakySource{}
			r := randcrypto.NewWithReader(source)
			_, _, err := crypto.GenerateKey(t.Context(), cryptotest.NewKeeperStub(t), r, 8)
			assert.HasError(t, err, "the test must read from a source that fails")
			assert.Equal(t, source.seen, make([]byte, 8),
				"the bytes of the source must be zeroed before GenerateKey returns")
		})

		t.Run("returns the error of Wrap", func(t *testing.T) {
			t.Parallel()
			errWrap := errors.New("custodian unavailable")
			k := cryptotest.NewKeeperStub(t)
			k.OnWrap.Returns(nil, errWrap)

			_, _, err := crypto.GenerateKey(t.Context(), k, constant.New(0x0101010101010101), 8)
			assert.ErrorIs(t, err, errWrap, "the failure of the custodian must be returned")
		})

		t.Run("returns no key with the error of Wrap", func(t *testing.T) {
			t.Parallel()
			k := cryptotest.NewKeeperStub(t)
			k.OnWrap.Returns(wrappedKey, errors.New("custodian unavailable"))

			plain, wrapped, err := crypto.GenerateKey(t.Context(), k, constant.New(0x0101010101010101), 8)
			assert.HasError(t, err, "the test must wrap with a custodian that fails")
			expect.Nil(t, plain, "no plaintext may accompany an error")
			expect.Nil(t, wrapped, "no wrapped key may accompany an error")
		})

		t.Run("zeroes the drawn key when Wrap fails", func(t *testing.T) {
			t.Parallel()
			k := cryptotest.NewKeeperStub(t)
			var seen []byte
			k.OnWrap.Func(func(_ context.Context, dek []byte) ([]byte, error) {
				seen = dek

				return nil, errors.New("custodian unavailable")
			})

			_, _, err := crypto.GenerateKey(t.Context(), k, constant.New(0x0101010101010101), 8)
			assert.HasError(t, err, "the test must wrap with a custodian that fails")
			assert.Equal(t, seen, make([]byte, 8), "the drawn key must be zeroed before GenerateKey returns")
		})
	})
}

// TestKeeperAllocs checks the allocation contract of the As functions
// through two decorators, and of the source path of GenerateKey over a
// Keeper that allocates nothing. MaxAllocs counts the allocations of the
// whole process, so the test does not run in parallel.
func TestKeeperAllocs(t *testing.T) {
	// Each chain is converted to the interface once, because a conversion
	// of a decorated value allocates.
	var local crypto.Keeper = decorated{decorated{newLocalKeeper(t)}}
	var destroyer crypto.Keeper = decorated{decorated{cryptotest.NewDestroyerStub(t)}}
	var generator crypto.Keeper = decorated{decorated{cryptotest.NewKeyGeneratorStub(t)}}

	tests := []struct {
		name string
		find func() bool
	}{
		{name: "AsDestroyer", find: func() bool { _, ok := crypto.AsDestroyer(destroyer); return ok }},
		{name: "AsKeyGenerator", find: func() bool { _, ok := crypto.AsKeyGenerator(generator); return ok }},
		{name: "AsAADKeeper", find: func() bool { _, ok := crypto.AsAADKeeper(local); return ok }},
		{name: "AsKeyCreator", find: func() bool { _, ok := crypto.AsKeyCreator(local); return ok }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ok bool
			expect.MaxAllocs(t, func() { ok = tt.find() }, 0, "a search of the chain must not allocate")
			assert.True(t, ok, "the test must measure a search that finds the capability")
		})
	}

	t.Run("GenerateKey", func(t *testing.T) {
		// A conversion of constant.Rand to the interface allocates, so it
		// happens once, outside the measured call.
		var r rand.Rand = constant.New(0x0101010101010101)
		var plain []byte
		var err error
		expect.MaxAllocs(t, func() { plain, _, err = crypto.GenerateKey(t.Context(), staticKeeper{}, r, 32) }, 1,
			"the source path must allocate only the plaintext")
		assert.NoError(t, err, "the test must measure a GenerateKey that succeeds")
		assert.Length(t, plain, 32, "the test must measure a key of 32 bytes")
	})
}

// BenchmarkKeeper reports the cost of each As function through two
// decorators and of the source path of GenerateKey, and fails when one of
// them allocates more than TestKeeperAllocs allows.
func BenchmarkKeeper(b *testing.B) {
	var local crypto.Keeper = decorated{decorated{newLocalKeeper(b)}}

	b.Run("AsDestroyer", func(b *testing.B) {
		var k crypto.Keeper = decorated{decorated{cryptotest.NewDestroyerStub(b)}}
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, ok = crypto.AsDestroyer(k)
		}

		assert.True(b, ok, "the benchmark must measure a search that finds the capability")
	})

	b.Run("AsKeyGenerator", func(b *testing.B) {
		var k crypto.Keeper = decorated{decorated{cryptotest.NewKeyGeneratorStub(b)}}
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, ok = crypto.AsKeyGenerator(k)
		}

		assert.True(b, ok, "the benchmark must measure a search that finds the capability")
	})

	b.Run("AsAADKeeper", func(b *testing.B) {
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, ok = crypto.AsAADKeeper(local)
		}

		assert.True(b, ok, "the benchmark must measure a search that finds the capability")
	})

	b.Run("AsKeyCreator", func(b *testing.B) {
		var ok bool

		c := bench.Start(b).MaxAllocs(0)
		defer c.End()

		for c.Loop() {
			_, ok = crypto.AsKeyCreator(local)
		}

		assert.True(b, ok, "the benchmark must measure a search that finds the capability")
	})

	b.Run("GenerateKey", func(b *testing.B) {
		var r rand.Rand = constant.New(0x0101010101010101)
		var err error

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			_, _, err = crypto.GenerateKey(b.Context(), staticKeeper{}, r, 32)
		}

		assert.NoError(b, err, "the benchmark must measure a GenerateKey that succeeds")
	})
}

// newLocalKeeper returns a localkey Keeper, which implements AADKeeper
// and KeyCreator, under a root key of zero bytes and a fake clock. It
// fails tb when localkey refuses the key.
func newLocalKeeper(tb testing.TB) *localkey.Keeper {
	tb.Helper()

	k, err := localkey.New("crypto-test/local", make([]byte, localkey.RootKeySize), randcrypto.New(),
		fake.New(time.Unix(0, 0).UTC()))
	assert.NoError(tb, err, "localkey.New must accept a root key of RootKeySize bytes")

	return k
}
