// Copyright ThesmOS B.V. 2026
// SPDX-License-Identifier: Apache-2.0

package xxh3

import (
	"encoding/binary"
	"math/bits"
)

// The primes of xxHash that XXH3-64 multiplies by.
const (
	prime32_1 = 0x9E3779B1
	prime32_2 = 0x85EBCA77
	prime32_3 = 0xC2B2AE3D
	prime64_1 = 0x9E3779B185EBCA87
	prime64_2 = 0xC2B2AE3D27D4EB4F
	prime64_3 = 0x165667B19E3779F9
	prime64_4 = 0x85EBCA77C2B2AE63
	prime64_5 = 0x27D4EB2F165667C5
	primeMx1  = 0x165667919E3779F9
	primeMx2  = 0x9FB21C651E98DF25
)

// The layout of the long path, for inputs of more than 240 bytes.
const (
	// stripeLen is the length of a stripe in bytes.
	stripeLen = 64
	// blockLen is the length of a block in bytes.
	blockLen = 1024
	// blockStripes is the number of stripes in a block.
	blockStripes = 16
	// stripeStep is the distance in bytes between the secrets of two
	// consecutive stripes of a block.
	stripeStep = 8
	// scrambleSecret is the offset of the secret of the scramble after each
	// block.
	scrambleSecret = 128
	// lastSecret is the offset of the secret of the last stripe.
	lastSecret = 121
	// mergeSecret is the offset of the secret of the merge of the
	// accumulators.
	mergeSecret = 11
)

// secret contains the 192 bytes of the default secret of XXH3. Each path of
// the hash reads it as little-endian words at offsets that the length of the
// input selects.
var secret = [192]byte{
	0xb8, 0xfe, 0x6c, 0x39, 0x23, 0xa4, 0x4b, 0xbe, 0x7c, 0x01, 0x81, 0x2c, 0xf7, 0x21, 0xad, 0x1c,
	0xde, 0xd4, 0x6d, 0xe9, 0x83, 0x90, 0x97, 0xdb, 0x72, 0x40, 0xa4, 0xa4, 0xb7, 0xb3, 0x67, 0x1f,
	0xcb, 0x79, 0xe6, 0x4e, 0xcc, 0xc0, 0xe5, 0x78, 0x82, 0x5a, 0xd0, 0x7d, 0xcc, 0xff, 0x72, 0x21,
	0xb8, 0x08, 0x46, 0x74, 0xf7, 0x43, 0x24, 0x8e, 0xe0, 0x35, 0x90, 0xe6, 0x81, 0x3a, 0x26, 0x4c,
	0x3c, 0x28, 0x52, 0xbb, 0x91, 0xc3, 0x00, 0xcb, 0x88, 0xd0, 0x65, 0x8b, 0x1b, 0x53, 0x2e, 0xa3,
	0x71, 0x64, 0x48, 0x97, 0xa2, 0x0d, 0xf9, 0x4e, 0x38, 0x19, 0xef, 0x46, 0xa9, 0xde, 0xac, 0xd8,
	0xa8, 0xfa, 0x76, 0x3f, 0xe3, 0x9c, 0x34, 0x3f, 0xf9, 0xdc, 0xbb, 0xc7, 0xc7, 0x0b, 0x4f, 0x1d,
	0x8a, 0x51, 0xe0, 0x4b, 0xcd, 0xb4, 0x59, 0x31, 0xc8, 0x9f, 0x7e, 0xc9, 0xd9, 0x78, 0x73, 0x64,
	0xea, 0xc5, 0xac, 0x83, 0x34, 0xd3, 0xeb, 0xc3, 0xc5, 0x81, 0xa0, 0xff, 0xfa, 0x13, 0x63, 0xeb,
	0x17, 0x0d, 0xdd, 0x51, 0xb7, 0xf0, 0xda, 0x49, 0xd3, 0x16, 0x55, 0x26, 0x29, 0xd4, 0x68, 0x9e,
	0x2b, 0x16, 0xbe, 0x58, 0x7d, 0x47, 0xa1, 0xfc, 0x8f, 0xf8, 0xb8, 0xd1, 0x7a, 0xd0, 0x31, 0xce,
	0x45, 0xcb, 0x3a, 0x8f, 0x95, 0x16, 0x04, 0x28, 0xaf, 0xd7, 0xfb, 0xca, 0xbb, 0x4b, 0x40, 0x7e,
}

// Hash returns XXH3-64 of b with seed 0 and the default secret, as xxHash
// 0.8 computes it. Its values do not change between processes, platforms or
// releases.
//
// Hash takes the path of XXH3 for the length of b: up to 16 bytes, 17 to 128
// bytes, 129 to 240 bytes, or more. An input of 17 to 240 bytes takes about
// one 128-bit multiplication for each 16 bytes. A longer input takes eight
// 32-bit multiplications for each 64 bytes. Hash reads b and does not keep
// it.
//
// # Allocation contract
//
// Hash does not allocate.
func Hash(b []byte) uint64 {
	le := binary.LittleEndian
	n := len(b)
	if n <= 16 {
		if n > 8 {
			lo := le.Uint64(b) ^ le.Uint64(secret[24:]) ^ le.Uint64(secret[32:])
			hi := le.Uint64(b[n-8:]) ^ le.Uint64(secret[40:]) ^ le.Uint64(secret[48:])

			return avalanche(uint64(n) + bits.ReverseBytes64(lo) + hi + mulFold(lo, hi))
		}
		if n > 3 {
			x := uint64(le.Uint32(b[n-4:])) | uint64(le.Uint32(b))<<32
			x ^= le.Uint64(secret[8:]) ^ le.Uint64(secret[16:])
			x ^= bits.RotateLeft64(x, 49) ^ bits.RotateLeft64(x, 24)
			x *= primeMx2
			x ^= x>>35 + uint64(n)
			x *= primeMx2

			return x ^ x>>28
		}
		if n > 0 {
			x := uint64(b[n-1]) | uint64(n)<<8 | uint64(b[0])<<16 | uint64(b[n>>1])<<24

			return avalanche64(x ^ uint64(le.Uint32(secret[0:])^le.Uint32(secret[4:])))
		}

		return avalanche64(le.Uint64(secret[56:]) ^ le.Uint64(secret[64:]))
	}
	if n <= 128 {
		acc := uint64(n) * prime64_1
		if n > 32 {
			if n > 64 {
				if n > 96 {
					acc += mulFold(le.Uint64(b[48:])^le.Uint64(secret[96:]), le.Uint64(b[56:])^le.Uint64(secret[104:]))
					acc += mulFold(
						le.Uint64(b[n-64:])^le.Uint64(secret[112:]),
						le.Uint64(b[n-56:])^le.Uint64(secret[120:]),
					)
				}
				acc += mulFold(le.Uint64(b[32:])^le.Uint64(secret[64:]), le.Uint64(b[40:])^le.Uint64(secret[72:]))
				acc += mulFold(le.Uint64(b[n-48:])^le.Uint64(secret[80:]), le.Uint64(b[n-40:])^le.Uint64(secret[88:]))
			}
			acc += mulFold(le.Uint64(b[16:])^le.Uint64(secret[32:]), le.Uint64(b[24:])^le.Uint64(secret[40:]))
			acc += mulFold(le.Uint64(b[n-32:])^le.Uint64(secret[48:]), le.Uint64(b[n-24:])^le.Uint64(secret[56:]))
		}
		acc += mulFold(le.Uint64(b)^le.Uint64(secret[0:]), le.Uint64(b[8:])^le.Uint64(secret[8:]))
		acc += mulFold(le.Uint64(b[n-16:])^le.Uint64(secret[16:]), le.Uint64(b[n-8:])^le.Uint64(secret[24:]))

		return avalanche(acc)
	}
	if n <= 240 {
		p := (*[128]byte)(b)
		acc := uint64(n) * prime64_1
		acc += mulFold(le.Uint64(p[0:])^le.Uint64(secret[0:]), le.Uint64(p[8:])^le.Uint64(secret[8:]))
		acc += mulFold(le.Uint64(p[16:])^le.Uint64(secret[16:]), le.Uint64(p[24:])^le.Uint64(secret[24:]))
		acc += mulFold(le.Uint64(p[32:])^le.Uint64(secret[32:]), le.Uint64(p[40:])^le.Uint64(secret[40:]))
		acc += mulFold(le.Uint64(p[48:])^le.Uint64(secret[48:]), le.Uint64(p[56:])^le.Uint64(secret[56:]))
		acc += mulFold(le.Uint64(p[64:])^le.Uint64(secret[64:]), le.Uint64(p[72:])^le.Uint64(secret[72:]))
		acc += mulFold(le.Uint64(p[80:])^le.Uint64(secret[80:]), le.Uint64(p[88:])^le.Uint64(secret[88:]))
		acc += mulFold(le.Uint64(p[96:])^le.Uint64(secret[96:]), le.Uint64(p[104:])^le.Uint64(secret[104:]))
		acc += mulFold(le.Uint64(p[112:])^le.Uint64(secret[112:]), le.Uint64(p[120:])^le.Uint64(secret[120:]))
		acc = avalanche(acc)
		if n >= 144 {
			acc += mulFold(le.Uint64(b[128:])^le.Uint64(secret[3:]), le.Uint64(b[136:])^le.Uint64(secret[11:]))
		}
		if n >= 160 {
			acc += mulFold(le.Uint64(b[144:])^le.Uint64(secret[19:]), le.Uint64(b[152:])^le.Uint64(secret[27:]))
		}
		if n >= 176 {
			acc += mulFold(le.Uint64(b[160:])^le.Uint64(secret[35:]), le.Uint64(b[168:])^le.Uint64(secret[43:]))
		}
		if n >= 192 {
			acc += mulFold(le.Uint64(b[176:])^le.Uint64(secret[51:]), le.Uint64(b[184:])^le.Uint64(secret[59:]))
		}
		if n >= 208 {
			acc += mulFold(le.Uint64(b[192:])^le.Uint64(secret[67:]), le.Uint64(b[200:])^le.Uint64(secret[75:]))
		}
		if n >= 224 {
			acc += mulFold(le.Uint64(b[208:])^le.Uint64(secret[83:]), le.Uint64(b[216:])^le.Uint64(secret[91:]))
		}
		if n >= 240 {
			acc += mulFold(le.Uint64(b[224:])^le.Uint64(secret[99:]), le.Uint64(b[232:])^le.Uint64(secret[107:]))
		}
		acc += mulFold(le.Uint64(b[n-16:])^le.Uint64(secret[119:]), le.Uint64(b[n-8:])^le.Uint64(secret[127:]))

		return avalanche(acc)
	}

	return long(b)
}

// HashString returns Hash of the bytes of s. It does not copy s.
//
// # Allocation contract
//
// HashString does not allocate.
func HashString(s string) uint64 {
	return Hash([]byte(s))
}

// long returns XXH3-64 of b, which has more than 240 bytes.
//
// Eight accumulators take the stripes of 64 bytes of b in blocks of 16
// stripes, and a scramble mixes them after each block. The stripes of the last
// block end before the last byte of b. The last 64 bytes of b then follow as
// one more stripe, and the accumulators merge into the hash. It does not
// allocate.
func long(b []byte) uint64 {
	le := binary.LittleEndian
	acc := [8]uint64{prime32_3, prime64_1, prime64_2, prime64_3, prime64_4, prime32_2, prime64_5, prime32_1}
	n := len(b)
	rest := b
	for len(rest) > blockLen {
		accumulateBlock(&acc, (*[blockLen]byte)(rest))
		for i := range acc {
			acc[i] = (acc[i] ^ acc[i]>>47 ^ le.Uint64(secret[scrambleSecret+8*i:])) * prime32_1
		}
		rest = rest[blockLen:]
	}
	accumulate(&acc, rest, secret[:], (len(rest)-1)/stripeLen)
	accumulate(&acc, b[n-stripeLen:], secret[lastSecret:], 1)

	h := uint64(n) * prime64_1
	h += mulFold(acc[0]^le.Uint64(secret[mergeSecret:]), acc[1]^le.Uint64(secret[mergeSecret+8:]))
	h += mulFold(acc[2]^le.Uint64(secret[mergeSecret+16:]), acc[3]^le.Uint64(secret[mergeSecret+24:]))
	h += mulFold(acc[4]^le.Uint64(secret[mergeSecret+32:]), acc[5]^le.Uint64(secret[mergeSecret+40:]))
	h += mulFold(acc[6]^le.Uint64(secret[mergeSecret+48:]), acc[7]^le.Uint64(secret[mergeSecret+56:]))

	return avalanche(h)
}

// accumulate adds count stripes of 64 bytes from the start of b to the
// accumulators acc. Stripe j takes the secret from byte 8j of keys. b has at
// least 64 * count bytes, and keys at least 8 * count + 56. It does not
// allocate.
//
// Each lane takes the low 32 bits of its word with a conversion, which
// compiles to one move. A mask compiles to two instructions.
//
//nolint:gosec // G115: each lane multiplies the low 32 bits of its word by the high 32 bits
func accumulate(acc *[8]uint64, b, keys []byte, count int) {
	le := binary.LittleEndian
	for ; count > 0; count-- {
		s, k := (*[stripeLen]byte)(b), (*[stripeLen]byte)(keys)
		v0, v1 := le.Uint64(s[0:]), le.Uint64(s[8:])
		x0, x1 := v0^le.Uint64(k[0:]), v1^le.Uint64(k[8:])
		acc[0] += v1 + uint64(uint32(x0))*(x0>>32)
		acc[1] += v0 + uint64(uint32(x1))*(x1>>32)
		v2, v3 := le.Uint64(s[16:]), le.Uint64(s[24:])
		x2, x3 := v2^le.Uint64(k[16:]), v3^le.Uint64(k[24:])
		acc[2] += v3 + uint64(uint32(x2))*(x2>>32)
		acc[3] += v2 + uint64(uint32(x3))*(x3>>32)
		v4, v5 := le.Uint64(s[32:]), le.Uint64(s[40:])
		x4, x5 := v4^le.Uint64(k[32:]), v5^le.Uint64(k[40:])
		acc[4] += v5 + uint64(uint32(x4))*(x4>>32)
		acc[5] += v4 + uint64(uint32(x5))*(x5>>32)
		v6, v7 := le.Uint64(s[48:]), le.Uint64(s[56:])
		x6, x7 := v6^le.Uint64(k[48:]), v7^le.Uint64(k[56:])
		acc[6] += v7 + uint64(uint32(x6))*(x6>>32)
		acc[7] += v6 + uint64(uint32(x7))*(x7>>32)
		b, keys = b[stripeLen:], keys[stripeStep:]
	}
}

// accumulateBlock adds the 16 stripes of blk to the accumulators acc, as
// accumulate does with the secret from byte 0. It reads each stripe and its
// secret at offsets of the stripe's index, so the compiler proves every read
// in bounds and the loop moves no slice. It does not allocate.
//
//nolint:gosec // G115: each lane multiplies the low 32 bits of its word by the high 32 bits
func accumulateBlock(acc *[8]uint64, blk *[blockLen]byte) {
	le := binary.LittleEndian
	for i := range blockStripes {
		s, k := (*[stripeLen]byte)(blk[stripeLen*i:]), (*[stripeLen]byte)(secret[stripeStep*i:])
		v0, v1 := le.Uint64(s[0:]), le.Uint64(s[8:])
		x0, x1 := v0^le.Uint64(k[0:]), v1^le.Uint64(k[8:])
		acc[0] += v1 + uint64(uint32(x0))*(x0>>32)
		acc[1] += v0 + uint64(uint32(x1))*(x1>>32)
		v2, v3 := le.Uint64(s[16:]), le.Uint64(s[24:])
		x2, x3 := v2^le.Uint64(k[16:]), v3^le.Uint64(k[24:])
		acc[2] += v3 + uint64(uint32(x2))*(x2>>32)
		acc[3] += v2 + uint64(uint32(x3))*(x3>>32)
		v4, v5 := le.Uint64(s[32:]), le.Uint64(s[40:])
		x4, x5 := v4^le.Uint64(k[32:]), v5^le.Uint64(k[40:])
		acc[4] += v5 + uint64(uint32(x4))*(x4>>32)
		acc[5] += v4 + uint64(uint32(x5))*(x5>>32)
		v6, v7 := le.Uint64(s[48:]), le.Uint64(s[56:])
		x6, x7 := v6^le.Uint64(k[48:]), v7^le.Uint64(k[56:])
		acc[6] += v7 + uint64(uint32(x6))*(x6>>32)
		acc[7] += v6 + uint64(uint32(x7))*(x7>>32)
	}
}

// mulFold returns the exclusive or of the high and the low 64 bits of the
// 128-bit product of x and y. It does not allocate.
func mulFold(x, y uint64) uint64 {
	hi, lo := bits.Mul64(x, y)

	return hi ^ lo
}

// avalanche returns the final mix of XXH3 of x. It does not allocate.
func avalanche(x uint64) uint64 {
	x ^= x >> 37
	x *= primeMx1

	return x ^ x>>32
}

// avalanche64 returns the final mix of XXH64 of x, which XXH3 takes for an
// input of up to 3 bytes. It does not allocate.
func avalanche64(x uint64) uint64 {
	x ^= x >> 33
	x *= prime64_2
	x ^= x >> 29
	x *= prime64_3

	return x ^ x>>32
}
