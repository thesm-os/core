// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/localkey"
	"go.thesmos.sh/core/rand/constant"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// newLocalKeeper returns a local keeper, which implements AADKeeper.
func newLocalKeeper(tb testing.TB) *localkey.Keeper {
	tb.Helper()

	k, err := localkey.New("crypto-test/local", make([]byte, localkey.RootKeySize), randcrypto.New(),
		fake.New(time.Unix(0, 0).UTC()))
	testkit.NoError(tb, err, "localkey.New must accept a 32-byte root key")

	return k
}

// decorated wraps a Keeper and returns it from UnwrapKeeper, as a
// tracing decorator does.
type decorated struct{ crypto.Keeper }

func (d decorated) UnwrapKeeper() crypto.Keeper { return d.Keeper }

// opaque wraps a Keeper and has no UnwrapKeeper, so the As functions
// cannot see past it.
type opaque struct{ crypto.Keeper }

func TestAsDestroyer(t *testing.T) {
	t.Parallel()

	t.Run("returns the Destroyer behind two decorators", func(t *testing.T) {
		t.Parallel()
		d := cryptotest.NewDestroyerStub(t)
		got, ok := crypto.AsDestroyer(decorated{decorated{d}})
		testkit.True(t, ok, "a Destroyer behind decorators must be found")
		testkit.True(t, got == crypto.Destroyer(d), "the wrapped Destroyer must be returned")
	})

	t.Run("reports false for a decorator without UnwrapKeeper", func(t *testing.T) {
		t.Parallel()
		_, ok := crypto.AsDestroyer(opaque{cryptotest.NewDestroyerStub(t)})
		testkit.False(t, ok, "a decorator without UnwrapKeeper must end the chain")
	})

	t.Run("reports false for a nil Keeper and a decorator of nil", func(t *testing.T) {
		t.Parallel()
		_, ok := crypto.AsDestroyer(nil)
		testkit.False(t, ok, "a nil Keeper must not report a capability")

		_, ok = crypto.AsDestroyer(decorated{})
		testkit.False(t, ok, "a decorator of nil must not report a capability")
	})
}

func TestAsKeyGenerator(t *testing.T) {
	t.Parallel()

	t.Run("returns the KeyGenerator behind a decorator", func(t *testing.T) {
		t.Parallel()
		g := cryptotest.NewKeyGeneratorStub(t)
		got, ok := crypto.AsKeyGenerator(decorated{g})
		testkit.True(t, ok, "a KeyGenerator behind a decorator must be found")
		testkit.True(t, got == crypto.KeyGenerator(g), "the wrapped KeyGenerator must be returned")
	})

	t.Run("reports false for a Keeper that cannot generate", func(t *testing.T) {
		t.Parallel()
		_, ok := crypto.AsKeyGenerator(decorated{cryptotest.NewKeeperStub(t)})
		testkit.False(t, ok, "a Keeper without the capability must not report it")
	})
}

func TestAsAADKeeper(t *testing.T) {
	t.Parallel()

	t.Run("returns the AADKeeper itself, behind one decorator and behind two", func(t *testing.T) {
		t.Parallel()
		k := newLocalKeeper(t)
		for name, chain := range map[string]crypto.Keeper{
			"itself":         k,
			"one decorator":  decorated{k},
			"two decorators": decorated{decorated{k}},
		} {
			got, ok := crypto.AsAADKeeper(chain)
			testkit.True(t, ok, "an AADKeeper behind "+name+" must be found")
			testkit.True(t, got == crypto.AADKeeper(k), "the AADKeeper behind "+name+" must be returned")
		}
	})

	t.Run("reports false for a Keeper without the capability", func(t *testing.T) {
		t.Parallel()
		_, ok := crypto.AsAADKeeper(decorated{cryptotest.NewKeeperStub(t)})
		testkit.False(t, ok, "a Keeper without the capability must not report it")
	})

	t.Run("reports false for a decorator without UnwrapKeeper", func(t *testing.T) {
		t.Parallel()
		_, ok := crypto.AsAADKeeper(opaque{newLocalKeeper(t)})
		testkit.False(t, ok, "a decorator without UnwrapKeeper must end the chain")
	})
}

// BenchmarkAsAADKeeper reports the cost and the allocations of
// AsAADKeeper through two decorators.
func BenchmarkAsAADKeeper(b *testing.B) {
	var k crypto.Keeper = decorated{decorated{newLocalKeeper(b)}}
	b.ReportAllocs()

	for b.Loop() {
		_, _ = crypto.AsAADKeeper(k)
	}
}

// TestKeeperZeroAlloc enforces the allocation contract of the As
// functions through two decorators. testing.AllocsPerRun reads a
// process-global malloc counter, so this test does not call
// t.Parallel.
//
//nolint:paralleltest // see comment above
func TestKeeperZeroAlloc(t *testing.T) {
	var k crypto.Keeper = decorated{decorated{cryptotest.NewDestroyerStub(t)}}

	for name, fn := range map[string]func(){
		"AsDestroyer":    func() { _, _ = crypto.AsDestroyer(k) },
		"AsKeyGenerator": func() { _, _ = crypto.AsKeyGenerator(k) },
	} {
		t.Run(name, func(t *testing.T) {
			testkit.Equal(t, testing.AllocsPerRun(100, fn), float64(0), name+" must not allocate")
		})
	}
}

func BenchmarkAsKeyGenerator(b *testing.B) {
	var k crypto.Keeper = decorated{decorated{cryptotest.NewKeyGeneratorStub(b)}}
	b.ReportAllocs()

	for b.Loop() {
		_, _ = crypto.AsKeyGenerator(k)
	}
}

// TestGenerateKey covers both paths. The generator tests pass a nil
// source, which panics on the first read, so success proves that the
// generator path does not read the source.
func TestGenerateKey(t *testing.T) {
	t.Parallel()

	t.Run("uses the custodian's generator when it has one", func(t *testing.T) {
		t.Parallel()
		g := cryptotest.NewKeyGeneratorStub(t)
		g.OnGenerateKey.Returns([]byte("plain"), []byte("wrapped"), nil)

		plain, wrapped, err := crypto.GenerateKey(t.Context(), g, nil, 5)
		testkit.NoError(t, err, "GenerateKey must succeed")
		testkit.Equal(t, plain, []byte("plain"), "the custodian's plaintext must be returned")
		testkit.Equal(t, wrapped, []byte("wrapped"), "the custodian's wrapped key must be returned")
	})

	t.Run("uses a generator behind a decorator", func(t *testing.T) {
		t.Parallel()
		g := cryptotest.NewKeyGeneratorStub(t)
		g.OnGenerateKey.Returns([]byte("plain"), []byte("wrapped"), nil)

		plain, _, err := crypto.GenerateKey(t.Context(), decorated{g}, nil, 5)
		testkit.NoError(t, err, "GenerateKey must succeed")
		testkit.Equal(t, plain, []byte("plain"), "the decorated custodian's plaintext must be returned")
	})

	t.Run("reads from the source and wraps when the custodian cannot generate", func(t *testing.T) {
		t.Parallel()
		k := cryptotest.NewKeeperStub(t)
		var seen []byte
		k.OnWrap.Func(func(_ context.Context, dek []byte) ([]byte, error) {
			seen = bytes.Clone(dek)

			return []byte("wrapped"), nil
		})

		plain, wrapped, err := crypto.GenerateKey(t.Context(), k, constant.New(0x0101010101010101), 8)
		testkit.NoError(t, err, "GenerateKey must succeed")
		testkit.Equal(t, plain, bytes.Repeat([]byte{0x01}, 8), "the plaintext must be the source's bytes")
		testkit.Equal(t, seen, plain, "Wrap must receive the plaintext")
		testkit.Equal(t, wrapped, []byte("wrapped"), "Wrap's output must be returned")
	})

	t.Run("rejects a non-positive size", func(t *testing.T) {
		t.Parallel()
		for _, size := range []int{0, -1} {
			_, _, err := crypto.GenerateKey(t.Context(), cryptotest.NewKeeperStub(t), randcrypto.New(), size)
			testkit.ErrorIs(t, err, crypto.ErrKeySize, "a non-positive size must be refused")
		}
	})

	t.Run("returns the source's failure and no key material", func(t *testing.T) {
		t.Parallel()
		failing := randcrypto.NewWithReader(&testkit.FailingReader{
			Source: bytes.NewReader(nil), Err: io.ErrUnexpectedEOF,
		})

		plain, wrapped, err := crypto.GenerateKey(t.Context(), cryptotest.NewKeeperStub(t), failing, 8)
		testkit.ErrorIs(t, err, io.ErrUnexpectedEOF, "the entropy failure must be returned")
		testkit.Equal(t, plain, []byte(nil), "no plaintext may accompany an error")
		testkit.Equal(t, wrapped, []byte(nil), "no wrapped key may accompany an error")
	})

	t.Run("returns the custodian's failure and zeroes the drawn bytes", func(t *testing.T) {
		t.Parallel()
		k := cryptotest.NewKeeperStub(t)
		var seen []byte
		k.OnWrap.Func(func(_ context.Context, dek []byte) ([]byte, error) {
			seen = dek

			return nil, testkit.TestError("custodian unavailable")
		})

		plain, _, err := crypto.GenerateKey(t.Context(), k, constant.New(0x0101010101010101), 8)
		testkit.Error(t, err, "the custodian's failure must be returned")
		testkit.Equal(t, plain, []byte(nil), "no plaintext may accompany an error")
		testkit.Equal(t, seen, make([]byte, 8), "the drawn key must be zeroed before returning")
	})
}
