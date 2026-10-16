// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package snappy_test

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"

	"go.dokimi.dev/assert"
)

// opticks returns the text of Isaac Newton's Opticks and its encoding by
// golang/snappy v1.0.0. The files are the test data of golang/snappy v1.0.0.
// The files of testdata/golden are the encodings of the first bytes of the
// repeated text by golang/snappy v1.0.0.
func opticks(tb assert.TB) (text, encoded []byte) {
	tb.Helper()

	text, err := os.ReadFile(filepath.Join("testdata", "Isaac.Newton-Opticks.txt"))
	assert.NoError(tb, err, "the text of the Opticks reads")
	encoded, err = os.ReadFile(filepath.Join("testdata", "Isaac.Newton-Opticks.txt.rawsnappy"))
	assert.NoError(tb, err, "the encoding of the Opticks reads")

	return text, encoded
}

// corpus returns the sources of the corpus of the encoder: 300 lengths of
// random bytes and of runs of periods 1, 2, 5 and 64, and sources of 2100,
// 4100 and 8192 bytes with a period of 2048 bytes. The encodings of the
// corpus contain literals of 60, 61, 256 and 257 bytes, and matches of 64,
// 65 to 67 and 68 bytes.
func corpus() [][]byte {
	var out [][]byte
	seed := uint64(0)
	for _, period := range []int{0, 1, 2, 5, 64} {
		for n := 1; n <= 300; n++ {
			seed++
			out = append(out, corpusSource(n, period, 0, seed))
		}
	}
	for _, every := range []int{0, 10} {
		for _, n := range []int{2100, 4100, 8192} {
			seed++
			out = append(out, corpusSource(n, 2048, every, seed))
		}
	}

	return out
}

// corpusSource returns n bytes from a PCG of seed. A byte is random below
// period, at each multiple of every, and everywhere for a period of 0. Every
// other byte repeats the byte period positions before it.
func corpusSource(n, period, every int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, 0))
	b := make([]byte, n)
	for i := range b {
		if period == 0 || i < period || (every != 0 && i%every == 0) {
			b[i] = byte(r.Uint32())
		} else {
			b[i] = b[i-period]
		}
	}

	return b
}

// leveldbBlock returns a data block of at least size bytes in the layout of
// LevelDB: entries of sorted 16-digit decimal keys with prefix compression
// and a restart point every 16 entries, then the restart offsets and their
// count. Each value has 100 bytes whose second half repeats its first 50
// random printable bytes.
func leveldbBlock(size int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed))
	var b []byte
	var restarts []uint32
	prev := ""
	for i := 0; len(b) < size; i++ {
		key := fmt.Sprintf("%016d", 1000*i)
		shared := 0
		if i%16 == 0 {
			restarts = append(restarts, uint32(len(b)))
		} else {
			for shared < len(key) && key[shared] == prev[shared] {
				shared++
			}
		}
		b = binary.AppendUvarint(b, uint64(shared))
		b = binary.AppendUvarint(b, uint64(len(key)-shared))
		b = binary.AppendUvarint(b, 100)
		b = append(b, key[shared:]...)
		var half [50]byte
		for j := range half {
			half[j] = byte(' ' + r.IntN(95))
		}
		b = append(b, half[:]...)
		b = append(b, half[:]...)
		prev = key
	}
	for _, off := range restarts {
		b = binary.LittleEndian.AppendUint32(b, off)
	}

	return binary.LittleEndian.AppendUint32(b, uint32(len(restarts)))
}
