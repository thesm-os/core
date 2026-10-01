// Copyright Thesmos 2026
// SPDX-License-Identifier: Apache-2.0

package note_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"go.thesmos.sh/testkit"

	"go.thesmos.sh/core/errs"
	"go.thesmos.sh/core/note"
)

// Verifier keys of other implementations, which ParseKey must accept and
// AppendText must write back byte for byte.
const (
	// exampleKey is the verifier key of the example of signed-note.
	exampleKey = "example.com/foo+530d903a+AekyeRrm56hApGFkyQR4ZCbV54Id2LKaANYcrnKv3U2k"

	// exampleName is the key name of exampleKey.
	exampleName = "example.com/foo"

	// exampleTypeAndKey is the base64 of the type and the Ed25519 public
	// key of exampleKey.
	exampleTypeAndKey = "AekyeRrm56hApGFkyQR4ZCbV54Id2LKaANYcrnKv3U2k"

	// peterKey and enochKey are the verifier keys of the tests of
	// golang.org/x/mod v0.41.0, sumdb/note/note_test.go.
	peterKey = "PeterNeumann+c74f20a3+ARpc2QcUPDhMQegwxbzhKqiBfsVkmqq/LDE4izWy10TW"
	enochKey = "EnochRoot+af0cfe78+ATtqJ7zOtqQtYqOo0CpvDXNlMhV3HeJDpjrASKGLWdop"

	// plusKey and cosignatureKey are verifier keys of types 0x01 and 0x04
	// of the tests of transparency-dev/formats v0.1.1,
	// note/note_cosigv1_test.go. The base64 of plusKey contains a '+'.
	plusKey        = "TEST+7997405c+AQcC+FTVKf0jlTdHDY3rbevmnKxxPjigCXlVtGe6RIr6"
	cosignatureKey = "remora.n621.de+da77ade7+BOvN63jn/bLvkieywe8R6UYAtVtNbZpXh34x7onlmtw2"

	// mldsaKeyFile holds the verifier key of type 0x06 of the ML-DSA-44 log
	// of filippo.io/torchwood v0.10.0, cmd/litewitness/testdata/mldsa.txt.
	mldsaKeyFile = "testdata/mldsa44.vkey"

	// wrongIDKeyFile holds the verifier key of type 0x06 of the tests of
	// transparency-dev/formats v0.1.1, note/note_cosigv1_test.go. Its key ID
	// is not the derivation of signed-note, which that module does not
	// check.
	wrongIDKeyFile = "testdata/mldsa44-wrong-id.vkey"
)

const (
	// chunkBytes is the number of bytes whose padded base64 is 256
	// characters, the number that ParseKey decodes at a time, and ends in
	// two padding characters.
	chunkBytes = 190

	// flagSet and keyFlag name the flag set and the flag of the case that
	// sets a Key through the flag package.
	flagSet = "checkpoint"
	keyFlag = "key"
)

// readKey returns the verifier key that the line of file holds.
func readKey(tb testing.TB, file string) string {
	tb.Helper()

	b, err := os.ReadFile(file)
	testkit.NoError(tb, err, "the fixture "+file+" must be readable")

	return strings.TrimSuffix(string(b), "\n")
}

// vkey returns the verifier key of name and the bytes of its type and
// public key, with the key ID that the test computes itself, so that a
// case refuses a key only for the rule that it names.
func vkey(name string, typeAndKey []byte) string {
	sum := sha256.Sum256(append([]byte(name+"\n"), typeAndKey...))

	return name + "+" + hex.EncodeToString(sum[:4]) + "+" + base64.StdEncoding.EncodeToString(typeAndKey)
}

// splitVkey returns the verifier key of name and the bytes head ‖ tail of
// its type and public key, with head and tail encoded in base64 one after
// the other. The base64 of head ends in padding before the base64 of tail
// when the length of head is not a multiple of 3.
func splitVkey(name string, head, tail []byte) string {
	sum := sha256.Sum256(append(append([]byte(name+"\n"), head...), tail...))

	return name + "+" + hex.EncodeToString(sum[:4]) + "+" +
		base64.StdEncoding.EncodeToString(head) + base64.StdEncoding.EncodeToString(tail)
}

// publicKey returns 32 bytes of public key material: byte i is i.
func publicKey() []byte {
	pub := make([]byte, 32)
	for i := range pub {
		pub[i] = byte(i)
	}

	return pub
}

// mustParseKey returns the key of vkey, and fails the test when ParseKey
// refuses it.
func mustParseKey(tb testing.TB, vkey string) note.Key {
	tb.Helper()

	k, err := note.ParseKey(vkey)
	testkit.NoError(tb, err, "ParseKey must accept "+vkey)

	return k
}

// randomKey returns a valid key: a random name, an assigned type or a
// type without an assigned byte, and a public key of 1 to 64 bytes.
func randomKey(tb testing.TB, r *rand.Rand) note.Key {
	tb.Helper()

	typ := note.Type([]byte{byte(r.IntN(0xff))})
	if r.IntN(2) == 0 {
		var err error

		typ, err = note.NewType(strings.Repeat("x", 1+r.IntN(255)))
		testkit.NoError(tb, err, "NewType must accept an identifier of 1 to 255 bytes")
	}

	return note.Key{Name: randomName(r), Type: typ, PublicKey: randomBytes(r, 1, 64)}
}

func TestKey(t *testing.T) {
	t.Parallel()

	// refused are the texts that ParseKey and UnmarshalText refuse with
	// ErrKey, one for each rule of a verifier key.
	refused := []struct {
		name string
		give string
	}{
		{name: "returns ErrKey for a key without a plus", give: "example.com/foo"},
		{name: "returns ErrKey for a key without a second plus", give: "example.com/foo+530d903a"},
		{name: "returns ErrKey for an invalid name", give: vkey("a b", append([]byte{1}, publicKey()...))},
		{
			name: "returns ErrKey for a key ID of 7 digits",
			give: strings.Replace(exampleKey, "530d903a", "530d903", 1),
		},
		{
			name: "returns ErrKey for a key ID of uppercase digits",
			give: strings.Replace(exampleKey, "530d903a", "530D903A", 1),
		},
		{
			name: "returns ErrKey for a key ID of 10 digits",
			give: strings.Replace(exampleKey, "530d903a", "530d903a00", 1),
		},
		{
			name: "returns ErrKey for a key ID that is not hexadecimal",
			give: strings.Replace(exampleKey, "530d903a", "530d903g", 1),
		},
		{name: "returns ErrKey for base64 that is not base64", give: exampleKey + "!"},
		{name: "returns ErrKey for base64 without its padding", give: vkey("a", []byte{1, 2})[:len("a+")+8+1+3]},
		{
			name: "returns ErrKey for base64 with spare bits",
			give: strings.Replace(vkey("a", []byte{1, 2}), "AQI=", "AQJ=", 1),
		},
		{
			name: "returns ErrKey for padding that ends the first 256 characters of longer base64",
			give: splitVkey("a", append([]byte{1}, make([]byte, chunkBytes-1)...), []byte{1, 2, 3}),
		},
		{name: "returns ErrKey for base64 with a newline", give: strings.Replace(exampleKey, "Aeky", "Ae\nky", 1)},
		{
			name: "returns ErrKey for base64 with a carriage return",
			give: strings.Replace(exampleKey, "Aeky", "Ae\rky", 1),
		},
		{name: "returns ErrKey for no bytes after the key ID", give: "a+00000000+"},
		{name: "returns ErrKey for a lone 0xff", give: vkey("a", []byte{0xff})},
		{name: "returns ErrKey for a type without a public key", give: vkey("a", []byte{1})},
		{name: "returns ErrKey for a 0xff type without a public key", give: vkey("a", []byte{0xff, 1, 'x'})},
		{
			name: "returns ErrKey for an identifier that runs past the bytes",
			give: vkey("a", []byte{0xff, 5, 'x', 'y'}),
		},
		{name: "returns ErrKey for an empty identifier", give: vkey("a", []byte{0xff, 0, 1, 2})},
		{
			name: "returns ErrKey for a key ID that differs from the key",
			give: strings.Replace(exampleKey, "530d903a", "530d903b", 1),
		},
	}

	t.Run("ParseKey", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the name, the type and the public key", func(t *testing.T) {
			t.Parallel()
			raw, err := base64.StdEncoding.DecodeString(exampleTypeAndKey)
			testkit.NoError(t, err, "the fixture must decode")

			k := mustParseKey(t, exampleKey)
			testkit.Equal(t, k.Name, note.Name(exampleName), "ParseKey must return the name")
			testkit.Equal(t, k.Type, note.TypeEd25519, "ParseKey must return type 0x01")
			testkit.Equal(t, k.PublicKey, raw[1:], "ParseKey must return the 32 bytes after the type")
		})

		t.Run(
			"accepts the keys of signed-note, golang.org/x/mod, transparency-dev/formats and torchwood",
			func(t *testing.T) {
				t.Parallel()
				for _, give := range []string{
					exampleKey, peterKey, enochKey, plusKey, cosignatureKey, readKey(t, mldsaKeyFile),
				} {
					k := mustParseKey(t, give)
					testkit.Equal(t, k.String(), give, "AppendText must write the key back byte for byte")
				}
			},
		)

		t.Run("returns the types of the keys", func(t *testing.T) {
			t.Parallel()
			mldsa := mustParseKey(t, readKey(t, mldsaKeyFile))
			testkit.Equal(t, mustParseKey(t, cosignatureKey).Type, note.Type("\x04"), "remora is of type 0x04")
			testkit.Equal(t, mldsa.Type, note.Type("\x06"), "the ML-DSA-44 log is of type 0x06")
			testkit.Equal(t, len(mldsa.PublicKey), 1312, "an ML-DSA-44 key is 1312 bytes")
		})

		t.Run("returns ErrKey for the ML-DSA-44 key of transparency-dev/formats, whose key ID is not the derivation",
			func(t *testing.T) {
				t.Parallel()
				_, err := note.ParseKey(readKey(t, wrongIDKeyFile))
				testkit.ErrorIs(t, err, note.ErrKey, "ParseKey must refuse a key ID that is not the derivation")
			})

		t.Run("accepts a key of a type without an assigned byte", func(t *testing.T) {
			t.Parallel()
			give := vkey("example.com/pq", append([]byte(pqType), publicKey()...))
			k := mustParseKey(t, give)
			testkit.Equal(t, k.Type, pqType, "ParseKey must return the type of the length byte")
			testkit.Equal(t, k.PublicKey, publicKey(), "ParseKey must return the bytes after the type")
		})

		t.Run("accepts base64 that ends in two padding characters", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, vkey("a", []byte{1, 2, 3, 4}))
			testkit.Equal(t, k.PublicKey, []byte{2, 3, 4}, "ParseKey must return the bytes after the type")
		})

		t.Run("accepts base64 of 256 characters that ends in padding", func(t *testing.T) {
			t.Parallel()
			raw := append([]byte{1}, make([]byte, chunkBytes-1)...)
			k := mustParseKey(t, vkey("a", raw))
			testkit.Equal(t, k.PublicKey, raw[1:], "ParseKey must return the bytes after the type")
		})

		t.Run("accepts every key that AppendText writes", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			for range 500 {
				k := randomKey(t, r)
				got, err := note.ParseKey(k.String())
				testkit.NoError(t, err, "ParseKey must accept "+k.String())
				testkit.Equal(t, got, k, "ParseKey must return the key that AppendText wrote")
			}
		})

		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got, err := note.ParseKey(tt.give)
				testkit.ErrorIs(t, err, note.ErrKey, "ParseKey must refuse "+tt.give)
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Equal(t, got.Valid(), false, "ParseKey must return the zero Key with an error")
			})
		}
	})

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give note.Key
			want bool
		}{
			{
				name: "reports true for a key with a name, a type and a public key",
				give: note.Key{Name: "a", Type: note.TypeEd25519, PublicKey: publicKey()}, want: true,
			},
			{
				name: "reports false for an invalid name",
				give: note.Key{Name: "a b", Type: note.TypeEd25519, PublicKey: publicKey()}, want: false,
			},
			{
				name: "reports false for an invalid type",
				give: note.Key{Name: "a", Type: "\xff", PublicKey: publicKey()}, want: false,
			},
			{
				name: "reports false for an empty public key",
				give: note.Key{Name: "a", Type: note.TypeEd25519, PublicKey: []byte{}}, want: false,
			},
			{name: "reports false for the zero Key", give: note.Key{}, want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				testkit.Equal(t, tt.give.Valid(), tt.want, "Valid must report whether the key is complete")
			})
		}
	})

	t.Run("ID", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key ID of the example of signed-note", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(
				t,
				mustParseKey(t, exampleKey).ID(),
				uint32(0x530d903a),
				"ID must hash the name, the type and the key",
			)
		})

		t.Run("returns the key ID of a type without an assigned byte", func(t *testing.T) {
			t.Parallel()
			k := note.Key{Name: "example.com/pq", Type: pqType, PublicKey: publicKey()}
			want := vkey("example.com/pq", append([]byte(pqType), publicKey()...))
			testkit.Equal(t, k.String(), want, "ID must hash every byte of the type")
		})
	})

	t.Run("AppendText", func(t *testing.T) {
		t.Parallel()

		t.Run("appends the verifier key to b", func(t *testing.T) {
			t.Parallel()
			got, err := mustParseKey(t, exampleKey).AppendText([]byte("key: "))
			testkit.NoError(t, err, "AppendText must accept a valid key")
			testkit.Equal(t, string(got), "key: "+exampleKey, "AppendText must append the verifier key")
		})

		t.Run("returns b unchanged and ErrKey for a Key that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := note.Key{Name: "a"}.AppendText([]byte("key: "))
			testkit.ErrorIs(t, err, note.ErrKey, "AppendText must refuse an incomplete key")
			testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
			testkit.Equal(t, string(got), "key: ", "AppendText must return b unchanged")
		})
	})

	t.Run("MarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the verifier key", func(t *testing.T) {
			t.Parallel()
			got, err := mustParseKey(t, peterKey).MarshalText()
			testkit.NoError(t, err, "MarshalText must accept a valid key")
			testkit.Equal(t, string(got), peterKey, "MarshalText must return the verifier key")
		})

		t.Run("returns a verifier key whose capacity is its length", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			for range 500 {
				got, err := randomKey(t, r).MarshalText()
				testkit.NoError(t, err, "MarshalText must accept a valid key")
				testkit.Equal(t, cap(got), len(got), "MarshalText must allocate the verifier key at its length")
			}
		})

		t.Run("returns nil and ErrKey for a Key that is not Valid", func(t *testing.T) {
			t.Parallel()
			got, err := note.Key{}.MarshalText()
			testkit.ErrorIs(t, err, note.ErrKey, "MarshalText must refuse the zero Key")
			testkit.True(t, got == nil, "MarshalText must return nil with an error")
		})
	})

	t.Run("UnmarshalText", func(t *testing.T) {
		t.Parallel()

		t.Run("decodes a list of verifier keys from JSON", func(t *testing.T) {
			t.Parallel()
			var keys []note.Key
			err := json.Unmarshal([]byte(`["`+peterKey+`","`+enochKey+`"]`), &keys)
			testkit.NoError(t, err, "a list of verifier keys must decode")
			testkit.Equal(t, keys, []note.Key{mustParseKey(t, peterKey), mustParseKey(t, enochKey)},
				"UnmarshalText must parse each key")
		})

		t.Run("reuses the public key of k", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, peterKey)
			pub := &k.PublicKey[0]
			testkit.NoError(t, k.UnmarshalText([]byte(enochKey)), "UnmarshalText must accept the key")
			testkit.True(t, &k.PublicKey[0] == pub, "UnmarshalText must copy the public key into the one of k")
			testkit.Equal(t, k, mustParseKey(t, enochKey), "UnmarshalText must set the key")
		})

		t.Run("sets k to each key in turn", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			var k note.Key
			for range 500 {
				want := randomKey(t, r)
				testkit.NoError(t, k.UnmarshalText([]byte(want.String())), "UnmarshalText must accept "+want.String())
				testkit.Equal(t, k, want, "UnmarshalText must set the key that AppendText wrote")
			}
		})

		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				k := mustParseKey(t, peterKey)
				err := k.UnmarshalText([]byte(tt.give))
				testkit.ErrorIs(t, err, note.ErrKey, "UnmarshalText must refuse "+tt.give)
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Equal(t, k, mustParseKey(t, peterKey), "UnmarshalText must leave k unchanged")
			})
		}
	})

	t.Run("Set", func(t *testing.T) {
		t.Parallel()

		t.Run("sets k to the key of vkey", func(t *testing.T) {
			t.Parallel()
			var k note.Key
			testkit.NoError(t, k.Set(exampleKey), "Set must accept the key")
			testkit.Equal(t, k, mustParseKey(t, exampleKey), "Set must set the key of ParseKey")
		})

		t.Run("sets the Key of a flag", func(t *testing.T) {
			t.Parallel()
			var k note.Key
			flags := flag.NewFlagSet(flagSet, flag.ContinueOnError)
			flags.Var(&k, keyFlag, "the verifier key of the log")
			testkit.NoError(t, flags.Parse([]string{"-" + keyFlag, peterKey}), "the flag must take the key")
			testkit.Equal(t, k, mustParseKey(t, peterKey), "Set must set the key of the flag")
			testkit.Equal(t, flags.Lookup(keyFlag).Value.String(), peterKey, "the flag must print the key")
		})

		t.Run("reuses the public key of k", func(t *testing.T) {
			t.Parallel()
			k := mustParseKey(t, peterKey)
			pub := &k.PublicKey[0]
			testkit.NoError(t, k.Set(enochKey), "Set must accept the key")
			testkit.True(t, &k.PublicKey[0] == pub, "Set must copy the public key into the one of k")
			testkit.Equal(t, k, mustParseKey(t, enochKey), "Set must set the key")
		})

		t.Run("sets k to each key in turn", func(t *testing.T) {
			t.Parallel()
			r := testkit.SeededRand(t)
			var k note.Key
			for range 500 {
				want := randomKey(t, r)
				testkit.NoError(t, k.Set(want.String()), "Set must accept "+want.String())
				testkit.Equal(t, k, want, "Set must set the key that AppendText wrote")
			}
		})

		for _, tt := range refused {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				k := mustParseKey(t, peterKey)
				err := k.Set(tt.give)
				testkit.ErrorIs(t, err, note.ErrKey, "Set must refuse "+tt.give)
				testkit.Equal(t, errs.Classify(err), errs.Invalid, "the error must classify as Invalid")
				testkit.Equal(t, k, mustParseKey(t, peterKey), "Set must leave k unchanged")
			})
		}
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the verifier key", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, mustParseKey(t, enochKey).String(), enochKey, "String must return the verifier key")
		})

		t.Run("returns the empty string for a Key that is not Valid", func(t *testing.T) {
			t.Parallel()
			testkit.Equal(t, note.Key{}.String(), "", "String must return the empty string for the zero Key")
		})
	})
}

func BenchmarkKey(b *testing.B) {
	k := mustParseKey(b, exampleKey)
	pq := vkey("example.com/pq", append([]byte(pqType), publicKey()...))

	b.Run("ParseKey", func(b *testing.B) {
		benchAllocs(b, 1, func() { sinkKey, errSink = note.ParseKey(exampleKey) })
	})

	b.Run("ParseKey of a type without an assigned byte", func(b *testing.B) {
		benchAllocs(b, 2, func() { sinkKey, errSink = note.ParseKey(pq) })
	})

	b.Run("Valid", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkBool = k.Valid() })
	})

	b.Run("ID", func(b *testing.B) {
		benchZeroAlloc(b, func() { sinkID = k.ID() })
	})

	b.Run("AppendText", func(b *testing.B) {
		buf := make([]byte, 0, len(exampleKey))
		benchZeroAlloc(b, func() { sinkBytes, errSink = k.AppendText(buf[:0]) })
	})

	b.Run("MarshalText", func(b *testing.B) {
		benchAllocs(b, 1, func() { sinkBytes, errSink = k.MarshalText() })
	})

	b.Run("Set", func(b *testing.B) {
		var reused note.Key
		testkit.NoError(b, reused.Set(exampleKey), "Set must accept the key")
		benchZeroAlloc(b, func() { errSink = reused.Set(exampleKey) })
	})

	b.Run("Set of a type without an assigned byte", func(b *testing.B) {
		var reused note.Key
		testkit.NoError(b, reused.Set(pq), "Set must accept the key")
		benchZeroAlloc(b, func() { errSink = reused.Set(pq) })
	})

	b.Run("UnmarshalText", func(b *testing.B) {
		text := []byte(exampleKey)
		var reused note.Key
		testkit.NoError(b, reused.UnmarshalText(text), "UnmarshalText must accept the key")
		benchZeroAlloc(b, func() { errSink = reused.UnmarshalText(text) })
	})

	b.Run("UnmarshalText of a type without an assigned byte", func(b *testing.B) {
		text := []byte(pq)
		var reused note.Key
		testkit.NoError(b, reused.UnmarshalText(text), "UnmarshalText must accept the key")
		benchZeroAlloc(b, func() { errSink = reused.UnmarshalText(text) })
	})

	b.Run("String", func(b *testing.B) {
		benchAllocs(b, 1, func() { sinkString = k.String() })
	})
}
