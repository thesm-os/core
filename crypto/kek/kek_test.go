// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package kek_test

import (
	"bytes"
	"context"
	"crypto/fips140"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.thesmos.sh/core/clock/fake"
	"go.thesmos.sh/core/coretest/cryptotest"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/kek"
	"go.thesmos.sh/core/crypto/localkey"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/rand"
	randcrypto "go.thesmos.sh/core/rand/crypto"
	"go.thesmos.sh/core/rand/seeded"
)

// The key IDs of the KEKs these tests create, and of their parent.
const (
	keyIDA      = "tenant-a/2026-09-25"
	keyIDB      = "tenant-b/2026-09-25"
	parentKeyID = "kek-test/parent"
)

// kekDomainVersion pins the domain version of the framed key ID.
const kekDomainVersion = 1

// The operations that the history of the race between Close and Wrap
// records.
const (
	opClose = "close"
	opWrap  = "wrap"
)

// wrappedDEKSize pins the length of a wrapped 32-byte DEK: a 16-byte
// salt and an AES-256-GCM envelope that adds 41 bytes.
const wrappedDEKSize = 89

// childTimeout is the -test.timeout of the child process of
// TestFIPSOnlyMode. The child runs one subtest in milliseconds. The
// bound ends a child whose parent has died, which no context of the
// parent can cancel.
const childTimeout = 30 * time.Second

// vectorsPath is the file of the recorded KEK vector.
const vectorsPath = "testdata/vectors.txt"

// The keywords of the vector file, and the seed of its KEK.
const (
	vectorComment = "#"
	vectorKEK     = "kek"
	vectorKeyID   = "keyid"
	vectorRecord  = "record"
	vectorDEK     = "dek"
	vectorWrapped = "wrapped"
	vectorSeed    = 7
)

var (
	// dek is the data key these tests wrap.
	dek = bytes.Repeat([]byte{0x5A}, 32)

	// failingSource fails every read with io.ErrUnexpectedEOF. Its reader
	// has no state, so the tests share it.
	failingSource = randcrypto.NewWithReader(iotest.ErrReader(io.ErrUnexpectedEOF))
)

// generatorSpy is a parent whose GenerateKey fails the test, and which
// counts its Wrap calls.
type generatorSpy struct {
	*localkey.Keeper

	t     *testing.T
	wraps atomic.Int32
}

// Wrap counts the call and wraps dek with the embedded Keeper.
func (s *generatorSpy) Wrap(ctx context.Context, dek []byte) ([]byte, error) {
	s.wraps.Add(1)

	return s.Keeper.Wrap(ctx, dek)
}

// GenerateKey fails the test, because no code under test may call it.
func (s *generatorSpy) GenerateKey(context.Context, int) ([]byte, []byte, error) {
	s.t.Error("the parent's GenerateKey must not be called")

	return nil, nil, io.ErrUnexpectedEOF
}

// recordingParent is a parent that keeps the record that it wraps and the
// record that its Unwrap returns, so a test can read what the package
// leaves in them. It is not safe for concurrent use.
type recordingParent struct {
	*localkey.Keeper

	wrapped   []byte
	unwrapped []byte
}

// Wrap keeps record and wraps it with the embedded Keeper.
func (p *recordingParent) Wrap(ctx context.Context, record []byte) ([]byte, error) {
	p.wrapped = record

	return p.Keeper.Wrap(ctx, record)
}

// Unwrap unwraps wrapped with the embedded Keeper and keeps the record
// that it returns.
func (p *recordingParent) Unwrap(ctx context.Context, wrapped []byte) ([]byte, error) {
	record, err := p.Keeper.Unwrap(ctx, wrapped)
	p.unwrapped = record

	return record, err
}

// TestKeeperContract runs the contract suite of crypto.Keeper with a
// second instance over the same record and a Keeper of another key ID.
func TestKeeperContract(t *testing.T) {
	t.Parallel()

	parent := newParent(t)
	_, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)
	_, other := mustGenerate(t, parent, randcrypto.New(), keyIDB)

	factory := func() crypto.Keeper { return mustNew(t, parent, keyIDA, wrapped) }
	cryptotest.AssertKeeperContract(t, factory,
		append(cryptotest.KeeperContractAssertions(),
			cryptotest.KeeperCrossInstanceAssertion(factory),
			cryptotest.KeeperForeignKeyAssertion(func() crypto.Keeper {
				return mustNew(t, parent, keyIDB, other)
			}),
		)...,
	)
}

func TestKeeper(t *testing.T) {
	t.Parallel()

	v := loadVector(t)
	vectorKeyIDValue := v[vectorKeyID]
	rec := decodeHex(t, v[vectorRecord])

	t.Run("implements no optional capability", func(t *testing.T) {
		t.Parallel()
		k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
		_, ok := crypto.AsKeyGenerator(k)
		expect.False(t, ok, "the Keeper must not report a KeyGenerator")
		_, ok = crypto.AsDestroyer(k)
		expect.False(t, ok, "the Keeper must not report a Destroyer")
		_, ok = crypto.AsAADKeeper(k)
		expect.False(t, ok, "the Keeper must not report an AADKeeper")
	})

	t.Run("serves crypto.GenerateKey without a call to the parent", func(t *testing.T) {
		t.Parallel()
		spy := &generatorSpy{Keeper: newParent(t), t: t}
		k, _ := mustGenerate(t, spy, randcrypto.New(), keyIDA)
		_, _, err := crypto.GenerateKey(t.Context(), k, randcrypto.New(), 32)
		assert.NoError(t, err, "GenerateKey must succeed")
		assert.Equal(t, spy.wraps.Load(), int32(1), "GenerateKey must not call the parent")
	})

	t.Run("Generate", func(t *testing.T) {
		t.Parallel()

		t.Run("wraps the record of a KEK read from the source", func(t *testing.T) {
			t.Parallel()
			parent := newParent(t)
			_, wrapped := mustGenerate(t, parent, seeded.New(1), keyIDA)
			got, err := parent.Unwrap(t.Context(), wrapped)
			assert.NoError(t, err, "the parent must unwrap the record")
			assert.Equal(t, got, record(seededKEK(t, 1), keyIDA), "the record must be the KEK and its binding")
		})

		// The record is a persisted layout, so a change to the binding
		// must fail here.
		t.Run("wraps the recorded record for the seed of the vector", func(t *testing.T) {
			t.Parallel()
			parent := newParent(t)
			_, wrapped := mustGenerate(t, parent, seeded.New(vectorSeed), vectorKeyIDValue)
			got, err := parent.Unwrap(t.Context(), wrapped)
			assert.NoError(t, err, "the parent must unwrap the record")
			assert.Equal(t, got, rec, "Generate must wrap the recorded record")
		})

		t.Run("has a recorded record of the documented layout", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, rec, record(seededKEK(t, vectorSeed), vectorKeyIDValue),
				"the record must follow the documented layout")
		})

		t.Run("returns a Keeper whose DEKs a Keeper from New unwraps", func(t *testing.T) {
			t.Parallel()
			parent := newParent(t)
			k, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)
			reader := mustNew(t, parent, keyIDA, wrapped)
			assert.RoundTrip(t, func(d []byte) ([]byte, error) { return k.Wrap(t.Context(), d) },
				func(sealed []byte) ([]byte, error) { return reader.Unwrap(t.Context(), sealed) },
				dek, "a Keeper from New must unwrap the DEK that the Keeper of Generate wrapped")
		})

		t.Run("zeroes the record that it wrapped", func(t *testing.T) {
			t.Parallel()
			parent := &recordingParent{Keeper: newParent(t)}
			mustGenerate(t, parent, randcrypto.New(), keyIDA)
			assert.Equal(t, parent.wrapped, make([]byte, kek.RecordSize),
				"the record must be zeroed before Generate returns")
		})

		t.Run("wraps the record with one call to Wrap", func(t *testing.T) {
			t.Parallel()
			spy := &generatorSpy{Keeper: newParent(t), t: t}
			mustGenerate(t, spy, randcrypto.New(), keyIDA)
			assert.Equal(t, spy.wraps.Load(), int32(1), "Generate must not call the KeyGenerator of the parent")
		})

		t.Run("returns ErrKeyID for an empty key ID", func(t *testing.T) {
			t.Parallel()
			_, _, err := kek.Generate(t.Context(), newParent(t), randcrypto.New(), "")
			assert.ErrorIs(t, err, crypto.ErrKeyID, "an empty key ID must be refused")
		})

		t.Run("returns the error of the source", func(t *testing.T) {
			t.Parallel()
			_, _, err := kek.Generate(t.Context(), newParent(t), failingSource, keyIDA)
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "Generate must return the error of the source")
		})

		t.Run("returns the error of the parent", func(t *testing.T) {
			t.Parallel()
			_, _, err := kek.Generate(t.Context(), destroyedParent(t), randcrypto.New(), keyIDA)
			assert.ErrorIs(t, err, crypto.ErrKeyDestroyed, "Generate must return the error of the parent")
		})

		t.Run("returns no Keeper with an error", func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name   string
				parent crypto.Keeper
				r      rand.Rand
				keyID  string
			}{
				{name: "an empty key ID", parent: newParent(t), r: randcrypto.New()},
				{name: "a source that fails", parent: newParent(t), r: failingSource, keyID: keyIDA},
				{name: "a destroyed parent", parent: destroyedParent(t), r: randcrypto.New(), keyID: keyIDA},
			} {
				k, wrapped, err := kek.Generate(t.Context(), tt.parent, tt.r, tt.keyID)
				assert.HasError(t, err, "the test must generate with "+tt.name)
				expect.Nil(t, k, "no Keeper may accompany the error of "+tt.name)
				expect.Nil(t, wrapped, "no record may accompany the error of "+tt.name)
			}
		})
	})

	t.Run("GenerateAAD", func(t *testing.T) {
		t.Parallel()

		parent := newParent(t)
		_, wrappedAAD, err := kek.GenerateAAD(t.Context(), parent, randcrypto.New(), keyIDA)
		assert.NoError(t, err, "GenerateAAD must succeed")

		t.Run("returns a record that the parent unwraps with the key ID", func(t *testing.T) {
			t.Parallel()
			_, err := parent.UnwrapAAD(t.Context(), wrappedAAD, []byte(keyIDA))
			assert.NoError(t, err, "the parent must unwrap the record with the key ID")
		})

		t.Run("returns a record that the parent does not unwrap without the key ID", func(t *testing.T) {
			t.Parallel()
			_, err := parent.Unwrap(t.Context(), wrappedAAD)
			assert.HasError(t, err, "the parent must not unwrap the record without the key ID")
		})

		t.Run("returns ErrKeyID for an empty key ID", func(t *testing.T) {
			t.Parallel()
			_, _, err := kek.GenerateAAD(t.Context(), parent, randcrypto.New(), "")
			assert.ErrorIs(t, err, crypto.ErrKeyID, "GenerateAAD must refuse an empty key ID")
		})
	})

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		parent := newParent(t)
		_, wrappedA := mustGenerate(t, parent, randcrypto.New(), keyIDA)
		_, wrappedB := mustGenerate(t, parent, randcrypto.New(), keyIDB)

		t.Run("returns ErrKeyIDMismatch for a record bound to another key ID", func(t *testing.T) {
			t.Parallel()
			_, err := kek.New(t.Context(), parent, randcrypto.New(), keyIDA, wrappedB)
			assert.ErrorIs(t, err, kek.ErrKeyIDMismatch, "a swapped record must be refused")
		})

		t.Run("returns an error of class Integrity for a record bound to another key ID", func(t *testing.T) {
			t.Parallel()
			_, err := kek.New(t.Context(), parent, randcrypto.New(), keyIDA, wrappedB)
			assert.Equal(t, errs.Classify(err), errs.Integrity, "ErrKeyIDMismatch must classify as Integrity")
		})

		t.Run("returns no Keeper for a record bound to another key ID", func(t *testing.T) {
			t.Parallel()
			k, err := kek.New(t.Context(), parent, randcrypto.New(), keyIDA, wrappedB)
			assert.HasError(t, err, "the test must open a swapped record")
			assert.Nil(t, k, "a refusal must return no Keeper")
		})

		t.Run("returns ErrKeySize for a record of another length", func(t *testing.T) {
			t.Parallel()
			short, err := parent.Wrap(t.Context(), make([]byte, kek.KeySize))
			assert.NoError(t, err, "Wrap must succeed")
			_, err = kek.New(t.Context(), parent, randcrypto.New(), keyIDA, short)
			assert.ErrorIs(t, err, crypto.ErrKeySize, "a record of another length must be refused")
		})

		t.Run("returns the error of the parent", func(t *testing.T) {
			t.Parallel()
			destroyed := newParent(t)
			_, wrapped := mustGenerate(t, destroyed, randcrypto.New(), keyIDA)
			_, err := destroyed.Destroy(t.Context(), parentKeyID)
			assert.NoError(t, err, "Destroy must succeed")
			_, err = kek.New(t.Context(), destroyed, randcrypto.New(), keyIDA, wrapped)
			assert.ErrorIs(t, err, crypto.ErrKeyDestroyed, "New must return the error of the parent")
		})

		t.Run("returns an error for a record that the parent cannot unwrap", func(t *testing.T) {
			t.Parallel()
			_, err := kek.New(t.Context(), parent, randcrypto.New(), keyIDA, wrappedA[:len(wrappedA)-1])
			assert.HasError(t, err, "a record that the parent cannot unwrap must fail")
		})

		t.Run("zeroes the record that the parent unwrapped", func(t *testing.T) {
			t.Parallel()
			recording := &recordingParent{Keeper: newParent(t)}
			_, wrapped := mustGenerate(t, recording, randcrypto.New(), keyIDA)
			mustNew(t, recording, keyIDA, wrapped)
			assert.Equal(t, recording.unwrapped, make([]byte, kek.RecordSize),
				"the record must be zeroed before New returns")
		})

		t.Run("returns ErrKeyID for an empty key ID", func(t *testing.T) {
			t.Parallel()
			_, err := kek.New(t.Context(), parent, randcrypto.New(), "", wrappedA)
			assert.ErrorIs(t, err, crypto.ErrKeyID, "an empty key ID must be refused")
		})

		t.Run("returns an error for a record from GenerateAAD", func(t *testing.T) {
			t.Parallel()
			_, wrappedAAD, err := kek.GenerateAAD(t.Context(), parent, randcrypto.New(), keyIDA)
			assert.NoError(t, err, "GenerateAAD must succeed")
			_, err = kek.New(t.Context(), parent, randcrypto.New(), keyIDA, wrappedAAD)
			assert.HasError(t, err, "New must not open a record from GenerateAAD")
		})
	})

	t.Run("NewAAD", func(t *testing.T) {
		t.Parallel()

		parent := newParent(t)
		_, wrappedAAD, err := kek.GenerateAAD(t.Context(), parent, randcrypto.New(), keyIDA)
		assert.NoError(t, err, "GenerateAAD must succeed")

		t.Run("returns a Keeper for a record from GenerateAAD", func(t *testing.T) {
			t.Parallel()
			k, err := kek.NewAAD(t.Context(), parent, randcrypto.New(), keyIDA, wrappedAAD)
			assert.NoError(t, err, "NewAAD must open a record from GenerateAAD")
			assert.NoError(t, k.Close(), "Close must succeed")
		})

		t.Run("returns an error for a record from Generate", func(t *testing.T) {
			t.Parallel()
			_, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)
			_, err := kek.NewAAD(t.Context(), parent, randcrypto.New(), keyIDA, wrapped)
			assert.HasError(t, err, "NewAAD must not open a record from Generate")
		})

		t.Run("returns the error of the parent", func(t *testing.T) {
			t.Parallel()
			destroyed := newParent(t)
			_, wrapped, err := kek.GenerateAAD(t.Context(), destroyed, randcrypto.New(), keyIDA)
			assert.NoError(t, err, "GenerateAAD must succeed")
			_, err = destroyed.Destroy(t.Context(), parentKeyID)
			assert.NoError(t, err, "Destroy must succeed")
			_, err = kek.NewAAD(t.Context(), destroyed, randcrypto.New(), keyIDA, wrapped)
			assert.ErrorIs(t, err, crypto.ErrKeyDestroyed, "NewAAD must return the error of the parent")
		})

		t.Run("returns the error of the parent for another key ID", func(t *testing.T) {
			t.Parallel()
			_, err := kek.NewAAD(t.Context(), parent, randcrypto.New(), keyIDB, wrappedAAD)
			assert.That(t, err).
				HasError("the parent must refuse the record under another key ID").
				ErrorIsNot(kek.ErrKeyIDMismatch, "the refusal must come from the parent")
		})

		t.Run("returns ErrKeyID for an empty key ID", func(t *testing.T) {
			t.Parallel()
			_, err := kek.NewAAD(t.Context(), parent, randcrypto.New(), "", wrappedAAD)
			assert.ErrorIs(t, err, crypto.ErrKeyID, "NewAAD must refuse an empty key ID")
		})
	})

	t.Run("Wrap", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a wrapped DEK of 89 bytes for a DEK of 32 bytes", func(t *testing.T) {
			t.Parallel()
			k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
			sealed, err := k.Wrap(t.Context(), dek)
			assert.NoError(t, err, "Wrap must succeed")
			assert.Length(t, sealed, wrappedDEKSize, "a wrapped DEK must be a salt and an envelope")
		})

		t.Run("binds the key ID into every wrapped DEK", func(t *testing.T) {
			t.Parallel()
			parent := newParent(t)
			k, _ := mustGenerate(t, parent, seeded.New(1), keyIDA)
			other, err := parent.Wrap(t.Context(), record(seededKEK(t, 1), keyIDB))
			assert.NoError(t, err, "Wrap must succeed")
			sealed, err := k.Wrap(t.Context(), dek)
			assert.NoError(t, err, "Wrap must succeed")
			_, err = mustNew(t, parent, keyIDB, other).Unwrap(t.Context(), sealed)
			assert.HasError(t, err, "a DEK wrapped under one key ID must not unwrap under another")
		})

		t.Run("returns the error of the source when it derives a wrapping key", func(t *testing.T) {
			t.Parallel()
			parent := newParent(t)
			_, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)
			k, err := kek.New(t.Context(), parent, failingSource, keyIDA, wrapped)
			assert.NoError(t, err, "New must not read the source")
			_, err = k.Wrap(t.Context(), dek)
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "Wrap must return the error of the source")
		})

		t.Run("returns ErrClosed after Close", func(t *testing.T) {
			t.Parallel()
			k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
			assert.FailsAfterClose(t, k.Close, func() error {
				_, err := k.Wrap(t.Context(), dek)

				return err
			}, kek.ErrClosed, "Wrap after Close must return ErrClosed")
		})

		t.Run("returns an error of class Invalid after Close", func(t *testing.T) {
			t.Parallel()
			k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
			assert.NoError(t, k.Close(), "Close must succeed")
			_, err := k.Wrap(t.Context(), dek)
			assert.Equal(t, errs.Classify(err), errs.Invalid, "ErrClosed must classify as Invalid")
		})
	})

	t.Run("Unwrap", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrCiphertextShort for material without an envelope after its salt", func(t *testing.T) {
			t.Parallel()
			k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
			prop.ErrorIs(t, func(n int) error {
				_, err := k.Unwrap(t.Context(), make([]byte, n))

				return err
			}, crypto.ErrCiphertextShort, "material without an envelope must be refused",
				prop.Using(prop.Integer(0, kek.SaltSize)), prop.Example(0), prop.Example(kek.SaltSize))
		})

		// The DEK, the record and the wrapped DEK are persisted, so a
		// change to the derivation or the layout must fail here.
		t.Run("returns the recorded DEK of the recorded wrapped DEK", func(t *testing.T) {
			t.Parallel()
			parent := newParent(t)
			wrapped, err := parent.Wrap(t.Context(), rec)
			assert.NoError(t, err, "the parent must wrap the record")
			k := mustNew(t, parent, vectorKeyIDValue, wrapped)
			got, err := k.Unwrap(t.Context(), decodeHex(t, v[vectorWrapped]))
			assert.NoError(t, err, "the recorded DEK must unwrap")
			assert.Equal(t, got, decodeHex(t, v[vectorDEK]), "Unwrap must return the recorded DEK")
		})

		t.Run("returns ErrClosed after Close", func(t *testing.T) {
			t.Parallel()
			k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
			sealed, err := k.Wrap(t.Context(), dek)
			assert.NoError(t, err, "Wrap must succeed")
			assert.FailsAfterClose(t, k.Close, func() error {
				_, err := k.Unwrap(t.Context(), sealed)

				return err
			}, kek.ErrClosed, "Unwrap after Close must return ErrClosed")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil on a second call", func(t *testing.T) {
			t.Parallel()
			k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
			assert.NoError(t, k.Close(), "the first Close must succeed")
			assert.NoError(t, k.Close(), "a second Close must succeed")
		})

		// Client 0 closes the Keeper while the other clients wrap, and the
		// history of the calls must linearize against a Keeper that is open
		// until Close takes effect. A wrap that completed must unwrap.
		t.Run("fails every Wrap that starts after it returns with ErrClosed", func(t *testing.T) {
			t.Parallel()
			parent := newParent(t)
			k, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)
			reader := mustNew(t, parent, keyIDA, wrapped)
			h := history.New()
			outcomes := history.Concurrently(5, 10*time.Second, func(client int) (any, error) {
				if client == 0 {
					call := h.Invoke(client, opClose, nil)
					call.OK(k.Close())

					return client, nil
				}
				var sealed [][]byte
				for range 50 {
					call := h.Invoke(client, opWrap, nil)
					s, err := k.Wrap(t.Context(), dek)
					call.OK(err)
					if err == nil {
						sealed = append(sealed, s)
					}
				}

				return sealed, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every client must finish")
			}
			history.Linearizable(t, h, history.Spec[bool]{
				Initial: func() bool { return false },
				Next: func(closed bool, op history.Operation) []bool {
					if op.Name == opClose {
						if !op.Returned(nil) {
							return nil
						}

						return []bool{true}
					}
					got, _ := op.Output.(error)
					if !op.Known || closed && errors.Is(got, kek.ErrClosed) || !closed && got == nil {
						return []bool{closed}
					}

					return nil
				},
			}, "a Wrap must succeed before Close takes effect and return ErrClosed after it")
			for _, o := range outcomes {
				sealed, _ := o.Output.([][]byte)
				for _, s := range sealed {
					got, err := reader.Unwrap(t.Context(), s)
					assert.NoError(t, err, "a wrap that completed must unwrap")
					expect.Equal(t, got, dek, "Unwrap must return the DEK")
				}
			}
		})
	})
}

// TestFIPSOnlyMode checks that a Keeper works in Go's FIPS 140-only mode.
// The mode is fixed when the process starts, so the test runs itself
// again in a child process with GODEBUG=fips140=only.
func TestFIPSOnlyMode(t *testing.T) {
	t.Parallel()

	if !fips140.Enforced() {
		t.Run("passes in a child process under fips140=only", func(t *testing.T) {
			t.Parallel()
			cmd := exec.CommandContext(t.Context(), os.Args[0],
				"-test.run=^TestFIPSOnlyMode$", "-test.v", "-test.timeout="+childTimeout.String())
			cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
			out, err := cmd.CombinedOutput()
			assert.NoError(t, err, "the fips140=only child must pass:\n"+string(out))
			assert.Contains(t, string(out), "wraps_a_DEK_under_a_generated_KEK", "the child must run the FIPS checks")
		})

		return
	}

	t.Run("wraps a DEK under a generated KEK", func(t *testing.T) {
		t.Parallel()
		parent := newParent(t)
		k, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)
		reader := mustNew(t, parent, keyIDA, wrapped)
		assert.RoundTrip(t, func(d []byte) ([]byte, error) { return k.Wrap(t.Context(), d) },
			func(sealed []byte) ([]byte, error) { return reader.Unwrap(t.Context(), sealed) },
			dek, "Unwrap must return the DEK in FIPS 140-only mode")
	})
}

// TestKeeperAllocs checks that Wrap allocates only the wrapped DEK once a
// wrapping key exists, and that Unwrap allocates only the DEK once its
// cipher is derived. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
func TestKeeperAllocs(t *testing.T) {
	parent := newParent(t)
	k, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)
	sealed, err := k.Wrap(t.Context(), dek)
	assert.NoError(t, err, "Wrap must succeed")
	reader := mustNew(t, parent, keyIDA, wrapped)

	t.Run("Wrap", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = k.Wrap(t.Context(), dek) }, 1,
			"Wrap must allocate only the wrapped DEK")
		assert.Length(t, got, wrappedDEKSize, "the test must measure a wrapped DEK")
	})

	t.Run("Unwrap", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = k.Unwrap(t.Context(), sealed) }, 1, "Unwrap must allocate only the DEK")
		assert.Equal(t, got, dek, "the test must measure the DEK")
	})

	t.Run("Unwrap in another Keeper", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = reader.Unwrap(t.Context(), sealed) }, 1,
			"Unwrap of a cached cipher must allocate only the DEK")
		assert.Equal(t, got, dek, "the test must measure the DEK")
	})
}

// BenchmarkKeeper reports the cost of Wrap, of Unwrap under the wrapping
// key that sealed the DEK, and of Unwrap in another Keeper that has
// cached the cipher of the DEK's salt, and fails when one of them
// allocates more than TestKeeperAllocs allows.
func BenchmarkKeeper(b *testing.B) {
	parent := newParent(b)
	k, wrapped := mustGenerate(b, parent, randcrypto.New(), keyIDA)
	sealed, err := k.Wrap(b.Context(), dek)
	assert.NoError(b, err, "Wrap must succeed")

	b.Run("Wrap", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = k.Wrap(b.Context(), dek)
		}

		assert.Length(b, got, wrappedDEKSize, "the benchmark must measure a wrapped DEK")
	})

	b.Run("Unwrap", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = k.Unwrap(b.Context(), sealed)
		}

		assert.Equal(b, got, dek, "the benchmark must measure the DEK")
	})

	b.Run("Unwrap in another Keeper", func(b *testing.B) {
		reader := mustNew(b, parent, keyIDA, wrapped)
		_, err := reader.Unwrap(b.Context(), sealed)
		assert.NoError(b, err, "the first Unwrap must derive and cache the cipher")
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = reader.Unwrap(b.Context(), sealed)
		}

		assert.Equal(b, got, dek, "the benchmark must measure the DEK")
	})
}

// newParent returns a local keeper over a random root key, as the
// parent of the KEKs. It fails tb when localkey refuses the root key.
func newParent(tb testing.TB) *localkey.Keeper {
	tb.Helper()

	root := make([]byte, localkey.RootKeySize)
	_, err := randcrypto.New().Read(root)
	assert.NoError(tb, err, "the root key must read")

	p, err := localkey.New(parentKeyID, root, randcrypto.New(), fake.New(time.Unix(0, 0).UTC()))
	assert.NoError(tb, err, "localkey.New must accept a 32-byte root key")

	return p
}

// destroyedParent returns a parent whose root key is destroyed, so its
// Wrap and Unwrap return crypto.ErrKeyDestroyed. It fails tb when
// Destroy fails.
func destroyedParent(tb testing.TB) *localkey.Keeper {
	tb.Helper()

	parent := newParent(tb)
	_, err := parent.Destroy(tb.Context(), parentKeyID)
	assert.NoError(tb, err, "Destroy must succeed")

	return parent
}

// mustGenerate creates a KEK for keyID under parent, and closes its
// Keeper when the test ends.
func mustGenerate(tb testing.TB, parent crypto.Keeper, r rand.Rand, keyID string) (*kek.Keeper, []byte) {
	tb.Helper()

	k, wrapped, err := kek.Generate(tb.Context(), parent, r, keyID)
	assert.NoError(tb, err, "Generate must succeed")
	tb.Cleanup(func() { _ = k.Close() })

	return k, wrapped
}

// mustNew opens the record that parent wrapped for keyID, and closes its
// Keeper when the test ends.
func mustNew(tb testing.TB, parent crypto.Keeper, keyID string, wrapped []byte) *kek.Keeper {
	tb.Helper()

	k, err := kek.New(tb.Context(), parent, randcrypto.New(), keyID, wrapped)
	assert.NoError(tb, err, "New must open the record")
	tb.Cleanup(func() { _ = k.Close() })

	return k
}

// record builds the record that the parent wraps for a KEK and a key ID,
// from the documented layout: the KEK, then the SHA-256 digest of the key
// ID framed under DomainName.
func record(kekBytes []byte, keyID string) []byte {
	f := crypto.NewFramer(nil, crypto.Domain{Name: kek.DomainName, Version: kekDomainVersion})
	f.String(keyID)
	binding := sha256.Sum256(f.Frame())

	return append(bytes.Clone(kekBytes), binding[:]...)
}

// seededKEK returns the KEK that Generate reads from seeded.New(seed).
func seededKEK(tb testing.TB, seed rand.Seed) []byte {
	tb.Helper()

	b := make([]byte, kek.KeySize)
	_, err := seeded.New(seed).Read(b)
	assert.NoError(tb, err, "the seeded source must read")

	return b
}

// decodeHex returns the bytes of the hexadecimal s. It fails tb when s
// is not hexadecimal.
func decodeHex(tb testing.TB, s string) []byte {
	tb.Helper()

	b, err := hex.DecodeString(s)
	assert.NoError(tb, err, "a field of the vector must be hexadecimal")

	return b
}

// loadVector reads the vector file into a map from keyword to value.
func loadVector(tb testing.TB) map[string]string {
	tb.Helper()

	data, err := os.ReadFile(vectorsPath)
	assert.NoError(tb, err, "the vector must read")

	fields := map[string]string{}
	for line := range strings.Lines(string(data)) {
		keyword, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || strings.HasPrefix(keyword, vectorComment) {
			continue
		}
		fields[keyword] = value
	}

	return fields
}
