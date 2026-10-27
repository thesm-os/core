// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package castest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/cas"
	"go.thesmos.sh/core/cas/memory"
	"go.thesmos.sh/core/crypto"
	"go.thesmos.sh/core/crypto/sha256"
	"go.thesmos.sh/core/crypto/sha3"
	"go.thesmos.sh/core/crypto/sha512"
	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/task"
)

// concurrentPuts is the number of goroutines that put one address at once.
const concurrentPuts = 16

// The values of the checks. Each check puts a value of its own, so the planted
// defect of a check changes the calls of its own addresses alone.
var (
	valueHasher    = []byte("the value of the check of Hasher")
	valueRoundTrip = []byte("the value of the round trip")
	valueAppended  = []byte("the value that Get appends")
	valueDuplicate = []byte("the value that a second Put puts")
	valueWrong     = []byte("the value under a wrong digest")
	valueForeign   = []byte("the value under a digest of another algorithm")
	valuePresent   = []byte("the value that Has finds")
	valueZero      = []byte("the value under the zero digest")
	valueCancelled = []byte("the value of the calls under a done context")
	valueRaced     = []byte("the value that goroutines put at once")
	valueStreamed  = []byte("the value that PutStream streams")
	valueWhole     = []byte("the value that Put writes whole and GetStream reads")
	valueFailed    = []byte("the value of a failed PutStream")
	valueKept      = []byte("the value that a restart keeps")
	valueKeptRead  = []byte("the value that a restart keeps after a PutStream")
	valueCrashed   = []byte("the value of a Put during a crash")
	valueCrashRead = []byte("the value of a PutStream during a crash")

	// The values whose addresses the checks never put. The wrong digest of
	// each check is the digest of other bytes of its own.
	neverStored      = []byte("never stored")
	neverStreamed    = []byte("never streamed")
	otherBytes       = []byte("other bytes entirely")
	otherStreamBytes = []byte("other bytes of a stream")
	otherKeptBytes   = []byte("other bytes of a restart")

	// prefix is the bytes of dst that StoreGetAppendsToItsBuffer passes to Get.
	prefix = []byte("keep-")
)

// The errors of the readers and of the planted defects of the checks.
var (
	// errInterrupted is the error that a failingReader returns after its data.
	errInterrupted = errors.New("castest: transfer interrupted")

	// errAbsentInvalid is the error of the planted defect of
	// StoreGetStreamReportsAbsenceAsNotFound, which classifies as Invalid.
	errAbsentInvalid = errs.WithClass(errors.New("castest: the address is refused"), errs.Invalid)

	// errEmptyInvalid is the error of the planted defect of
	// StoreStoresTheEmptyValue, which classifies as Invalid.
	errEmptyInvalid = errs.WithClass(errors.New("castest: the empty value is refused"), errs.Invalid)
)

// failingReader returns its data and then err, as a transfer that fails
// part-way does.
type failingReader struct {
	err  error
	data []byte
}

// Read copies the data that remains into p, and returns r.err once no data
// remains.
func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]

	return n, nil
}

// skewedHasher is the hasher of the planted defect of
// StoreHasherComputesWritableAddresses: the hasher of the store, whose Hash of
// valueHasher returns the digest of another algorithm.
type skewedHasher struct {
	crypto.Hasher

	other crypto.Hasher
}

// Hash returns the digest of data under the other algorithm for valueHasher,
// and under the hasher of the store for any other data.
func (s skewedHasher) Hash(data []byte) crypto.Digest {
	if bytes.Equal(data, valueHasher) {
		return s.other.Hash(data)
	}

	return s.Hasher.Hash(data)
}

// streamingStore is a store whose PutStream and GetStream are those of a
// double, the subject of the planted defect of
// StorePutStreamStoresNothingOnFailure.
type streamingStore struct {
	cas.Store
	*StreamerStub
}

// StoreHasherComputesWritableAddresses checks that a Put of a value under the
// address that Hasher computes stores the value.
//
//dokimi:check cas.Store.Hasher defect=casHasherOfAnotherAlgorithm
func StoreHasherComputesWritableAddresses(h *StoreHandle) {
	s := h.Subject()
	wrote, err := s.Put(h.Context(), s.Hasher().Hash(valueHasher), valueHasher)
	assert.NoError(h, err, h.Statement())
	assert.True(h, wrote, h.Statement())
}

// StorePutGetRoundTripsAnIndependentSlice checks that Get returns the value that
// Put stored, in bytes that the caller may change without changing the store.
//
//dokimi:check cas.Store.Get defect=casGetAliasesItsStorage
func StorePutGetRoundTripsAnIndependentSlice(h *StoreHandle) {
	s := h.Subject()
	d := put(h, s, valueRoundTrip)

	got, err := s.Get(h.Context(), d, nil)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, valueRoundTrip, h.Statement())
	got[0] ^= 0xFF

	again, err := s.Get(h.Context(), d, nil)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, again, valueRoundTrip, h.Statement())
}

// StoreGetAppendsToItsBuffer checks that Get appends the value to dst, and that
// a Get of an absent address returns dst unchanged and an error of class
// NotFound.
//
//dokimi:check cas.Store.Get defect=casGetDropsItsBuffer
func StoreGetAppendsToItsBuffer(h *StoreHandle) {
	s := h.Subject()
	d := put(h, s, valueAppended)

	got, err := s.Get(h.Context(), d, bytes.Clone(prefix))
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, append(bytes.Clone(prefix), valueAppended...), h.Statement())

	dst := bytes.Clone(prefix)
	back, err := s.Get(h.Context(), s.Hasher().Hash(neverStored), dst)
	assert.Equal(h, errs.Classify(err), errs.NotFound, h.Statement())
	assert.Equal(h, string(back), string(prefix), h.Statement())
}

// StorePutReportsADuplicateAsNotWritten checks that a second Put of a value
// succeeds and reports wrote=false.
//
//dokimi:check cas.Store.Put defect=casReportsEveryWriteAsNew
func StorePutReportsADuplicateAsNotWritten(h *StoreHandle) {
	s := h.Subject()
	d := put(h, s, valueDuplicate)

	wrote, err := s.Put(h.Context(), d, valueDuplicate)
	assert.NoError(h, err, h.Statement())
	assert.False(h, wrote, h.Statement())
}

// StorePutRefusesAWrongDigest checks that a Put of a value under the digest of
// other bytes stores nothing and returns an error of class Integrity.
//
//dokimi:check cas.Store.Put defect=casAcceptsAWrongDigest
func StorePutRefusesAWrongDigest(h *StoreHandle) {
	s := h.Subject()
	d := s.Hasher().Hash(otherBytes)

	wrote, err := s.Put(h.Context(), d, valueWrong)
	assert.False(h, wrote, h.Statement())
	assert.Equal(h, errs.Classify(err), errs.Integrity, h.Statement())
	ok, err := s.Has(h.Context(), d)
	assert.NoError(h, err, h.Statement())
	assert.False(h, ok, h.Statement())
}

// StorePutRefusesADigestOfAnotherAlgorithm checks that a Put of a value under
// its digest of another algorithm of the same width stores nothing and returns
// an error of class Integrity.
//
//dokimi:check cas.Store.Put defect=casAcceptsADigestOfAnotherAlgorithm
func StorePutRefusesADigestOfAnotherAlgorithm(h *StoreHandle) {
	s := h.Subject()
	other, ok := foreignOf(s.Hasher())
	if !ok {
		h.Skipf("the kit knows no hasher of another algorithm with the width of %s", s.Hasher().Algorithm())
	}
	d := other.Hash(valueForeign)

	wrote, err := s.Put(h.Context(), d, valueForeign)
	assert.False(h, wrote, h.Statement())
	assert.Equal(h, errs.Classify(err), errs.Integrity, h.Statement())
	present, err := s.Has(h.Context(), d)
	assert.NoError(h, err, h.Statement())
	assert.False(h, present, h.Statement())
}

// StoreHasAgreesWithGet checks that Has reports a stored address and does not
// report an absent one, and that Get of the absent address returns an error of
// class NotFound.
//
//dokimi:check cas.Store.Has defect=casHasReportsAnAbsentAddress
func StoreHasAgreesWithGet(h *StoreHandle) {
	s := h.Subject()
	present := put(h, s, valuePresent)
	absent := s.Hasher().Hash(neverStored)

	ok, err := s.Has(h.Context(), present)
	assert.NoError(h, err, h.Statement())
	assert.True(h, ok, h.Statement())
	ok, err = s.Has(h.Context(), absent)
	assert.NoError(h, err, h.Statement())
	assert.False(h, ok, h.Statement())
	_, err = s.Get(h.Context(), absent, nil)
	assert.Equal(h, errs.Classify(err), errs.NotFound, h.Statement())
}

// StoreRefusesTheZeroDigest checks that Put, Get, Has, cas.PutStream and
// cas.GetStream classify the zero digest as Invalid.
//
//dokimi:check cas.Store.Has defect=casHasAcceptsTheZeroDigest
func StoreRefusesTheZeroDigest(h *StoreHandle) {
	s := h.Subject()
	var zero crypto.Digest

	_, err := s.Put(h.Context(), zero, valueZero)
	assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	_, err = s.Get(h.Context(), zero, nil)
	assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	_, err = s.Has(h.Context(), zero)
	assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	_, err = cas.PutStream(h.Context(), s, zero, bytes.NewReader(valueZero))
	assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
	_, err = cas.GetStream(h.Context(), s, zero)
	assert.Equal(h, errs.Classify(err), errs.Invalid, h.Statement())
}

// StoreStoresTheEmptyValue checks that the digest of zero bytes is an address
// that Put writes, and that Get returns the empty value from it.
//
//dokimi:check cas.Store.Put defect=casRefusesTheEmptyValue
func StoreStoresTheEmptyValue(h *StoreHandle) {
	s := h.Subject()
	d := s.Hasher().Hash(nil)

	wrote, err := s.Put(h.Context(), d, nil)
	assert.NoError(h, err, h.Statement())
	assert.True(h, wrote, h.Statement())
	got, err := s.Get(h.Context(), d, nil)
	assert.NoError(h, err, h.Statement())
	assert.Empty(h, got, h.Statement())
}

// StoreReturnsTheErrorOfADoneContext checks that Put, Get, Has and
// cas.GetStream return the error of a context that ended.
//
//dokimi:check cas.Store.Has defect=casHasIgnoresItsContext
func StoreReturnsTheErrorOfADoneContext(h *StoreHandle) {
	s := h.Subject()
	d := s.Hasher().Hash(valueCancelled)

	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := s.Put(ctx, d, valueCancelled)

		return err
	}, h.Statement())
	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := s.Get(ctx, d, nil)

		return err
	}, h.Statement())
	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := s.Has(ctx, d)

		return err
	}, h.Statement())
	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := cas.GetStream(ctx, s, d)

		return err
	}, h.Statement())
}

// StorePutWritesOnceUnderConcurrency checks that one of 16 concurrent Puts of a
// value reports wrote=true, and that the value is whole after the Puts.
//
//dokimi:check cas.Store.Put defect=casReportsEveryWriteAsNew
func StorePutWritesOnceUnderConcurrency(h *StoreHandle) {
	s := h.Subject()
	d := s.Hasher().Hash(valueRaced)

	var wrote atomic.Int64
	err := putConcurrently(h.Context(), func(ctx context.Context) (bool, error) {
		return s.Put(ctx, d, valueRaced)
	}, &wrote)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, wrote.Load(), int64(1), h.Statement())
	got, err := s.Get(h.Context(), d, nil)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, valueRaced, h.Statement())
}

// StoreStreamAgreesWithTheWholeValue checks that Get returns the value that
// cas.PutStream streamed, and that cas.GetStream returns the value that Put
// wrote.
//
//dokimi:check cas.Store.Get defect=casGetChangesTheWholeValue
func StoreStreamAgreesWithTheWholeValue(h *StoreHandle) {
	s := h.Subject()
	d := s.Hasher().Hash(valueStreamed)
	wrote, err := cas.PutStream(h.Context(), s, d, bytes.NewReader(valueStreamed))
	assert.NoError(h, err, h.Statement())
	assert.True(h, wrote, h.Statement())
	got, err := s.Get(h.Context(), d, nil)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, valueStreamed, h.Statement())

	rc, err := cas.GetStream(h.Context(), s, put(h, s, valueWhole))
	assert.NoError(h, err, h.Statement())
	body, err := io.ReadAll(rc)
	assert.NoError(h, err, h.Statement())
	assert.NoError(h, rc.Close(), h.Statement())
	assert.Equal(h, body, valueWhole, h.Statement())
}

// StorePutStreamReportsADuplicateAsNotWritten checks that a second cas.PutStream
// of a value succeeds and reports wrote=false.
//
//dokimi:check cas.Store.Put defect=casReportsEveryWriteAsNew
func StorePutStreamReportsADuplicateAsNotWritten(h *StoreHandle) {
	s := h.Subject()
	d := s.Hasher().Hash(valueDuplicate)
	_, err := cas.PutStream(h.Context(), s, d, bytes.NewReader(valueDuplicate))
	assert.NoError(h, err, "PutStream streams the value")

	wrote, err := cas.PutStream(h.Context(), s, d, bytes.NewReader(valueDuplicate))
	assert.NoError(h, err, h.Statement())
	assert.False(h, wrote, h.Statement())
}

// StorePutStreamStoresNothingOnFailure checks that a cas.PutStream that fails
// for bytes that do not hash to the address, for an error of its reader or for a
// done context stores nothing.
//
//dokimi:check cas.Store.Put defect=casStreamIgnoresItsContext
func StorePutStreamStoresNothingOnFailure(h *StoreHandle) {
	s := h.Subject()

	wrong := s.Hasher().Hash(otherStreamBytes)
	wrote, err := cas.PutStream(h.Context(), s, wrong, bytes.NewReader(valueFailed))
	assert.False(h, wrote, h.Statement())
	assert.Equal(h, errs.Classify(err), errs.Integrity, h.Statement())
	assertAbsent(h, s, wrong)

	d := s.Hasher().Hash(valueFailed)
	_, err = cas.PutStream(h.Context(), s, d, &failingReader{data: valueFailed[:5], err: errInterrupted})
	assert.ErrorIs(h, err, errInterrupted, h.Statement())
	assertAbsent(h, s, d)

	assert.HonoursCancellation(h, func(ctx context.Context) error {
		_, err := cas.PutStream(ctx, s, d, bytes.NewReader(valueFailed))

		return err
	}, h.Statement())
	assertAbsent(h, s, d)
}

// StorePutStreamWritesOnceUnderConcurrency checks that one of 16 concurrent
// cas.PutStream calls of a value reports wrote=true, and that the value is whole
// after the calls.
//
//dokimi:check cas.Store.Put defect=casReportsEveryWriteAsNew
func StorePutStreamWritesOnceUnderConcurrency(h *StoreHandle) {
	s := h.Subject()
	d := s.Hasher().Hash(valueRaced)

	var wrote atomic.Int64
	err := putConcurrently(h.Context(), func(ctx context.Context) (bool, error) {
		return cas.PutStream(ctx, s, d, bytes.NewReader(valueRaced))
	}, &wrote)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, wrote.Load(), int64(1), h.Statement())
	got, err := s.Get(h.Context(), d, nil)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, valueRaced, h.Statement())
}

// StoreGetStreamReportsAbsenceAsNotFound checks that cas.GetStream of an absent
// address returns an error of class NotFound.
//
//dokimi:check cas.Store.Get defect=casReportsAbsenceAsInvalid
func StoreGetStreamReportsAbsenceAsNotFound(h *StoreHandle) {
	s := h.Subject()
	_, err := cas.GetStream(h.Context(), s, s.Hasher().Hash(neverStreamed))
	assert.Equal(h, errs.Classify(err), errs.NotFound, h.Statement())
}

// StoreKeepsEveryWriteAcrossARestart checks that a store that Handle.Restart
// opened again has the algorithm of the store, returns each value that Put or
// cas.PutStream stored before the restart, has no value of a refused Put, and
// reports wrote=false for a second Put of a value of before the restart.
//
//dokimi:check cas.Store.Put defect=casOverStorage restart=casRestartForgets
func StoreKeepsEveryWriteAcrossARestart(h *StoreHandle) {
	s := h.Subject()
	d := put(h, s, valueKept)
	sd := s.Hasher().Hash(valueKeptRead)
	_, err := cas.PutStream(h.Context(), s, sd, bytes.NewReader(valueKeptRead))
	assert.NoError(h, err, "PutStream streams the value")
	refused := s.Hasher().Hash(otherKeptBytes)
	_, err = s.Put(h.Context(), refused, valueKept)
	assert.Equal(h, errs.Classify(err), errs.Integrity, "Put refuses a value under a wrong digest")

	r := h.Restart(s)

	assert.Equal(h, r.Hasher().Algorithm(), s.Hasher().Algorithm(), h.Statement())
	got, err := r.Get(h.Context(), d, nil)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, valueKept, h.Statement())
	got, err = r.Get(h.Context(), sd, nil)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, valueKeptRead, h.Statement())
	ok, err := r.Has(h.Context(), refused)
	assert.NoError(h, err, h.Statement())
	assert.False(h, ok, h.Statement())
	wrote, err := r.Put(h.Context(), d, valueKept)
	assert.NoError(h, err, h.Statement())
	assert.False(h, wrote, h.Statement())
}

// StoreLeavesEachAddressAbsentOrWhole checks that each address that Put and
// cas.PutStream wrote during Handle.Crash is absent or contains the whole
// value after the crash, and that an address whose write returned without an
// error contains the whole value.
//
//dokimi:check cas.Store.Put defect=casOverStorage crash=casCrashTearsAValue
func StoreLeavesEachAddressAbsentOrWhole(h *StoreHandle) {
	s := h.Subject()
	d, sd := s.Hasher().Hash(valueCrashed), s.Hasher().Hash(valueCrashRead)

	var putErr, streamErr error
	r := h.Crash(s, func() {
		_, putErr = s.Put(h.Context(), d, valueCrashed)
		_, streamErr = cas.PutStream(h.Context(), s, sd, bytes.NewReader(valueCrashRead))
	})

	for _, c := range []struct {
		err   error
		value []byte
		d     crypto.Digest
	}{
		{err: putErr, value: valueCrashed, d: d},
		{err: streamErr, value: valueCrashRead, d: sd},
	} {
		got, err := r.Get(h.Context(), c.d, nil)
		if c.err != nil && errs.Classify(err) == errs.NotFound {
			continue
		}
		assert.NoError(h, err, h.Statement())
		assert.Equal(h, got, c.value, h.Statement())
	}
}

// put puts value under its digest in s, and stops the check when Put fails. It
// returns the digest.
func put(h *StoreHandle, s cas.Store, value []byte) crypto.Digest {
	h.Helper()

	d := s.Hasher().Hash(value)
	_, err := s.Put(h.Context(), d, value)
	assert.NoError(h, err, "Put stores the value")

	return d
}

// assertAbsent asserts with the statement of the check that s does not have d.
func assertAbsent(h *StoreHandle, s cas.Store, d crypto.Digest) {
	h.Helper()

	ok, err := s.Has(h.Context(), d)
	assert.NoError(h, err, h.Statement())
	assert.False(h, ok, h.Statement())
}

// putConcurrently calls put from 16 goroutines at once, adds one to wrote for
// each call that reports wrote=true, and returns the first error. The error
// returns to the goroutine of the check, because FailNow must not run on the
// goroutine of a Put.
func putConcurrently(ctx context.Context, put func(context.Context) (bool, error), wrote *atomic.Int64) error {
	return task.Run(ctx, concurrentPuts, func(_ context.Context, g *task.Group) error {
		for range concurrentPuts {
			if err := g.Go(func(ctx context.Context) error {
				ok, err := put(ctx)
				if ok {
					wrote.Add(1)
				}

				return err
			}); err != nil {
				return err
			}
		}

		return nil
	})
}

// foreignOf returns a hasher of another algorithm than h whose digests have the
// width of the digests of h, and false for an algorithm that the kit does not
// know.
func foreignOf(h crypto.Hasher) (crypto.Hasher, bool) {
	switch h.Algorithm() {
	case crypto.AlgSHA256:
		return sha3.New256(), true
	case crypto.AlgSHA3_256:
		return sha256.New(), true
	case crypto.AlgSHA384:
		return sha3.New384(), true
	case crypto.AlgSHA3_384:
		return sha512.New384(), true
	case crypto.AlgSHA512:
		return sha3.New512(), true
	case crypto.AlgSHA3_512:
		return sha512.New512(), true
	default:
		return nil, false
	}
}

// casOverStorage returns the planted defect of StoreKeepsEveryWriteAcrossARestart
// and of StoreLeavesEachAddressAbsentOrWhole: the sound store in memory, whose
// doors casRestartForgets and casCrashTearsAValue break the laws of the checks.
func casOverStorage(*StoreHandle) cas.Store {
	return memory.New(sha256.New())
}

// casRestartForgets is the restart of the planted defect of
// StoreKeepsEveryWriteAcrossARestart: a store over empty storage.
func casRestartForgets(*StoreHandle, cas.Store) cas.Store {
	return memory.New(sha256.New())
}

// casCrashTearsAValue is the crash of the planted defect of
// StoreLeavesEachAddressAbsentOrWhole: it runs call, and returns the store with
// a Get that returns each value without its last byte.
func casCrashTearsAValue(h *StoreHandle, s cas.Store, call func()) cas.Store {
	call()

	return NewStoreStub(h, StoreStubDelegateTo(s),
		WithStoreGet(func(ctx context.Context, d crypto.Digest, dst []byte) ([]byte, error) {
			got, err := s.Get(ctx, d, dst)
			if err == nil && len(got) > len(dst) {
				got = got[:len(got)-1]
			}

			return got, err
		}))
}

// casHasherOfAnotherAlgorithm returns the planted defect of
// StoreHasherComputesWritableAddresses: a store whose Hasher computes the
// address of valueHasher under SHA3-256 while Put verifies under SHA-256.
func casHasherOfAnotherAlgorithm(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())
	skewed := skewedHasher{Hasher: sound.Hasher(), other: sha3.New256()}

	return NewStoreStub(h, StoreStubDelegateTo(sound), WithStoreHasher(func() crypto.Hasher { return skewed }))
}

// casGetAliasesItsStorage returns the planted defect of
// StorePutGetRoundTripsAnIndependentSlice: a store whose Get of valueRoundTrip
// into nil returns one slice for every call.
func casGetAliasesItsStorage(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())
	d := sound.Hasher().Hash(valueRoundTrip)
	var aliased []byte

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreGet(func(ctx context.Context, got crypto.Digest, dst []byte) ([]byte, error) {
			if !got.Equal(d) || dst != nil {
				return sound.Get(ctx, got, dst)
			}
			if aliased == nil {
				value, err := sound.Get(ctx, got, nil)
				if err != nil {
					return nil, err
				}
				aliased = value
			}

			return aliased, nil
		}))
}

// casGetDropsItsBuffer returns the planted defect of StoreGetAppendsToItsBuffer:
// a store whose Get of valueAppended returns the value without the bytes of
// dst.
func casGetDropsItsBuffer(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())
	d := sound.Hasher().Hash(valueAppended)

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreGet(func(ctx context.Context, got crypto.Digest, dst []byte) ([]byte, error) {
			if got.Equal(d) {
				return sound.Get(ctx, got, nil)
			}

			return sound.Get(ctx, got, dst)
		}))
}

// casReportsEveryWriteAsNew returns the planted defect of
// StorePutReportsADuplicateAsNotWritten, StorePutWritesOnceUnderConcurrency,
// StorePutStreamReportsADuplicateAsNotWritten and
// StorePutStreamWritesOnceUnderConcurrency: a store whose every Put that
// succeeds reports wrote=true.
func casReportsEveryWriteAsNew(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
			_, err := sound.Put(ctx, d, data)

			return err == nil, err
		}))
}

// casAcceptsAWrongDigest returns the planted defect of
// StorePutRefusesAWrongDigest: a store whose Put under the digest of otherBytes
// stores nothing and returns no error.
func casAcceptsAWrongDigest(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())
	wrong := sound.Hasher().Hash(otherBytes)

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
			if d.Equal(wrong) && !bytes.Equal(data, otherBytes) {
				return false, nil
			}

			return sound.Put(ctx, d, data)
		}))
}

// casAcceptsADigestOfAnotherAlgorithm returns the planted defect of
// StorePutRefusesADigestOfAnotherAlgorithm: a store whose Put stores nothing
// and returns no error for the digest of the data under SHA3-256.
func casAcceptsADigestOfAnotherAlgorithm(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())
	other := sha3.New256()

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
			if d.Equal(other.Hash(data)) {
				return false, nil
			}

			return sound.Put(ctx, d, data)
		}))
}

// casHasReportsAnAbsentAddress returns the planted defect of
// StoreHasAgreesWithGet: a store whose Has reports the address of neverStored
// as present.
func casHasReportsAnAbsentAddress(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())
	absent := sound.Hasher().Hash(neverStored)

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreHas(func(ctx context.Context, d crypto.Digest) (bool, error) {
			if d.Equal(absent) {
				return true, nil
			}

			return sound.Has(ctx, d)
		}))
}

// casHasAcceptsTheZeroDigest returns the planted defect of
// StoreRefusesTheZeroDigest: a store whose Has reports the zero digest as
// absent.
func casHasAcceptsTheZeroDigest(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreHas(func(ctx context.Context, d crypto.Digest) (bool, error) {
			if d.IsZero() {
				return false, nil
			}

			return sound.Has(ctx, d)
		}))
}

// casRefusesTheEmptyValue returns the planted defect of
// StoreStoresTheEmptyValue: a store whose Put of the empty value returns an
// error of class Invalid.
func casRefusesTheEmptyValue(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStorePut(func(ctx context.Context, d crypto.Digest, data []byte) (bool, error) {
			if len(data) == 0 {
				return false, errEmptyInvalid
			}

			return sound.Put(ctx, d, data)
		}))
}

// casHasIgnoresItsContext returns the planted defect of
// StoreReturnsTheErrorOfADoneContext: a store whose Has answers under a context
// that ended.
func casHasIgnoresItsContext(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreHas(func(ctx context.Context, d crypto.Digest) (bool, error) {
			return sound.Has(context.WithoutCancel(ctx), d)
		}))
}

// casGetChangesTheWholeValue returns the planted defect of
// StoreStreamAgreesWithTheWholeValue: a store whose Get of valueWhole returns
// the value with its first byte changed.
func casGetChangesTheWholeValue(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())
	d := sound.Hasher().Hash(valueWhole)

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreGet(func(ctx context.Context, got crypto.Digest, dst []byte) ([]byte, error) {
			value, err := sound.Get(ctx, got, dst)
			if err == nil && got.Equal(d) && len(value) > len(dst) {
				value[len(dst)] ^= 0x01
			}

			return value, err
		}))
}

// casStreamIgnoresItsContext returns the planted defect of
// StorePutStreamStoresNothingOnFailure: a store whose PutStream streams under a
// context that ended.
func casStreamIgnoresItsContext(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())

	return streamingStore{Store: sound, StreamerStub: NewStreamerStub(h, StreamerStubDelegateTo(sound),
		WithStreamerPutStream(func(ctx context.Context, d crypto.Digest, r io.Reader) (bool, error) {
			return sound.PutStream(context.WithoutCancel(ctx), d, r)
		}))}
}

// casReportsAbsenceAsInvalid returns the planted defect of
// StoreGetStreamReportsAbsenceAsNotFound: a store whose Get of the address of
// neverStreamed returns an error of class Invalid.
func casReportsAbsenceAsInvalid(h *StoreHandle) cas.Store {
	sound := memory.New(sha256.New())
	absent := sound.Hasher().Hash(neverStreamed)

	return NewStoreStub(h, StoreStubDelegateTo(sound),
		WithStoreGet(func(ctx context.Context, d crypto.Digest, dst []byte) ([]byte, error) {
			if d.Equal(absent) {
				return dst, errAbsentInvalid
			}

			return sound.Get(ctx, d, dst)
		}))
}
