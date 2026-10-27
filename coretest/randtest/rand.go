// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package randtest

import (
	"fmt"
	"io"

	"go.dokimi.dev/assert"

	"go.thesmos.sh/core/rand"
)

// draws is the number of calls of Uint64 whose values Uint64ReturnsDistinctValues
// compares.
const draws = 16

// fillSizes are the lengths of the buffers that ReadFillsTheBuffer passes to Read.
var fillSizes = []int{1, 7, 8, 9, 32, 1024}

// ReadFillsTheBuffer checks that Read returns the length of a buffer of 1, 7, 8, 9,
// 32 or 1024 bytes and no error.
//
//dokimi:check Rand.Read defect=readsShort
func ReadFillsTheBuffer(h *RandHandle) {
	r := h.Subject()
	assert.Total(h, func(size int) error {
		n, err := r.Read(make([]byte, size))
		if err != nil {
			return err
		}
		if n != size {
			return fmt.Errorf("randtest: Read returned %d of %d bytes", n, size)
		}

		return nil
	}, fillSizes, h.Statement())
}

// ReadOfNilReturnsZero checks that Read of a nil buffer returns 0 and no error.
//
//dokimi:check Rand.Read defect=failsOnNil
func ReadOfNilReturnsZero(h *RandHandle) {
	n, err := h.Subject().Read(nil)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, n, 0, h.Statement())
}

// ReadOfEmptyReturnsZero checks that Read of an empty buffer returns 0 and no error.
//
//dokimi:check Rand.Read defect=failsOnEmpty
func ReadOfEmptyReturnsZero(h *RandHandle) {
	n, err := h.Subject().Read([]byte{})
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, n, 0, h.Statement())
}

// Uint64ReturnsDistinctValues checks that 16 calls of Uint64 return 16 distinct
// values.
//
//dokimi:check Rand.Uint64 defect=repeatsUint64
func Uint64ReturnsDistinctValues(h *RandHandle) {
	r := h.Subject()
	assert.NoDuplicates(h, func() ([]uint64, error) {
		values := make([]uint64, 0, draws)
		for range draws {
			values = append(values, r.Uint64())
		}

		return values, nil
	}, h.Statement())
}

// sound returns a double of Rand that keeps every law of the suite: Uint64 counts up
// from 1, and Read reports the length of its buffer. The options of a planted defect
// replace the call that breaks its law.
func sound(h *RandHandle, opts ...RandStubOption) rand.Rand {
	var next uint64

	return NewRandStub(h, append([]RandStubOption{
		WithRandUint64(func() uint64 {
			next++

			return next
		}),
		WithRandRead(func(p []byte) (int, error) { return len(p), nil }),
	}, opts...)...)
}

// readsShort returns the planted defect of ReadFillsTheBuffer: a Rand whose Read of a
// buffer with bytes reports one byte less than its length.
func readsShort(h *RandHandle) rand.Rand {
	return sound(h, WithRandRead(func(p []byte) (int, error) { return max(len(p)-1, 0), nil }))
}

// failsOnNil returns the planted defect of ReadOfNilReturnsZero: a Rand whose Read of
// a nil buffer returns an error.
func failsOnNil(h *RandHandle) rand.Rand {
	return sound(h, WithRandRead(func(p []byte) (int, error) {
		if p == nil {
			return 0, io.ErrShortBuffer
		}

		return len(p), nil
	}))
}

// failsOnEmpty returns the planted defect of ReadOfEmptyReturnsZero: a Rand whose Read
// of an empty buffer that is not nil returns an error.
func failsOnEmpty(h *RandHandle) rand.Rand {
	return sound(h, WithRandRead(func(p []byte) (int, error) {
		if p != nil && len(p) == 0 {
			return 0, io.ErrShortBuffer
		}

		return len(p), nil
	}))
}

// repeatsUint64 returns the planted defect of Uint64ReturnsDistinctValues: a Rand
// whose Uint64 returns 7 on every call.
func repeatsUint64(h *RandHandle) rand.Rand {
	return sound(h, WithRandUint64(func() uint64 { return 7 }))
}
