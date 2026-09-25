// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package kek_test

import (
	"bytes"
	"context"
	"crypto/fips140"
	"crypto/sha256"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.thesmos.sh/testkit"

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

// wrappedDEKSize pins the length of a wrapped 32-byte DEK: a 16-byte
// salt and an AES-256-GCM envelope that adds 41 bytes.
const wrappedDEKSize = 89

// dek is the data key these tests wrap.
var dek = bytes.Repeat([]byte{0x5A}, 32)

// newParent returns a local keeper over a random root key, as the
// parent of the KEKs.
func newParent(tb testing.TB) *localkey.Keeper {
	tb.Helper()

	root := make([]byte, localkey.RootKeySize)
	_, err := randcrypto.New().Read(root)
	testkit.NoError(tb, err, "the root key must read")

	p, err := localkey.New(parentKeyID, root, randcrypto.New(), fake.New(time.Unix(0, 0).UTC()))
	testkit.NoError(tb, err, "localkey.New must accept a 32-byte root key")

	return p
}

// mustGenerate creates a KEK for keyID under parent, and closes its
// Keeper when the test ends.
func mustGenerate(tb testing.TB, parent crypto.Keeper, r rand.Rand, keyID string) (*kek.Keeper, []byte) {
	tb.Helper()

	k, wrapped, err := kek.Generate(tb.Context(), parent, r, keyID)
	testkit.NoError(tb, err, "Generate must succeed")
	tb.Cleanup(func() { _ = k.Close() })

	return k, wrapped
}

// mustNew opens the record that parent wrapped for keyID, and closes its
// Keeper when the test ends.
func mustNew(tb testing.TB, parent crypto.Keeper, keyID string, wrapped []byte) *kek.Keeper {
	tb.Helper()

	k, err := kek.New(tb.Context(), parent, randcrypto.New(), keyID, wrapped)
	testkit.NoError(tb, err, "New must open the record")
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
	testkit.NoError(tb, err, "the seeded source must read")

	return b
}

// generatorSpy is a parent whose GenerateKey fails the test, and which
// counts its Wrap calls.
type generatorSpy struct {
	*localkey.Keeper

	t     *testing.T
	wraps atomic.Int32
}

func (s *generatorSpy) Wrap(ctx context.Context, dek []byte) ([]byte, error) {
	s.wraps.Add(1)

	return s.Keeper.Wrap(ctx, dek) //nolint:wrapcheck // the test double passes the error through
}

func (s *generatorSpy) GenerateKey(context.Context, int) ([]byte, []byte, error) {
	s.t.Error("the parent's GenerateKey must not be called")

	return nil, nil, io.ErrUnexpectedEOF
}

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

func TestGenerate(t *testing.T) {
	t.Parallel()

	t.Run("reads the KEK from the source and wraps the documented record", func(t *testing.T) {
		t.Parallel()
		parent := newParent(t)
		_, wrapped := mustGenerate(t, parent, seeded.New(1), keyIDA)

		got, err := parent.Unwrap(t.Context(), wrapped)
		testkit.NoError(t, err, "the parent must unwrap the record")
		testkit.Equal(t, got, record(seededKEK(t, 1), keyIDA), "the record must be the KEK and its binding")
	})

	t.Run("returns a Keeper whose DEKs a Keeper from New unwraps", func(t *testing.T) {
		t.Parallel()
		parent := newParent(t)
		k, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)

		sealed, err := k.Wrap(t.Context(), dek)
		testkit.NoError(t, err, "Wrap must succeed")

		got, err := mustNew(t, parent, keyIDA, wrapped).Unwrap(t.Context(), sealed)
		testkit.NoError(t, err, "a Keeper from New must unwrap the DEK")
		testkit.Equal(t, got, dek, "Unwrap must return the DEK")
	})

	t.Run("refuses an empty key ID", func(t *testing.T) {
		t.Parallel()
		k, wrapped, err := kek.Generate(t.Context(), newParent(t), randcrypto.New(), "")
		testkit.ErrorIs(t, err, crypto.ErrKeyID, "an empty key ID must be refused")
		testkit.True(t, k == nil && wrapped == nil, "a refusal must return no Keeper and no record")
	})

	t.Run("returns the error of the source", func(t *testing.T) {
		t.Parallel()
		failing := randcrypto.NewWithReader(&testkit.FailingReader{
			Source: bytes.NewReader(nil),
			Err:    io.ErrUnexpectedEOF,
		})
		k, wrapped, err := kek.Generate(t.Context(), newParent(t), failing, keyIDA)
		testkit.ErrorIs(t, err, io.ErrUnexpectedEOF, "Generate must return the source's error")
		testkit.True(t, k == nil && wrapped == nil, "a failure must return no Keeper and no record")
	})

	t.Run("returns the error of the parent", func(t *testing.T) {
		t.Parallel()
		parent := newParent(t)
		_, err := parent.Destroy(t.Context(), parentKeyID)
		testkit.NoError(t, err, "Destroy must succeed")

		k, wrapped, err := kek.Generate(t.Context(), parent, randcrypto.New(), keyIDA)
		testkit.ErrorIs(t, err, crypto.ErrKeyDestroyed, "Generate must return the parent's error")
		testkit.True(t, k == nil && wrapped == nil, "a failure must return no Keeper and no record")
	})

	t.Run("does not call the parent's KeyGenerator", func(t *testing.T) {
		t.Parallel()
		spy := &generatorSpy{Keeper: newParent(t), t: t}
		mustGenerate(t, spy, randcrypto.New(), keyIDA)
		testkit.Equal(t, spy.wraps.Load(), int32(1), "Generate must wrap the record with one call to Wrap")
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	parent := newParent(t)
	_, wrappedA := mustGenerate(t, parent, randcrypto.New(), keyIDA)
	_, wrappedB := mustGenerate(t, parent, randcrypto.New(), keyIDB)

	t.Run("refuses a record bound to another key ID", func(t *testing.T) {
		t.Parallel()
		k, err := kek.New(t.Context(), parent, randcrypto.New(), keyIDA, wrappedB)
		testkit.ErrorIs(t, err, kek.ErrKeyIDMismatch, "a swapped record must be refused")
		testkit.True(t, k == nil, "a refusal must return no Keeper")
	})

	t.Run("refuses a record of another length", func(t *testing.T) {
		t.Parallel()
		short, err := parent.Wrap(t.Context(), make([]byte, kek.KeySize))
		testkit.NoError(t, err, "Wrap must succeed")

		_, err = kek.New(t.Context(), parent, randcrypto.New(), keyIDA, short)
		testkit.ErrorIs(t, err, crypto.ErrKeySize, "a record of another length must be refused")
	})

	t.Run("returns the error of the parent", func(t *testing.T) {
		t.Parallel()
		_, err := kek.New(t.Context(), parent, randcrypto.New(), keyIDA, wrappedA[:len(wrappedA)-1])
		testkit.Error(t, err, "a record the parent cannot unwrap must fail")
	})

	t.Run("refuses an empty key ID", func(t *testing.T) {
		t.Parallel()
		_, err := kek.New(t.Context(), parent, randcrypto.New(), "", wrappedA)
		testkit.ErrorIs(t, err, crypto.ErrKeyID, "an empty key ID must be refused")
	})
}

func TestGenerateAAD(t *testing.T) {
	t.Parallel()

	parent := newParent(t)
	_, wrappedAAD, err := kek.GenerateAAD(t.Context(), parent, randcrypto.New(), keyIDA)
	testkit.NoError(t, err, "GenerateAAD must succeed")
	_, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)

	t.Run("binds the key ID as the parent's associated data", func(t *testing.T) {
		t.Parallel()
		_, err := parent.UnwrapAAD(t.Context(), wrappedAAD, []byte(keyIDA))
		testkit.NoError(t, err, "the parent must unwrap the record with the key ID")
		_, err = parent.Unwrap(t.Context(), wrappedAAD)
		testkit.Error(t, err, "the parent must not unwrap the record without the key ID")
	})

	t.Run("opens with NewAAD and not with New", func(t *testing.T) {
		t.Parallel()
		k, err := kek.NewAAD(t.Context(), parent, randcrypto.New(), keyIDA, wrappedAAD)
		testkit.NoError(t, err, "NewAAD must open a record from GenerateAAD")
		testkit.NoError(t, k.Close(), "Close must succeed")

		_, err = kek.New(t.Context(), parent, randcrypto.New(), keyIDA, wrappedAAD)
		testkit.Error(t, err, "New must not open a record from GenerateAAD")
	})

	t.Run("NewAAD does not open a record from Generate", func(t *testing.T) {
		t.Parallel()
		_, err := kek.NewAAD(t.Context(), parent, randcrypto.New(), keyIDA, wrapped)
		testkit.Error(t, err, "NewAAD must not open a record from Generate")
	})

	t.Run("NewAAD fails at the parent for another key ID", func(t *testing.T) {
		t.Parallel()
		_, err := kek.NewAAD(t.Context(), parent, randcrypto.New(), keyIDB, wrappedAAD)
		testkit.Error(t, err, "the parent must refuse the record under another key ID")
		testkit.ErrorIsNot(t, err, kek.ErrKeyIDMismatch, "the refusal must come from the parent")
	})

	t.Run("both refuse an empty key ID", func(t *testing.T) {
		t.Parallel()
		_, _, err := kek.GenerateAAD(t.Context(), parent, randcrypto.New(), "")
		testkit.ErrorIs(t, err, crypto.ErrKeyID, "GenerateAAD must refuse an empty key ID")
		_, err = kek.NewAAD(t.Context(), parent, randcrypto.New(), "", wrappedAAD)
		testkit.ErrorIs(t, err, crypto.ErrKeyID, "NewAAD must refuse an empty key ID")
	})
}

func TestWrap(t *testing.T) {
	t.Parallel()

	t.Run("wraps a 32-byte DEK to 89 bytes", func(t *testing.T) {
		t.Parallel()
		k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
		sealed, err := k.Wrap(t.Context(), dek)
		testkit.NoError(t, err, "Wrap must succeed")
		testkit.Equal(t, len(sealed), wrappedDEKSize, "a wrapped DEK must be a salt and an envelope")
	})

	t.Run("binds the key ID, so another key ID over the same KEK fails", func(t *testing.T) {
		t.Parallel()
		parent := newParent(t)
		k, _ := mustGenerate(t, parent, seeded.New(1), keyIDA)

		other, err := parent.Wrap(t.Context(), record(seededKEK(t, 1), keyIDB))
		testkit.NoError(t, err, "Wrap must succeed")

		sealed, err := k.Wrap(t.Context(), dek)
		testkit.NoError(t, err, "Wrap must succeed")
		_, err = mustNew(t, parent, keyIDB, other).Unwrap(t.Context(), sealed)
		testkit.Error(t, err, "a DEK wrapped under one key ID must not unwrap under another")
	})

	t.Run("returns the error of the source when it derives a wrapping key", func(t *testing.T) {
		t.Parallel()
		parent := newParent(t)
		_, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)
		failing := randcrypto.NewWithReader(&testkit.FailingReader{
			Source: bytes.NewReader(nil),
			Err:    io.ErrUnexpectedEOF,
		})
		k, err := kek.New(t.Context(), parent, failing, keyIDA, wrapped)
		testkit.NoError(t, err, "New must not read the source")

		_, err = k.Wrap(t.Context(), dek)
		testkit.ErrorIs(t, err, io.ErrUnexpectedEOF, "Wrap must return the source's error")
	})
}

func TestUnwrap(t *testing.T) {
	t.Parallel()

	t.Run("refuses material with no envelope after its salt", func(t *testing.T) {
		t.Parallel()
		k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
		for _, n := range []int{0, kek.SaltSize - 1, kek.SaltSize} {
			_, err := k.Unwrap(t.Context(), make([]byte, n))
			testkit.ErrorIs(t, err, crypto.ErrCiphertextShort, "material without an envelope must be refused")
		}
	})
}

func TestClose(t *testing.T) {
	t.Parallel()

	t.Run("makes Wrap and Unwrap return ErrClosed, classified Invalid", func(t *testing.T) {
		t.Parallel()
		k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
		sealed, err := k.Wrap(t.Context(), dek)
		testkit.NoError(t, err, "Wrap must succeed")

		testkit.NoError(t, k.Close(), "Close must succeed")

		_, err = k.Wrap(t.Context(), dek)
		testkit.ErrorIs(t, err, kek.ErrClosed, "Wrap after Close must return ErrClosed")
		testkit.Equal(t, errs.Classify(err), errs.Invalid, "ErrClosed must classify as Invalid")
		_, err = k.Unwrap(t.Context(), sealed)
		testkit.ErrorIs(t, err, kek.ErrClosed, "Unwrap after Close must return ErrClosed")
	})

	t.Run("is idempotent", func(t *testing.T) {
		t.Parallel()
		k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
		testkit.NoError(t, k.Close(), "the first Close must succeed")
		testkit.NoError(t, k.Close(), "a second Close must succeed")
	})

	t.Run("lets each wrap in progress complete or return ErrClosed", func(t *testing.T) {
		t.Parallel()
		parent := newParent(t)
		k, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)
		reader := mustNew(t, parent, keyIDA, wrapped)

		const wrappers, wrapsEach = 4, 50

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			results []error
			sealed  [][]byte
		)
		for range wrappers {
			wg.Go(func() {
				for range wrapsEach {
					s, err := k.Wrap(t.Context(), dek)
					mu.Lock()
					results = append(results, err)
					if err == nil {
						sealed = append(sealed, s)
					}
					mu.Unlock()
				}
			})
		}
		testkit.NoError(t, k.Close(), "Close must succeed while wraps run")
		wg.Wait()

		for _, err := range results {
			if err != nil {
				testkit.ErrorIs(t, err, kek.ErrClosed, "a wrap that fails must fail because the Keeper closed")
			}
		}
		for _, s := range sealed {
			got, err := reader.Unwrap(t.Context(), s)
			testkit.NoError(t, err, "a wrap that completed must unwrap")
			testkit.Equal(t, got, dek, "Unwrap must return the DEK")
		}
	})
}

func TestCapabilities(t *testing.T) {
	t.Parallel()

	t.Run("reports no KeyGenerator, Destroyer or AADKeeper", func(t *testing.T) {
		t.Parallel()
		k, _ := mustGenerate(t, newParent(t), randcrypto.New(), keyIDA)
		_, ok := crypto.AsKeyGenerator(k)
		testkit.False(t, ok, "the Keeper must not report a KeyGenerator")
		_, ok = crypto.AsDestroyer(k)
		testkit.False(t, ok, "the Keeper must not report a Destroyer")
		_, ok = crypto.AsAADKeeper(k)
		testkit.False(t, ok, "the Keeper must not report an AADKeeper")
	})

	t.Run("crypto.GenerateKey over the Keeper does not call the parent", func(t *testing.T) {
		t.Parallel()
		spy := &generatorSpy{Keeper: newParent(t), t: t}
		k, _ := mustGenerate(t, spy, randcrypto.New(), keyIDA)

		_, _, err := crypto.GenerateKey(t.Context(), k, randcrypto.New(), 32)
		testkit.NoError(t, err, "GenerateKey must succeed")
		testkit.Equal(t, spy.wraps.Load(), int32(1), "GenerateKey must not call the parent")
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
			//nolint:gosec // G204: the child is this test binary, run again with a fixed pattern.
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFIPSOnlyMode$", "-test.v")
			cmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
			out, err := cmd.CombinedOutput()
			testkit.NoError(t, err, "the fips140=only child must pass:\n"+string(out))
			testkit.True(t, bytes.Contains(out, []byte("generates_a_KEK_and_wraps_a_DEK")),
				"the child must run the FIPS checks, not skip them")
		})

		return
	}

	t.Run("generates a KEK and wraps a DEK", func(t *testing.T) {
		t.Parallel()
		parent := newParent(t)
		k, wrapped := mustGenerate(t, parent, randcrypto.New(), keyIDA)

		sealed, err := k.Wrap(t.Context(), dek)
		testkit.NoError(t, err, "Wrap must succeed in FIPS 140-only mode")

		got, err := mustNew(t, parent, keyIDA, wrapped).Unwrap(t.Context(), sealed)
		testkit.NoError(t, err, "Unwrap must succeed in FIPS 140-only mode")
		testkit.Equal(t, got, dek, "Unwrap must return the DEK")
	})
}

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

// loadVector reads the vector file into a map from keyword to value.
func loadVector(tb testing.TB) map[string]string {
	tb.Helper()

	data, err := os.ReadFile(vectorsPath)
	testkit.NoError(tb, err, "the vector must read")

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

// TestVectors checks the recorded KEK vector. The record and the wrapped
// DEK are persisted layouts, so a change to the binding, the derivation
// or the layout must fail this test.
func TestVectors(t *testing.T) {
	t.Parallel()

	v := loadVector(t)
	kekBytes := testkit.MustDecodeHex(t, v[vectorKEK])
	keyID := v[vectorKeyID]
	rec := testkit.MustDecodeHex(t, v[vectorRecord])

	t.Run("the record is the KEK and its binding to the key ID", func(t *testing.T) {
		t.Parallel()
		testkit.Equal(t, kekBytes, seededKEK(t, vectorSeed), "the KEK must be the one the seeded source gives")
		testkit.Equal(t, rec, record(kekBytes, keyID), "the record must follow the documented layout")
	})

	t.Run("Generate wraps the recorded record", func(t *testing.T) {
		t.Parallel()
		parent := newParent(t)
		_, wrapped := mustGenerate(t, parent, seeded.New(vectorSeed), keyID)

		got, err := parent.Unwrap(t.Context(), wrapped)
		testkit.NoError(t, err, "the parent must unwrap the record")
		testkit.Equal(t, got, rec, "Generate must wrap the recorded record")
	})

	t.Run("a Keeper over the record unwraps the recorded DEK", func(t *testing.T) {
		t.Parallel()
		parent := newParent(t)
		wrapped, err := parent.Wrap(t.Context(), rec)
		testkit.NoError(t, err, "the parent must wrap the record")

		got, err := mustNew(t, parent, keyID, wrapped).Unwrap(t.Context(), testkit.MustDecodeHex(t, v[vectorWrapped]))
		testkit.NoError(t, err, "the recorded DEK must unwrap")
		testkit.Equal(t, got, testkit.MustDecodeHex(t, v[vectorDEK]), "Unwrap must return the recorded DEK")
	})
}

// BenchmarkKeeper reports the cost and the allocations of Wrap, of
// Unwrap under the wrapping key that sealed the DEK, and of Unwrap in
// another Keeper that has cached the cipher of the DEK's salt.
func BenchmarkKeeper(b *testing.B) {
	parent := newParent(b)
	k, wrapped := mustGenerate(b, parent, randcrypto.New(), keyIDA)

	b.Run("Wrap", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = k.Wrap(b.Context(), dek)
		}
	})

	b.Run("Unwrap", func(b *testing.B) {
		sealed, err := k.Wrap(b.Context(), dek)
		testkit.NoError(b, err, "Wrap must succeed")

		b.ReportAllocs()
		for b.Loop() {
			_, _ = k.Unwrap(b.Context(), sealed)
		}
	})

	b.Run("Unwrap in another Keeper", func(b *testing.B) {
		sealed, err := k.Wrap(b.Context(), dek)
		testkit.NoError(b, err, "Wrap must succeed")
		reader := mustNew(b, parent, keyIDA, wrapped)
		_, err = reader.Unwrap(b.Context(), sealed)
		testkit.NoError(b, err, "the first Unwrap must derive and cache the cipher")

		b.ReportAllocs()
		for b.Loop() {
			_, _ = reader.Unwrap(b.Context(), sealed)
		}
	})
}
