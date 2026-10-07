// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package localkey_test

import (
	"bytes"
	"context"
	"crypto/fips140"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
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
	"go.thesmos.sh/core/crypto/aesgcm"
	"go.thesmos.sh/core/crypto/localkey"
	"go.thesmos.sh/core/errs"
	randcrypto "go.thesmos.sh/core/rand/crypto"
)

// testKeyID is the key ID that the tests pass to New.
const testKeyID = "local/test-root-key"

// concurrentCallers is the number of goroutines that call the Keepers of
// one key table concurrently.
const concurrentCallers = 32

// The operations that the history of the race between Destroy and the
// use of the key records.
const (
	opDestroy = "destroy"
	opUnwrap  = "unwrap"
	opWrap    = "wrap"
)

// childTimeout is the -test.timeout of the child process of
// TestFIPSOnlyMode. The child runs its checks in milliseconds. The
// bound ends a child whose parent has died, which no context of the
// parent can cancel.
const childTimeout = 30 * time.Second

var (
	// rootKey and otherKey are two distinct root keys of RootKeySize bytes.
	rootKey  = []byte("contract-test-root-key-32-bytes!")
	otherKey = []byte("a-different-root-key-of-32-bytes")

	// origin is the start time of every fake clock in the tests.
	origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// errUnwrapped is the error of useCreatedKey when Unwrap returns a data
	// key other than the one it wrapped.
	errUnwrapped = errors.New("unwrapped data key differs")

	// errStillWraps is the error of useCreatedKey when a destroyed key
	// still wraps.
	errStillWraps = errors.New("destroyed key still wraps")
)

// keepingSource writes 0x01 into every byte of p and keeps p, so a test
// can read what the caller left in the buffer after the call. It is not
// safe for concurrent use.
type keepingSource struct{ seen []byte }

// Read fills p, keeps it in s.seen, and reports len(p).
func (s *keepingSource) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0x01
	}
	s.seen = p

	return len(p), nil
}

// TestKeeperContract runs the contract suite of crypto.Keeper with a
// second instance over the same root key and a Keeper of another root
// key.
func TestKeeperContract(t *testing.T) {
	t.Parallel()

	factory := func() crypto.Keeper { return mustNew(t, testKeyID, rootKey) }
	cryptotest.AssertKeeperContract(t, factory,
		append(cryptotest.KeeperContractAssertions(),
			cryptotest.KeeperCrossInstanceAssertion(factory),
			cryptotest.KeeperForeignKeyAssertion(func() crypto.Keeper {
				return mustNew(t, "local/other-root-key", otherKey)
			}),
		)...,
	)
}

// TestDestroyerContract runs the contract suite of crypto.Destroyer.
func TestDestroyerContract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertDestroyerContract(t, mustNew(t, testKeyID, rootKey))
}

// TestKeyGeneratorContract runs the contract suite of
// crypto.KeyGenerator.
func TestKeyGeneratorContract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertKeyGeneratorContract(t, mustNew(t, testKeyID, rootKey))
}

// TestAADKeeperContract runs the contract suite of crypto.AADKeeper.
func TestAADKeeperContract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertAADKeeperContract(t, mustNew(t, testKeyID, rootKey))
}

// TestKeyCreatorContract runs the contract suite of crypto.KeyCreator.
func TestKeyCreatorContract(t *testing.T) {
	t.Parallel()

	cryptotest.AssertKeyCreatorContract(t, mustNew(t, testKeyID, rootKey))
}

func TestKeeper(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Keeper for a root key of RootKeySize bytes", func(t *testing.T) {
			t.Parallel()
			_, err := localkey.New(testKeyID, make([]byte, localkey.RootKeySize), randcrypto.New(), fake.New(origin))
			assert.NoError(t, err, "New must accept the documented root-key size")
		})

		t.Run("returns ErrKeySize for a root key of another length", func(t *testing.T) {
			t.Parallel()
			prop.ErrorIs(t, func(n int) error {
				_, err := localkey.New(testKeyID, make([]byte, n), randcrypto.New(), fake.New(origin))

				return err
			}, crypto.ErrKeySize, "only a root key of RootKeySize bytes must be valid",
				prop.Using(prop.Integer(0, 64).Filter(func(n int) bool { return n != localkey.RootKeySize })),
				prop.Example(0), prop.Example(16), prop.Example(24), prop.Example(31), prop.Example(33))
		})

		t.Run("returns ErrKeyID for an empty key ID", func(t *testing.T) {
			t.Parallel()
			_, err := localkey.New("", rootKey, randcrypto.New(), fake.New(origin))
			assert.ErrorIs(t, err, crypto.ErrKeyID, "an empty key ID must be refused")
		})

		t.Run("returns a Keeper that a later write to the root key leaves unchanged", func(t *testing.T) {
			t.Parallel()
			k := bytes.Clone(rootKey)
			keeper := mustNew(t, testKeyID, k)
			wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
			assert.NoError(t, err, "Wrap must succeed")
			clear(k)
			got, err := keeper.Unwrap(t.Context(), wrapped)
			assert.NoError(t, err, "zeroing the root key of the caller must not affect the Keeper")
			assert.Equal(t, got, []byte("data-key"), "Unwrap must still return the data key")
		})
	})

	t.Run("KeyID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key ID passed to New", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, mustNew(t, testKeyID, rootKey).KeyID(), testKeyID,
				"KeyID must return the key ID passed to New")
		})
	})

	t.Run("Unwrap", func(t *testing.T) {
		t.Parallel()

		// Both AES-GCM constructions write the nonce at one offset.
		t.Run("returns the data key of an envelope that aesgcm.New sealed under the root key", func(t *testing.T) {
			t.Parallel()
			a, err := aesgcm.New(rootKey)
			assert.NoError(t, err, "aesgcm.New must accept the root key")
			sealed, err := crypto.Seal(a, randcrypto.New(), []byte("data-key"), nil)
			assert.NoError(t, err, "Seal must succeed")
			got, err := mustNew(t, testKeyID, rootKey).Unwrap(t.Context(), sealed)
			assert.NoError(t, err, "Unwrap must open an envelope from aesgcm.New")
			assert.Equal(t, got, []byte("data-key"), "Unwrap must return the data key")
		})

		t.Run("returns ErrKeyDestroyed after Destroy", func(t *testing.T) {
			t.Parallel()
			keeper := mustNew(t, testKeyID, rootKey)
			wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
			assert.NoError(t, err, "Wrap must succeed")
			_, err = keeper.Destroy(t.Context(), testKeyID)
			assert.NoError(t, err, "Destroy must succeed")
			_, err = keeper.Unwrap(t.Context(), wrapped)
			assert.ErrorIs(t, err, crypto.ErrKeyDestroyed, "Unwrap must return ErrKeyDestroyed")
		})

		t.Run("returns an error of class Denied after Destroy", func(t *testing.T) {
			t.Parallel()
			keeper := mustNew(t, testKeyID, rootKey)
			_, err := keeper.Destroy(t.Context(), testKeyID)
			assert.NoError(t, err, "Destroy must succeed")
			_, err = keeper.Unwrap(t.Context(), []byte("wrapped"))
			assert.Equal(t, errs.Classify(err), errs.Denied, "ErrKeyDestroyed must classify as Denied")
		})
	})

	t.Run("GenerateKey", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a data key of size bytes", func(t *testing.T) {
			t.Parallel()
			keeper := mustNew(t, testKeyID, rootKey)
			prop.Equal(t, func(n int) int {
				plaintext, _, _ := keeper.GenerateKey(t.Context(), n)

				return len(plaintext)
			}, func(n int) int { return n }, "the data key must have the requested size",
				prop.Using(prop.Integer(1, 128)), prop.Example(16), prop.Example(32), prop.Example(64))
		})

		t.Run("returns a wrapped key that Unwrap opens to the data key", func(t *testing.T) {
			t.Parallel()
			keeper := mustNew(t, testKeyID, rootKey)
			prop.ForAll(t, "Unwrap must open the wrapped key of GenerateKey to its data key", func(c *prop.Case) {
				plaintext, wrapped, err := keeper.GenerateKey(c.Context(), c.Draw(prop.Integer(1, 128), "size"))
				assert.NoError(c, err, "GenerateKey must succeed")
				got, err := keeper.Unwrap(c.Context(), wrapped)
				assert.NoError(c, err, "Unwrap must open the wrapped key")
				assert.Equal(c, got, plaintext, "Unwrap must return the data key")
			})
		})

		t.Run("returns ErrKeySize for a size that is not positive", func(t *testing.T) {
			t.Parallel()
			keeper := mustNew(t, testKeyID, rootKey)
			prop.ErrorIs(t, func(n int) error {
				_, _, err := keeper.GenerateKey(t.Context(), n)

				return err
			}, crypto.ErrKeySize, "a size that is not positive must be refused",
				prop.Using(prop.Integer(math.MinInt, 0)), prop.Example(0), prop.Example(-1))
		})

		t.Run("returns the error of the source", func(t *testing.T) {
			t.Parallel()
			_, _, err := failingKeeper(t).GenerateKey(t.Context(), 32)
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "GenerateKey must return the failure of the source")
		})

		t.Run("returns no key with the error of the source", func(t *testing.T) {
			t.Parallel()
			plaintext, wrapped, err := failingKeeper(t).GenerateKey(t.Context(), 32)
			assert.HasError(t, err, "the test must read from a source that fails")
			expect.Nil(t, plaintext, "no data key may accompany an error")
			expect.Nil(t, wrapped, "no wrapped key may accompany an error")
		})

		t.Run("returns ErrKeyDestroyed after Destroy", func(t *testing.T) {
			t.Parallel()
			keeper := mustNew(t, testKeyID, rootKey)
			_, err := keeper.Destroy(t.Context(), testKeyID)
			assert.NoError(t, err, "Destroy must succeed")
			_, _, err = keeper.GenerateKey(t.Context(), 32)
			assert.ErrorIs(t, err, crypto.ErrKeyDestroyed, "a destroyed Keeper must not generate keys")
		})
	})

	t.Run("Destroy", func(t *testing.T) {
		t.Parallel()

		t.Run("returns ErrKeyID for an unknown key ID", func(t *testing.T) {
			t.Parallel()
			_, err := mustNew(t, testKeyID, rootKey).Destroy(t.Context(), "local/not-this-one")
			assert.ErrorIs(t, err, crypto.ErrKeyID, "Destroy must refuse a key ID that it does not have")
		})

		t.Run("returns an error of class NotFound for an unknown key ID", func(t *testing.T) {
			t.Parallel()
			_, err := mustNew(t, testKeyID, rootKey).Destroy(t.Context(), "local/not-this-one")
			assert.Equal(t, errs.Classify(err), errs.NotFound, "ErrKeyID must classify as NotFound")
		})

		t.Run("returns the time of the clock on the first call", func(t *testing.T) {
			t.Parallel()
			got, err := mustNew(t, testKeyID, rootKey).Destroy(t.Context(), testKeyID)
			assert.NoError(t, err, "Destroy must succeed")
			assert.Equal(t, got, origin, "Destroy must return the time of the clock")
		})

		t.Run("returns the time of the first call on a later call", func(t *testing.T) {
			t.Parallel()
			c := fake.New(origin)
			keeper, err := localkey.New(testKeyID, rootKey, randcrypto.New(), c)
			assert.NoError(t, err, "New must accept a root key of RootKeySize bytes")
			_, err = keeper.Destroy(t.Context(), testKeyID)
			assert.NoError(t, err, "the first Destroy must succeed")
			c.Advance(time.Hour)
			again, err := keeper.Destroy(t.Context(), testKeyID)
			assert.NoError(t, err, "a second Destroy must succeed")
			assert.Equal(t, again, origin, "a second Destroy must return the time of the first call")
		})

		t.Run("destroys a created key", func(t *testing.T) {
			t.Parallel()
			creator := mustNew(t, testKeyID, rootKey)
			created, err := creator.CreateKey(t.Context())
			assert.NoError(t, err, "CreateKey must succeed")
			_, err = creator.Destroy(t.Context(), created.KeyID())
			assert.NoError(t, err, "the creator must destroy a created key")
			_, err = created.Wrap(t.Context(), []byte("data-key"))
			assert.ErrorIs(t, err, crypto.ErrKeyDestroyed, "the created key must be destroyed")
		})

		t.Run("leaves the key of the creator of a destroyed key", func(t *testing.T) {
			t.Parallel()
			creator := mustNew(t, testKeyID, rootKey)
			created, err := creator.CreateKey(t.Context())
			assert.NoError(t, err, "CreateKey must succeed")
			_, err = creator.Destroy(t.Context(), created.KeyID())
			assert.NoError(t, err, "the creator must destroy a created key")
			_, err = creator.Wrap(t.Context(), []byte("data-key"))
			assert.NoError(t, err, "the key of the creator must still wrap")
		})

		// Client 0 destroys the key while the other clients wrap and unwrap,
		// and the history of the calls must linearize against a key that
		// works until Destroy takes effect.
		t.Run("fails every call that starts after it returns with ErrKeyDestroyed", func(t *testing.T) {
			t.Parallel()
			keeper := mustNew(t, testKeyID, rootKey)
			wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
			assert.NoError(t, err, "Wrap must succeed")
			h := history.New()
			outcomes := history.Concurrently(concurrentCallers, 10*time.Second, func(client int) (any, error) {
				if client == 0 {
					call := h.Invoke(client, opDestroy, nil)
					_, destroyErr := keeper.Destroy(t.Context(), testKeyID)
					call.OK(destroyErr)

					return client, nil
				}
				call := h.Invoke(client, opWrap, nil)
				_, wrapErr := keeper.Wrap(t.Context(), []byte("data-key"))
				call.OK(wrapErr)
				call = h.Invoke(client, opUnwrap, nil)
				_, unwrapErr := keeper.Unwrap(t.Context(), wrapped)
				call.OK(unwrapErr)

				return client, nil
			})
			for _, o := range outcomes {
				assert.True(t, o.Finished, "every client must finish")
			}
			history.Linearizable(t, h, history.Spec[bool]{
				Initial: func() bool { return false },
				Next: func(destroyed bool, op history.Operation) []bool {
					if op.Name == opDestroy {
						if !op.Returned(nil) {
							return nil
						}

						return []bool{true}
					}
					got, _ := op.Output.(error)
					if !op.Known || destroyed && errors.Is(got, crypto.ErrKeyDestroyed) || !destroyed && got == nil {
						return []bool{destroyed}
					}

					return nil
				},
			}, "a call must succeed before Destroy takes effect and return ErrKeyDestroyed after it")
			_, err = keeper.Wrap(t.Context(), []byte("data-key"))
			assert.ErrorIs(t, err, crypto.ErrKeyDestroyed, "Wrap must return ErrKeyDestroyed once Destroy returns")
		})
	})

	t.Run("CreateKey", func(t *testing.T) {
		t.Parallel()

		t.Run("returns keys named after the key ID passed to New", func(t *testing.T) {
			t.Parallel()
			creator := mustNew(t, testKeyID, rootKey)
			for _, want := range []string{testKeyID + "/1", testKeyID + "/2"} {
				created, err := creator.CreateKey(t.Context())
				assert.NoError(t, err, "CreateKey must succeed")
				expect.Equal(t, created.KeyID(), want, "CreateKey must number the keys it creates from 1")
			}
		})

		t.Run("numbers keys across every Keeper of the table", func(t *testing.T) {
			t.Parallel()
			first, err := mustNew(t, testKeyID, rootKey).CreateKey(t.Context())
			assert.NoError(t, err, "CreateKey must succeed")
			kc, ok := crypto.AsKeyCreator(first)
			assert.True(t, ok, "a created Keeper must be a KeyCreator")
			second, err := kc.CreateKey(t.Context())
			assert.NoError(t, err, "a created Keeper must create keys")
			assert.Equal(t, second.KeyID(), testKeyID+"/2", "a created Keeper must share the numbering of its creator")
		})

		t.Run("returns the error of the source", func(t *testing.T) {
			t.Parallel()
			_, err := failingKeeper(t).CreateKey(t.Context())
			assert.ErrorIs(t, err, io.ErrUnexpectedEOF, "CreateKey must return the failure of the source")
		})

		t.Run("returns no Keeper with the error of the source", func(t *testing.T) {
			t.Parallel()
			created, err := failingKeeper(t).CreateKey(t.Context())
			assert.HasError(t, err, "the test must read from a source that fails")
			assert.Nil(t, created, "no Keeper may accompany an error")
		})

		t.Run("zeroes the root key that it read from the source", func(t *testing.T) {
			t.Parallel()
			source := &keepingSource{}
			creator, err := localkey.New(testKeyID, rootKey, randcrypto.NewWithReader(source), fake.New(origin))
			assert.NoError(t, err, "New must accept a root key of RootKeySize bytes")
			_, err = creator.CreateKey(t.Context())
			assert.NoError(t, err, "CreateKey must succeed")
			assert.Equal(t, source.seen, make([]byte, localkey.RootKeySize),
				"the root key must be zeroed before CreateKey returns")
		})

		t.Run("adds no key when the source fails", func(t *testing.T) {
			t.Parallel()
			creator := failingKeeper(t)
			_, err := creator.CreateKey(t.Context())
			assert.HasError(t, err, "the test must read from a source that fails")
			_, err = creator.OpenKey(t.Context(), testKeyID+"/1")
			assert.ErrorIs(t, err, crypto.ErrKeyID, "a failed CreateKey must not add a key")
		})

		t.Run("returns another key to each concurrent caller", func(t *testing.T) {
			t.Parallel()
			creator := mustNew(t, testKeyID, rootKey)
			outcomes := history.Concurrently(concurrentCallers, 10*time.Second, func(int) (any, error) {
				return useCreatedKey(t.Context(), creator)
			})
			keyIDs := make([]any, 0, len(outcomes))
			for _, o := range outcomes {
				expect.True(t, o.Finished, "every client must finish")
				expect.NoError(t, o.Error, "every client must use and destroy its own key")
				keyIDs = append(keyIDs, o.Output)
			}
			assert.NoDuplicates(t, func() ([]any, error) { return keyIDs, nil },
				"no two callers may receive one key ID")
		})
	})

	t.Run("OpenKey", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a Keeper of the key passed to New", func(t *testing.T) {
			t.Parallel()
			creator := mustNew(t, testKeyID, rootKey)
			wrapped, err := creator.Wrap(t.Context(), []byte("data-key"))
			assert.NoError(t, err, "Wrap must succeed")
			opened, err := creator.OpenKey(t.Context(), testKeyID)
			assert.NoError(t, err, "OpenKey must open the key passed to New")
			got, err := opened.Unwrap(t.Context(), wrapped)
			assert.NoError(t, err, "the opened Keeper must unwrap the material of the creator")
			assert.Equal(t, got, []byte("data-key"), "the opened Keeper must return the data key")
		})

		t.Run("returns ErrKeyID for a key of another table", func(t *testing.T) {
			t.Parallel()
			other := mustNew(t, "local/other-root-key", otherKey)
			_, err := mustNew(t, testKeyID, rootKey).OpenKey(t.Context(), other.KeyID())
			assert.ErrorIs(t, err, crypto.ErrKeyID, "OpenKey must not open a key outside its table")
		})
	})
}

// TestFIPSOnlyMode checks the Keeper under GODEBUG=fips140=only. The
// mode is fixed when a process starts, so the test runs itself again
// in a child process with the mode set. In the child,
// fips140.Enforced reports true and the checks run there.
func TestFIPSOnlyMode(t *testing.T) {
	t.Parallel()

	if !fips140.Enforced() {
		t.Run("passes in a child process under fips140=only", func(t *testing.T) {
			t.Parallel()
			//nolint:gosec // G204: the child is this test binary, run again with a fixed pattern.
			cmd := exec.CommandContext(t.Context(), os.Args[0],
				"-test.run=^TestFIPSOnlyMode$", "-test.v", "-test.timeout="+childTimeout.String())
			cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
			out, err := cmd.CombinedOutput()
			assert.NoError(t, err, "the fips140=only child must pass:\n"+string(out))
			assert.Contains(t, string(out), "round-trips_a_data_key", "the child must run the FIPS checks")
		})

		return
	}

	t.Run("round-trips a data key", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		wrapped, err := keeper.Wrap(t.Context(), []byte("data-key"))
		assert.NoError(t, err, "Wrap must succeed in FIPS 140-only mode")
		got, err := keeper.Unwrap(t.Context(), wrapped)
		assert.NoError(t, err, "Unwrap must succeed in FIPS 140-only mode")
		assert.Equal(t, got, []byte("data-key"), "Unwrap must return the data key")
	})

	t.Run("generates a data key that unwraps", func(t *testing.T) {
		t.Parallel()
		keeper := mustNew(t, testKeyID, rootKey)
		plaintext, wrapped, err := keeper.GenerateKey(t.Context(), 32)
		assert.NoError(t, err, "GenerateKey must succeed in FIPS 140-only mode")
		got, err := keeper.Unwrap(t.Context(), wrapped)
		assert.NoError(t, err, "Unwrap must open the generated key")
		assert.Equal(t, got, plaintext, "Unwrap must return the generated data key")
	})

	t.Run("creates a key that round-trips a data key", func(t *testing.T) {
		t.Parallel()
		created, err := mustNew(t, testKeyID, rootKey).CreateKey(t.Context())
		assert.NoError(t, err, "CreateKey must succeed in FIPS 140-only mode")
		wrapped, err := created.Wrap(t.Context(), []byte("data-key"))
		assert.NoError(t, err, "a created key must wrap in FIPS 140-only mode")
		got, err := created.Unwrap(t.Context(), wrapped)
		assert.NoError(t, err, "a created key must unwrap in FIPS 140-only mode")
		assert.Equal(t, got, []byte("data-key"), "Unwrap must return the data key")
	})
}

// TestKeeperAllocs checks that Wrap allocates only the envelope and
// Unwrap only the data key. MaxAllocs counts the allocations of the whole
// process, so the test does not run in parallel.
//
//nolint:paralleltest // see above
func TestKeeperAllocs(t *testing.T) {
	keeper := mustNew(t, testKeyID, rootKey)
	dek := make([]byte, 32)
	wrapped, err := keeper.Wrap(t.Context(), dek)
	assert.NoError(t, err, "Wrap must succeed")

	t.Run("Wrap", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = keeper.Wrap(t.Context(), dek) }, 1,
			"Wrap must allocate only the envelope")
		assert.Length(t, got, len(wrapped), "the test must measure an envelope")
	})

	t.Run("Unwrap", func(t *testing.T) {
		var got []byte
		expect.MaxAllocs(t, func() { got, _ = keeper.Unwrap(t.Context(), wrapped) }, 1,
			"Unwrap must allocate only the data key")
		assert.Equal(t, got, dek, "the test must measure the data key")
	})
}

// BenchmarkKeeper reports the cost of Wrap and Unwrap of a data key of 32
// bytes, and fails when either allocates more than TestKeeperAllocs
// allows.
func BenchmarkKeeper(b *testing.B) {
	keeper := mustNew(b, testKeyID, rootKey)
	dek := make([]byte, 32)
	wrapped, err := keeper.Wrap(b.Context(), dek)
	assert.NoError(b, err, "Wrap must succeed")

	b.Run("Wrap", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = keeper.Wrap(b.Context(), dek)
		}

		assert.Length(b, got, len(wrapped), "the benchmark must measure an envelope")
	})

	b.Run("Unwrap", func(b *testing.B) {
		var got []byte

		c := bench.Start(b).MaxAllocs(1)
		defer c.End()

		for c.Loop() {
			got, _ = keeper.Unwrap(b.Context(), wrapped)
		}

		assert.Equal(b, got, dek, "the benchmark must measure the data key")
	})
}

// mustNew returns a Keeper over key, named keyID, with a cryptographic
// random source and a fake clock at origin. It fails tb when New returns
// an error.
func mustNew(tb testing.TB, keyID string, key []byte) *localkey.Keeper {
	tb.Helper()

	k, err := localkey.New(keyID, key, randcrypto.New(), fake.New(origin))
	assert.NoError(tb, err, "New must accept a root key of RootKeySize bytes")

	return k
}

// failingKeeper returns a Keeper over rootKey whose random source fails
// with io.ErrUnexpectedEOF. It fails tb when New returns an error.
func failingKeeper(tb testing.TB) *localkey.Keeper {
	tb.Helper()

	k, err := localkey.New(testKeyID, rootKey, randcrypto.NewWithReader(iotest.ErrReader(io.ErrUnexpectedEOF)),
		fake.New(origin))
	assert.NoError(tb, err, "New must accept a root key of RootKeySize bytes")

	return k
}

// useCreatedKey creates a key through creator and wraps a data key under
// it. It unwraps the data key through the Keeper that OpenKey returns for
// the new key ID. It then destroys the key through creator and checks that
// Wrap returns [crypto.ErrKeyDestroyed]. It returns the new key ID, or the
// first error.
func useCreatedKey(ctx context.Context, creator *localkey.Keeper) (string, error) {
	created, err := creator.CreateKey(ctx)
	if err != nil {
		return "", err
	}

	wrapped, err := created.Wrap(ctx, []byte("data-key"))
	if err != nil {
		return "", err
	}

	opened, err := creator.OpenKey(ctx, created.KeyID())
	if err != nil {
		return "", err
	}

	got, err := opened.Unwrap(ctx, wrapped)
	if err != nil {
		return "", err
	}

	if !bytes.Equal(got, []byte("data-key")) {
		return "", errUnwrapped
	}

	if _, err = creator.Destroy(ctx, created.KeyID()); err != nil {
		return "", err
	}

	if _, err = created.Wrap(ctx, []byte("data-key")); !errors.Is(err, crypto.ErrKeyDestroyed) {
		return "", errStillWraps
	}

	return created.KeyID(), nil
}
