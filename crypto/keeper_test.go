// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package crypto_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/rand/constant"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

func TestGenerateKey(t *testing.T) {
	t.Parallel()

	t.Run("uses the custodian's generator when it has one", func(t *testing.T) {
		t.Parallel()
		g := cryptotest.NewKeyGeneratorStub(t)
		g.OnGenerateKey.Returns([]byte("plain"), []byte("wrapped"), nil)

		// A nil source panics on the first read, so success proves the
		// generator path never reaches for it.
		plain, wrapped, err := crypto.GenerateKey(t.Context(), g, nil, 5)
		testkit.NoError(t, err, "GenerateKey must succeed")
		testkit.Equal(t, plain, []byte("plain"), "the custodian's plaintext must be returned")
		testkit.Equal(t, wrapped, []byte("wrapped"), "the custodian's wrapped key must be returned")
	})

	t.Run("draws from the source and wraps when the custodian cannot generate", func(t *testing.T) {
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
